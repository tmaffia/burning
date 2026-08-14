package main

import (
	"context"
	"encoding/base64"
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
	openAIClientID      = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAIScope         = "openid profile email offline_access"
	openAICallbackPath  = "/auth/callback"
	openAIRefreshBefore = time.Minute
	maxWindowSeconds    = int64((1<<63 - 1) / int64(time.Second))
	// openAILongWindowSeconds is the minimum window length OpenAI's usage API
	// treats as the long-running "weekly" window (a day, not literally a
	// week); anything shorter is the "session" window.
	openAILongWindowSeconds = int64(24 * time.Hour / time.Second)
)

var (
	openAIAuthorizeURL    = "https://auth.openai.com/oauth/authorize"
	openAITokenURL        = "https://auth.openai.com/oauth/token"
	openAIUsageURL        = "https://chatgpt.com/backend-api/wham/usage"
	openAICallbackAddress = "127.0.0.1:1455"
)

type openaiProvider struct{}

func (openaiProvider) Name() string { return "openai" }

// openAICredential is stored only in Burning's auth.json.
type openAICredential struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	AccountID    string    `json:"account_id"`
}

type openAIAuthorization = oauthAuthorization
type openAICallbackServer = oauthCallbackServer

func newOpenAIAuthorization(redirectURI, state string) (openAIAuthorization, error) {
	return newOAuthAuthorization(openAIAuthorizeURL, openAIClientID, openAIScope, redirectURI, state, url.Values{
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {"burning"},
	})
}

func startOpenAICallback(state string) (*openAICallbackServer, error) {
	publicAddress := ""
	if openAICallbackAddress == "127.0.0.1:1455" {
		publicAddress = "localhost:1455"
	}
	callback, err := startOAuthCallback(openAICallbackAddress, publicAddress, openAICallbackPath, state, "OpenAI authentication completed. You can close this window.")
	if err != nil {
		return nil, errors.New("could not start OpenAI login callback")
	}
	return callback, nil
}

// login runs the browser OAuth flow instead of asking the user to paste a
// credential. The URL stays visible if the automatic browser launch fails.
func (openaiProvider) login(ctx context.Context, stdin *os.File, stdout io.Writer) (json.RawMessage, error) {
	state, err := randomURLValue()
	if err != nil {
		return nil, err
	}
	callback, err := startOpenAICallback(state)
	if err != nil {
		return nil, err
	}
	defer callback.server.Close()
	authorization, err := newOpenAIAuthorization(callback.redirectURI, state)
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
	credential, err := exchangeOpenAICode(ctx, code, authorization.verifier, callback.redirectURI)
	if err != nil {
		return nil, err
	}
	value, err := json.Marshal(credential)
	if err != nil {
		return nil, providerFailure("openai", categoryMalformedResponse, err)
	}
	return value, nil
}

func (openaiProvider) Usage(ctx context.Context) ([]usageWindow, error) {
	credential, err := loadOpenAICredential(ctx)
	if err != nil {
		return nil, err
	}
	return fetchOpenAIUsage(ctx, credential)
}

func loadOpenAICredential(ctx context.Context) (openAICredential, error) {
	value, ok, err := credential("openai")
	if err != nil || !ok {
		return openAICredential{}, providerFailure("openai", categoryAuthentication, err)
	}
	current, err := parseOpenAICredential(value)
	if err != nil {
		return openAICredential{}, err
	}
	if !current.ExpiresAt.After(time.Now().Add(openAIRefreshBefore)) {
		return refreshStoredOpenAICredential(ctx)
	}
	return current, nil
}

func refreshStoredOpenAICredential(ctx context.Context) (openAICredential, error) {
	var refreshed openAICredential
	err := mutateCredentials(ctx, func(auth *authFile) (bool, error) {
		value, ok := auth.Credentials["openai"]
		if !ok {
			return false, providerFailure("openai", categoryAuthentication, nil)
		}
		current, err := parseOpenAICredential(value)
		if err != nil {
			return false, err
		}
		if current.ExpiresAt.After(time.Now().Add(openAIRefreshBefore)) {
			refreshed = current
			return false, nil
		}
		refreshed, err = refreshOpenAICredential(ctx, current.RefreshToken)
		if err != nil {
			return false, err
		}
		value, err = json.Marshal(refreshed)
		if err != nil {
			return false, providerFailure("openai", categoryMalformedResponse, err)
		}
		auth.Credentials["openai"] = value
		return true, nil
	})
	if err != nil {
		return openAICredential{}, err
	}
	return refreshed, nil
}

func parseOpenAICredential(value json.RawMessage) (openAICredential, error) {
	var cred openAICredential
	if err := json.Unmarshal(value, &cred); err != nil || strings.TrimSpace(cred.AccessToken) == "" || strings.TrimSpace(cred.RefreshToken) == "" || cred.ExpiresAt.IsZero() || strings.TrimSpace(cred.AccountID) == "" {
		return openAICredential{}, providerFailure("openai", categoryAuthentication, err)
	}
	return cred, nil
}

func exchangeOpenAICode(ctx context.Context, code, verifier, redirectURI string) (openAICredential, error) {
	return requestOpenAIToken(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {openAIClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
	})
}

