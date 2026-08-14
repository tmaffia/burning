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
	grokClientID       = "b1a00492-073a-47ea-816f-4c329264a828"
	grokScope          = "openid profile email offline_access grok-cli:access api:access"
	grokCallbackPath   = "/callback"
	grokRefreshBefore  = time.Minute
	grokWeeklyDuration = 7 * 24 * time.Hour
	grokWeeklyPeriod   = "USAGE_PERIOD_TYPE_WEEKLY"
)

var (
	grokAuthorizeURL    = "https://auth.x.ai/oauth2/authorize"
	grokTokenURL        = "https://auth.x.ai/oauth2/token"
	grokUsageURL        = "https://cli-chat-proxy.grok.com/v1/billing?format=credits"
	grokCallbackAddress = "127.0.0.1:56121"
)

type grokProvider struct{}

func (grokProvider) Name() string { return "grok" }

// grokCredential is stored only in Burning's auth.json.
type grokCredential struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func newGrokAuthorization(redirectURI, state string) (oauthAuthorization, error) {
	return newOAuthAuthorization(grokAuthorizeURL, grokClientID, grokScope, redirectURI, state, nil)
}

func startGrokCallback(state string) (*oauthCallbackServer, error) {
	callback, err := startOAuthCallback(grokCallbackAddress, "", grokCallbackPath, state, "Grok authentication completed. You can close this window.")
	if err != nil {
		return nil, errors.New("could not start Grok login callback")
	}
	return callback, nil
}

func (grokProvider) login(ctx context.Context, stdin *os.File, stdout io.Writer) (json.RawMessage, error) {
	state, err := randomURLValue()
	if err != nil {
		return nil, err
	}
	callback, err := startGrokCallback(state)
	if err != nil {
		return nil, err
	}
	defer callback.server.Close()
	authorization, err := newGrokAuthorization(callback.redirectURI, state)
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
	credential, err := exchangeGrokCode(ctx, code, authorization.verifier, callback.redirectURI)
	if err != nil {
		return nil, err
	}
	value, err := json.Marshal(credential)
	if err != nil {
		return nil, providerFailure("grok", categoryMalformedResponse, err)
	}
	return value, nil
}

func (grokProvider) Usage(ctx context.Context) ([]usageWindow, error) {
	credential, err := loadGrokCredential(ctx)
	if err != nil {
		return nil, err
	}
	return fetchGrokUsage(ctx, credential)
}

func loadGrokCredential(ctx context.Context) (grokCredential, error) {
	value, ok, err := credential("grok")
	if err != nil || !ok {
		return grokCredential{}, providerFailure("grok", categoryAuthentication, err)
	}
	current, err := parseGrokCredential(value)
	if err != nil {
		return grokCredential{}, err
	}
	if !current.ExpiresAt.After(time.Now().Add(grokRefreshBefore)) {
		return refreshStoredGrokCredential(ctx)
	}
	return current, nil
}

func refreshStoredGrokCredential(ctx context.Context) (grokCredential, error) {
	var refreshed grokCredential
	err := mutateCredentials(ctx, func(auth *authFile) (bool, error) {
		value, ok := auth.Credentials["grok"]
		if !ok {
			return false, providerFailure("grok", categoryAuthentication, nil)
		}
		current, err := parseGrokCredential(value)
		if err != nil {
			return false, err
		}
		if current.ExpiresAt.After(time.Now().Add(grokRefreshBefore)) {
			refreshed = current
			return false, nil
		}
		refreshed, err = refreshGrokCredential(ctx, current.RefreshToken)
		if err != nil {
			return false, err
		}
		value, err = json.Marshal(refreshed)
		if err != nil {
			return false, providerFailure("grok", categoryMalformedResponse, err)
		}
		auth.Credentials["grok"] = value
		return true, nil
	})
	if err != nil {
		return grokCredential{}, err
	}
	return refreshed, nil
}

func parseGrokCredential(value json.RawMessage) (grokCredential, error) {
	var cred grokCredential
	if err := json.Unmarshal(value, &cred); err != nil || strings.TrimSpace(cred.AccessToken) == "" || strings.TrimSpace(cred.RefreshToken) == "" || cred.ExpiresAt.IsZero() {
		return grokCredential{}, providerFailure("grok", categoryAuthentication, err)
	}
	return cred, nil
}

func exchangeGrokCode(ctx context.Context, code, verifier, redirectURI string) (grokCredential, error) {
	return requestGrokToken(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {grokClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	})
}

