package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	openAIClientID       = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAIScope          = "openid profile email offline_access"
	openAICallbackPath   = "/auth/callback"
	openAIAuthClaim      = "https://api.openai.com/auth"
	openAIRefreshBefore  = time.Minute
	maxWindowSeconds     = int64((1<<63 - 1) / int64(time.Second))
	openAIAuthError      = "openai_authentication"
	openAITimeoutError   = "openai_timeout"
	openAIRateLimitError = "openai_rate_limited"
	openAIUnavailable    = "openai_unavailable"
	openAIMalformed      = "openai_malformed_response"
)

var (
	openAIAuthorizeURL    = "https://auth.openai.com/oauth/authorize"
	openAITokenURL        = "https://auth.openai.com/oauth/token"
	openAIUsageURL        = "https://chatgpt.com/backend-api/wham/usage"
	openAICallbackAddress = "127.0.0.1:1455"
	openAINow             = time.Now
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

type openAIAuthorization struct {
	verifier string
	state    string
	url      string
}

func newOpenAIAuthorization(redirectURI string) (openAIAuthorization, error) {
	return newOpenAIAuthorizationWithState(redirectURI, "")
}

func newOpenAIAuthorizationWithState(redirectURI, state string) (openAIAuthorization, error) {
	verifier, err := randomURLValue()
	if err != nil {
		return openAIAuthorization{}, err
	}
	if state == "" {
		state, err = randomURLValue()
		if err != nil {
			return openAIAuthorization{}, err
		}
	}
	endpoint, err := url.Parse(openAIAuthorizeURL)
	if err != nil {
		return openAIAuthorization{}, errors.New("invalid OpenAI authorization URL")
	}
	query := endpoint.Query()
	query.Set("response_type", "code")
	query.Set("client_id", openAIClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", openAIScope)
	query.Set("code_challenge", pkceChallenge(verifier))
	query.Set("code_challenge_method", "S256")
	query.Set("state", state)
	query.Set("id_token_add_organizations", "true")
	query.Set("codex_cli_simplified_flow", "true")
	query.Set("originator", "pi")
	endpoint.RawQuery = query.Encode()
	return openAIAuthorization{verifier: verifier, state: state, url: endpoint.String()}, nil
}

func randomURLValue() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", errors.New("could not generate secure login values")
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func pkceChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

type openAICallbackServer struct {
	server      *http.Server
	redirectURI string
	state       string
	result      chan openAICallbackResult
}

type openAICallbackResult struct {
	code string
	err  error
}

func startOpenAICallback(state string) (*openAICallbackServer, error) {
	listener, err := net.Listen("tcp", openAICallbackAddress)
	if err != nil {
		return nil, errors.New("could not start OpenAI login callback")
	}
	callback := &openAICallbackServer{state: state, result: make(chan openAICallbackResult, 1)}
	callback.redirectURI = callbackURI(listener.Addr().String())
	callback.server = &http.Server{Handler: http.HandlerFunc(callback.handle)}
	go func() { _ = callback.server.Serve(listener) }()
	return callback, nil
}

func callbackURI(address string) string {
	if openAICallbackAddress == "127.0.0.1:1455" {
		return "http://localhost:1455" + openAICallbackPath
	}
	return "http://" + address + openAICallbackPath
}

func (callback *openAICallbackServer) close() {
	_ = callback.server.Close()
}

func (callback *openAICallbackServer) wait(ctx context.Context) (string, error) {
	select {
	case result := <-callback.result:
		return result.code, result.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (callback *openAICallbackServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != openAICallbackPath {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	state, ok := singleCallbackParameter(r.URL.Query(), "state")
	if !ok || subtle.ConstantTimeCompare([]byte(state), []byte(callback.state)) != 1 {
		callbackError(w, "State mismatch.")
		return
	}
	if _, canceled := r.URL.Query()["error"]; canceled {
		if _, ok := singleCallbackParameter(r.URL.Query(), "error"); !ok {
			callbackError(w, "Invalid callback.")
			return
		}
		if _, hasCode := r.URL.Query()["code"]; hasCode {
			callbackError(w, "Invalid callback.")
			return
		}
		callbackError(w, "Login cancelled.")
		callback.deliver(openAICallbackResult{err: errors.New("login cancelled")})
		return
	}
	code, ok := singleCallbackParameter(r.URL.Query(), "code")
	if !ok {
		callbackError(w, "Missing authorization code.")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, "OpenAI authentication completed. You can close this window.")
	callback.deliver(openAICallbackResult{code: code})
}

func singleCallbackParameter(values url.Values, name string) (string, bool) {
	value := values[name]
	if len(value) != 1 || value[0] == "" {
		return "", false
	}
	return value[0], true
}

func callbackError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = io.WriteString(w, message)
}

func (callback *openAICallbackServer) deliver(result openAICallbackResult) {
	select {
	case callback.result <- result:
	default:
	}
}

// login runs the browser OAuth flow instead of asking the user to paste a
// credential. The URL stays visible if the automatic browser launch fails.
func (openaiProvider) login(ctx context.Context, stdout io.Writer) (json.RawMessage, error) {
	state, err := randomURLValue()
	if err != nil {
		return nil, err
	}
	callback, err := startOpenAICallback(state)
	if err != nil {
		return nil, err
	}
	defer callback.close()
	authorization, err := newOpenAIAuthorizationWithState(callback.redirectURI, state)
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
		return nil, openAIFailure(openAIMalformed, err)
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
		return openAICredential{}, openAIFailure(openAIAuthError, err)
	}
	current, err := parseOpenAICredential(value)
	if err != nil {
		return openAICredential{}, err
	}
	if !current.ExpiresAt.After(openAINow().Add(openAIRefreshBefore)) {
		return refreshStoredOpenAICredential(ctx)
	}
	return current, nil
}

func refreshStoredOpenAICredential(ctx context.Context) (openAICredential, error) {
	var refreshed openAICredential
	err := mutateCredentialsWith(ctx, func(auth *authFile) (bool, error) {
		value, ok := auth.Credentials["openai"]
		if !ok {
			return false, openAIFailure(openAIAuthError, nil)
		}
		current, err := parseOpenAICredential(value)
		if err != nil {
			return false, err
		}
		if current.ExpiresAt.After(openAINow().Add(openAIRefreshBefore)) {
			refreshed = current
			return false, nil
		}
		refreshed, err = refreshOpenAICredential(ctx, current.RefreshToken)
		if err != nil {
			return false, err
		}
		value, err = json.Marshal(refreshed)
		if err != nil {
			return false, openAIFailure(openAIMalformed, err)
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
	var credential openAICredential
	if err := json.Unmarshal(value, &credential); err != nil || strings.TrimSpace(credential.AccessToken) == "" || strings.TrimSpace(credential.RefreshToken) == "" || credential.ExpiresAt.IsZero() || strings.TrimSpace(credential.AccountID) == "" {
		return openAICredential{}, openAIFailure(openAIAuthError, err)
	}
	return credential, nil
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAITokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return openAICredential{}, openAIFailure(openAIUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return openAICredential{}, openAIHTTPFailure(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		switch res.StatusCode {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
			return openAICredential{}, openAIFailure(openAIAuthError, nil)
		case http.StatusTooManyRequests:
			return openAICredential{}, openAIFailure(openAIRateLimitError, nil)
		default:
			return openAICredential{}, openAIFailure(openAIUnavailable, nil)
		}
	}
	return decodeOpenAIToken(res.Body)
}

func decodeOpenAIToken(body io.Reader) (openAICredential, error) {
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	decoder := json.NewDecoder(body)
	if err := decoder.Decode(&token); err != nil {
		return openAICredential{}, openAIFailure(openAIMalformed, err)
	}
	if err := ensureJSONEOF(decoder); err != nil || strings.TrimSpace(token.AccessToken) == "" || strings.TrimSpace(token.RefreshToken) == "" || token.ExpiresIn <= 0 || token.ExpiresIn > maxWindowSeconds {
		return openAICredential{}, openAIFailure(openAIMalformed, err)
	}
	accountID, err := accountIDFromJWT(token.AccessToken)
	if err != nil {
		return openAICredential{}, openAIFailure(openAIMalformed, err)
	}
	return openAICredential{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    openAINow().Add(time.Duration(token.ExpiresIn) * time.Second),
		AccountID:    accountID,
	}, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra struct{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("invalid JSON response")
	}
	return nil
}

func accountIDFromJWT(accessToken string) (string, error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return "", errors.New("invalid access token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("invalid access token")
	}
	var claims map[string]struct {
		AccountID string `json:"chatgpt_account_id"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || strings.TrimSpace(claims[openAIAuthClaim].AccountID) == "" {
		return "", errors.New("invalid access token")
	}
	return claims[openAIAuthClaim].AccountID, nil
}

type openAIUsageResponseWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds *int64   `json:"limit_window_seconds"`
	ResetAt            *int64   `json:"reset_at"`
}

func fetchOpenAIUsage(ctx context.Context, credential openAICredential) ([]usageWindow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openAIUsageURL, nil)
	if err != nil {
		return nil, openAIFailure(openAIUnavailable, err)
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
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, openAIFailure(openAIAuthError, nil)
	case http.StatusTooManyRequests:
		return nil, openAIFailure(openAIRateLimitError, nil)
	default:
		return nil, openAIFailure(openAIUnavailable, nil)
	}

	var response struct {
		RateLimit struct {
			Primary   *openAIUsageResponseWindow `json:"primary_window"`
			Secondary *openAIUsageResponseWindow `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	decoder := json.NewDecoder(res.Body)
	if err := decoder.Decode(&response); err != nil || ensureJSONEOF(decoder) != nil {
		return nil, openAIFailure(openAIMalformed, err)
	}
	primary, err := normalizeOpenAIWindow("session", response.RateLimit.Primary)
	if err != nil {
		return nil, err
	}
	secondary, err := normalizeOpenAIWindow("weekly", response.RateLimit.Secondary)
	if err != nil {
		return nil, err
	}
	return []usageWindow{primary, secondary}, nil
}

func normalizeOpenAIWindow(name string, window *openAIUsageResponseWindow) (usageWindow, error) {
	if window == nil || window.UsedPercent == nil || *window.UsedPercent < 0 || *window.UsedPercent > 100 || window.LimitWindowSeconds == nil || *window.LimitWindowSeconds <= 0 || *window.LimitWindowSeconds > maxWindowSeconds || window.ResetAt == nil || *window.ResetAt <= 0 {
		return usageWindow{}, openAIFailure(openAIMalformed, nil)
	}
	return usageWindow{
		Name:     name,
		Duration: time.Duration(*window.LimitWindowSeconds) * time.Second,
		Usage:    usageFromFraction(*window.UsedPercent / 100),
		ResetsAt: time.Unix(*window.ResetAt, 0),
	}, nil
}

type openAIError struct {
	code    string
	message string
	cause   error
}

func (e openAIError) Error() string { return e.message }
func (e openAIError) Unwrap() error { return e.cause }

func openAIHTTPFailure(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return openAIFailure(openAITimeoutError, err)
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	return openAIFailure(openAIUnavailable, err)
}

func openAIFailure(code string, cause error) error {
	message := code
	switch code {
	case openAIAuthError:
		message = "authentication failed"
	case openAITimeoutError:
		message = fmt.Sprintf("timeout after %s", fetchTimeout)
	case openAIRateLimitError:
		message = "rate limited"
	case openAIUnavailable:
		message = "unavailable"
	case openAIMalformed:
		message = "malformed response"
	}
	return openAIError{code: code, message: message, cause: cause}
}