func refreshOpenAICredential(ctx context.Context, refreshToken string) (openAICredential, error) {
	return requestOpenAIToken(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {openAIClientID},
		"refresh_token": {refreshToken},
	})
}

func requestOpenAIToken(ctx context.Context, form url.Values) (openAICredential, error) {
	body, err := postOAuthToken(ctx, "openai", openAITokenURL, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		return openAICredential{}, err
	}
	defer body.Close()
	return decodeOpenAIToken(body)
}

func decodeOpenAIToken(body io.Reader) (openAICredential, error) {
	var token struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    *int64 `json:"expires_in"`
	}
	decoder := json.NewDecoder(body)
	if err := decoder.Decode(&token); err != nil {
		return openAICredential{}, providerFailure("openai", categoryMalformedResponse, err)
	}
	if err := ensureJSONEOF(decoder); err != nil || strings.TrimSpace(token.AccessToken) == "" || strings.TrimSpace(token.RefreshToken) == "" {
		return openAICredential{}, providerFailure("openai", categoryMalformedResponse, err)
	}
	claimsToken := token.IDToken
	if claimsToken == "" {
		claimsToken = token.AccessToken
	}
	claims, err := parseOpenAIJWT(claimsToken)
	if err != nil || strings.TrimSpace(claims.AccountID) == "" {
		return openAICredential{}, providerFailure("openai", categoryMalformedResponse, err)
	}
	expiresAt := time.Time{}
	if token.ExpiresIn != nil && *token.ExpiresIn > 0 && *token.ExpiresIn <= maxWindowSeconds {
		expiresAt = time.Now().Add(time.Duration(*token.ExpiresIn) * time.Second)
	} else if claims.ExpiresAt > 0 {
		expiresAt = time.Unix(claims.ExpiresAt, 0)
	}
	if expiresAt.IsZero() {
		return openAICredential{}, providerFailure("openai", categoryMalformedResponse, nil)
	}
	return openAICredential{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    expiresAt,
		AccountID:    claims.AccountID,
	}, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra struct{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("invalid JSON response")
	}
	return nil
}

type openAIJWTClaims struct {
	AccountID string
	ExpiresAt int64
}

func parseOpenAIJWT(token string) (openAIJWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return openAIJWTClaims{}, errors.New("invalid token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return openAIJWTClaims{}, errors.New("invalid token")
	}
	var claims struct {
		ExpiresAt int64 `json:"exp"`
		Auth      struct {
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return openAIJWTClaims{}, errors.New("invalid token")
	}
	return openAIJWTClaims{AccountID: claims.Auth.AccountID, ExpiresAt: claims.ExpiresAt}, nil
}

type openAIUsageResponseWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds *int64   `json:"limit_window_seconds"`
	ResetAt            *int64   `json:"reset_at"`
}

func fetchOpenAIUsage(ctx context.Context, credential openAICredential) ([]usageWindow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openAIUsageURL, nil)
	if err != nil {
		return nil, providerFailure("openai", categoryUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	req.Header.Set("ChatGPT-Account-Id", credential.AccountID)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "burning")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, openAIHTTPFailure(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, providerFailure("openai", classifyHTTPStatus(res.StatusCode), nil)
	}

	var response struct {
		RateLimit struct {
			Primary   *openAIUsageResponseWindow `json:"primary_window"`
			Secondary *openAIUsageResponseWindow `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	decoder := json.NewDecoder(res.Body)
	if err := decoder.Decode(&response); err != nil || ensureJSONEOF(decoder) != nil {
		return nil, providerFailure("openai", categoryMalformedResponse, err)
	}
	windows := make([]usageWindow, 0, 2)
	for _, window := range []*openAIUsageResponseWindow{response.RateLimit.Primary, response.RateLimit.Secondary} {
		if window == nil {
			continue
		}
		normalized, err := normalizeOpenAIWindow(openAIWindowName(window), window)
		if err != nil {
			return nil, err
		}
		windows = append(windows, normalized)
	}
	if len(windows) == 0 {
		return nil, providerFailure("openai", categoryMalformedResponse, nil)
	}
	return windows, nil
}

func openAIWindowName(window *openAIUsageResponseWindow) string {
	if window.LimitWindowSeconds != nil && *window.LimitWindowSeconds >= openAILongWindowSeconds {
		return "weekly"
	}
	return "session"
}

func normalizeOpenAIWindow(name string, window *openAIUsageResponseWindow) (usageWindow, error) {
	if window == nil || window.UsedPercent == nil || *window.UsedPercent < 0 || *window.UsedPercent > 100 || window.LimitWindowSeconds == nil || *window.LimitWindowSeconds <= 0 || *window.LimitWindowSeconds > maxWindowSeconds || window.ResetAt == nil || *window.ResetAt <= 0 {
		return usageWindow{}, providerFailure("openai", categoryMalformedResponse, nil)
	}
	return usageWindow{
		Name:     name,
		Duration: time.Duration(*window.LimitWindowSeconds) * time.Second,
		Usage:    usageFromFraction(*window.UsedPercent / 100),
		ResetsAt: time.Unix(*window.ResetAt, 0),
	}, nil
}

func openAIHTTPFailure(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return providerFailure("openai", categoryTimeout, err)
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	return providerFailure("openai", categoryUnavailable, err)
}