func refreshGrokCredential(ctx context.Context, refreshToken string) (grokCredential, error) {
	return requestGrokToken(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {grokClientID},
		"refresh_token": {refreshToken},
	})
}

func requestGrokToken(ctx context.Context, form url.Values) (grokCredential, error) {
	body, err := postOAuthToken(ctx, "grok", grokTokenURL, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return grokCredential{}, err
	}
	defer body.Close()
	return decodeGrokToken(body)
}

func decodeGrokToken(body io.Reader) (grokCredential, error) {
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    *int64 `json:"expires_in"`
	}
	decoder := json.NewDecoder(body)
	if err := decoder.Decode(&token); err != nil {
		return grokCredential{}, providerFailure("grok", categoryMalformedResponse, err)
	}
	if err := ensureJSONEOF(decoder); err != nil || strings.TrimSpace(token.AccessToken) == "" || strings.TrimSpace(token.RefreshToken) == "" || token.ExpiresIn == nil || *token.ExpiresIn <= 0 || *token.ExpiresIn > maxWindowSeconds {
		return grokCredential{}, providerFailure("grok", categoryMalformedResponse, err)
	}
	return grokCredential{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(*token.ExpiresIn) * time.Second),
	}, nil
}

func fetchGrokUsage(ctx context.Context, credential grokCredential) ([]usageWindow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, grokUsageURL, nil)
	if err != nil {
		return nil, providerFailure("grok", categoryUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	req.Header.Set("Accept", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, oauthHTTPFailure("grok", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, providerFailure("grok", classifyHTTPStatus(res.StatusCode), nil)
	}
	return decodeGrokUsage(res.Body)
}

func decodeGrokUsage(body io.Reader) ([]usageWindow, error) {
	var response struct {
		Config struct {
			CurrentPeriod struct {
				Type  string `json:"type"`
				Start string `json:"start"`
				End   string `json:"end"`
			} `json:"currentPeriod"`
			CreditUsagePercent *float64 `json:"creditUsagePercent"`
			OnDemandCap        struct {
				Val *float64 `json:"val"`
			} `json:"onDemandCap"`
			OnDemandUsed struct {
				Val *float64 `json:"val"`
			} `json:"onDemandUsed"`
			BillingPeriodEnd string `json:"billingPeriodEnd"`
		} `json:"config"`
	}
	decoder := json.NewDecoder(body)
	if err := decoder.Decode(&response); err != nil {
		return nil, providerFailure("grok", categoryMalformedResponse, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, providerFailure("grok", categoryMalformedResponse, err)
	}
	if response.Config.CurrentPeriod.Type != grokWeeklyPeriod {
		return nil, providerFailure("grok", categoryMalformedResponse, nil)
	}
	start, startOK := parseGrokTime(response.Config.CurrentPeriod.Start)
	end, endOK := parseGrokTime(response.Config.CurrentPeriod.End)
	duration := grokWeeklyDuration
	if startOK && endOK {
		duration = end.Sub(start)
		if duration <= 0 || duration > time.Duration(maxWindowSeconds)*time.Second {
			return nil, providerFailure("grok", categoryMalformedResponse, nil)
		}
	}
	if !endOK {
		end, endOK = parseGrokTime(response.Config.BillingPeriodEnd)
	}
	if !endOK {
		return nil, providerFailure("grok", categoryMalformedResponse, nil)
	}
	percent, err := grokUsagePercent(response.Config.CreditUsagePercent, response.Config.OnDemandUsed.Val, response.Config.OnDemandCap.Val)
	if err != nil {
		return nil, err
	}
	return []usageWindow{{
		Name:     "weekly",
		Duration: duration,
		Usage:    usageFromFraction(percent / 100),
		ResetsAt: end,
	}}, nil
}

func grokUsagePercent(credit, used, cap *float64) (float64, error) {
	if credit != nil {
		if *credit < 0 || *credit > 100 {
			return 0, providerFailure("grok", categoryMalformedResponse, nil)
		}
		return *credit, nil
	}
	if cap != nil && *cap > 0 {
		if used == nil || *used < 0 {
			return 0, providerFailure("grok", categoryMalformedResponse, nil)
		}
		return *used / *cap * 100, nil
	}
	return 0, nil
}

func parseGrokTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || parsed.IsZero() {
		return time.Time{}, false
	}
	return parsed, true
}
