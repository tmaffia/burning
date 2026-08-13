package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	claudeClientID      = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	claudeScope         = "org:create_api_key user:profile user:inference"
	claudeCallbackPath  = "/callback"
	claudeRefreshBefore = time.Minute
)

var (
	claudeAuthorizeURL    = "https://claude.ai/oauth/authorize"
	claudeTokenURL        = "https://platform.claude.com/v1/oauth/token"
	claudeUsageURL        = "https://api.anthropic.com/api/oauth/usage"
	claudeCallbackAddress = "127.0.0.1:0"
)

type claudeProvider struct{}

func (claudeProvider) Name() string { return "claude" }

// claudeCredential is stored only in Burning's auth.json.
type claudeCredential struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func newClaudeAuthorization(redirectURI, state string) (oauthAuthorization, error) {
	return newOAuthAuthorization(claudeAuthorizeURL, claudeClientID, claudeScope, redirectURI, state, url.Values{"code": {"true"}})
}

func startClaudeCallback(state string) (*oauthCallbackServer, error) {
	callback, err := startOAuthCallback(claudeCallbackAddress, "", claudeCallbackPath, state, "Claude authentication completed. You can close this window.")
	if err != nil {
		return nil, errors.New("could not start Claude login callback")
	}
	return callback, nil
}

// login runs Claude's browser OAuth flow and returns the Credential to store.
func (claudeProvider) login(ctx context.Context, stdin *os.File, stdout io.Writer) (json.RawMessage, error) {
	state, err := randomURLValue()
	if err != nil {
		return nil, err
	}
	callback, err := startClaudeCallback(state)
	if err != nil {
		return nil, err
	}
	defer callback.server.Close()
	authorization, err := newClaudeAuthorization(callback.redirectURI, state)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(stdout, "Open this URL in your browser:\n%s\n", authorization.url)
	if err := openURL(authorization.url); err != nil {
		fmt.Fprintln(stdout, "Could not open a browser; open the URL above.")
	}
	code, err := callback.wait(ctx)
	if err != nil {
		return nil, err
	}
	credential, err := exchangeClaudeCode(ctx, code, authorization.verifier, callback.redirectURI, state)
	if err != nil {
		return nil, err
	}
	value, err := json.Marshal(credential)
	if err != nil {
		return nil, providerFailure("claude", categoryMalformedResponse, err)
	}
	return value, nil
}

func (claudeProvider) Usage(ctx context.Context) ([]usageWindow, error) {
	credential, err := loadClaudeCredential(ctx)
	if err != nil {
		return nil, err
	}
	return fetchClaudeUsage(ctx, credential)
}

func loadClaudeCredential(ctx context.Context) (claudeCredential, error) {
	value, ok, err := credential("claude")
	if err != nil || !ok {
		return claudeCredential{}, providerFailure("claude", categoryAuthentication, err)
	}
	current, err := parseClaudeCredential(value)
	if err != nil {
		return claudeCredential{}, err
	}
	if !current.ExpiresAt.After(time.Now().Add(claudeRefreshBefore)) {
		return refreshStoredClaudeCredential(ctx)
	}
	return current, nil
}

func refreshStoredClaudeCredential(ctx context.Context) (claudeCredential, error) {
	var refreshed claudeCredential
	err := mutateCredentials(ctx, func(auth *authFile) (bool, error) {
		value, ok := auth.Credentials["claude"]
		if !ok {
			return false, providerFailure("claude", categoryAuthentication, nil)
		}
		current, err := parseClaudeCredential(value)
		if err != nil {
			return false, err
		}
		if current.ExpiresAt.After(time.Now().Add(claudeRefreshBefore)) {
			refreshed = current
			return false, nil
		}
		refreshed, err = refreshClaudeCredential(ctx, current.RefreshToken)
		if err != nil {
			return false, err
		}
		value, err = json.Marshal(refreshed)
		if err != nil {
			return false, providerFailure("claude", categoryMalformedResponse, err)
		}
		auth.Credentials["claude"] = value
		return true, nil
	})
	if err != nil {
		return claudeCredential{}, err
	}
	return refreshed, nil
}

func parseClaudeCredential(value json.RawMessage) (claudeCredential, error) {
	var cred claudeCredential
	if err := json.Unmarshal(value, &cred); err != nil || strings.TrimSpace(cred.AccessToken) == "" || strings.TrimSpace(cred.RefreshToken) == "" || cred.ExpiresAt.IsZero() {
		return claudeCredential{}, providerFailure("claude", categoryAuthentication, err)
	}
	return cred, nil
}

func exchangeClaudeCode(ctx context.Context, code, verifier, redirectURI, state string) (claudeCredential, error) {
	return requestClaudeToken(ctx, map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     claudeClientID,
		"code":          code,
		"code_verifier": verifier,
		"redirect_uri":  redirectURI,
		"state":         state,
	})
}

func refreshClaudeCredential(ctx context.Context, refreshToken string) (claudeCredential, error) {
	return requestClaudeToken(ctx, map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     claudeClientID,
		"refresh_token": refreshToken,
	})
}

func requestClaudeToken(ctx context.Context, payload map[string]string) (claudeCredential, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return claudeCredential{}, providerFailure("claude", categoryMalformedResponse, err)
	}
	response, err := postOAuthToken(ctx, "claude", claudeTokenURL, "application/json", strings.NewReader(string(body)))
	if err != nil {
		return claudeCredential{}, err
	}
	defer response.Close()
	return decodeClaudeToken(response)
}

func decodeClaudeToken(body io.Reader) (claudeCredential, error) {
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    *int64 `json:"expires_in"`
	}
	decoder := json.NewDecoder(body)
	if err := decoder.Decode(&token); err != nil {
		return claudeCredential{}, providerFailure("claude", categoryMalformedResponse, err)
	}
	if err := ensureJSONEOF(decoder); err != nil || strings.TrimSpace(token.AccessToken) == "" || strings.TrimSpace(token.RefreshToken) == "" || token.ExpiresIn == nil || *token.ExpiresIn <= 0 || *token.ExpiresIn > maxWindowSeconds {
		return claudeCredential{}, providerFailure("claude", categoryMalformedResponse, err)
	}
	return claudeCredential{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(*token.ExpiresIn) * time.Second),
	}, nil
}

type claudeUsageLimit struct {
	Kind        string     `json:"kind"`
	Utilization *float64   `json:"utilization"`
	Percent     *float64   `json:"percent"`
	ResetsAt    *time.Time `json:"resets_at"`
}

func fetchClaudeUsage(ctx context.Context, credential claudeCredential) ([]usageWindow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, claudeUsageURL, nil)
	if err != nil {
		return nil, providerFailure("claude", categoryUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, oauthHTTPFailure("claude", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, providerFailure("claude", classifyHTTPStatus(res.StatusCode), nil)
	}

	var response struct {
		Limits []claudeUsageLimit `json:"limits"`
	}
	decoder := json.NewDecoder(res.Body)
	if err := decoder.Decode(&response); err != nil {
		return nil, providerFailure("claude", categoryMalformedResponse, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, providerFailure("claude", categoryMalformedResponse, err)
	}
	windows := make(map[string]usageWindow, 2)
	for _, limit := range response.Limits {
		name, duration := claudeWindow(limit.Kind)
		if name == "" {
			continue
		}
		if _, exists := windows[name]; exists {
			return nil, providerFailure("claude", categoryMalformedResponse, nil)
		}
		window, err := normalizeClaudeWindow(name, duration, limit)
		if err != nil {
			return nil, err
		}
		windows[name] = window
	}
	if len(windows) != 2 {
		return nil, providerFailure("claude", categoryMalformedResponse, nil)
	}
	return []usageWindow{windows["session"], windows["weekly"]}, nil
}

func claudeWindow(kind string) (string, time.Duration) {
	switch kind {
	case "session":
		return "session", 5 * time.Hour
	case "weekly_all":
		return "weekly", 7 * 24 * time.Hour
	default:
		return "", 0
	}
}

func normalizeClaudeWindow(name string, duration time.Duration, limit claudeUsageLimit) (usageWindow, error) {
	utilization := limit.Utilization
	if utilization == nil {
		utilization = limit.Percent // Current API responses call this field percent.
	}
	if utilization == nil || *utilization < 0 || *utilization > 100 || limit.ResetsAt == nil || limit.ResetsAt.IsZero() {
		return usageWindow{}, providerFailure("claude", categoryMalformedResponse, nil)
	}
	return usageWindow{
		Name:     name,
		Duration: duration,
		Usage:    usageFromFraction(*utilization / 100),
		ResetsAt: *limit.ResetsAt,
	}, nil
}
