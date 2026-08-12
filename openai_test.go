package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func useOpenAIEndpoints(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	oldAuthorizeURL, oldTokenURL, oldUsageURL := openAIAuthorizeURL, openAITokenURL, openAIUsageURL
	openAIAuthorizeURL = server.URL + "/oauth/authorize"
	openAITokenURL = server.URL + "/oauth/token"
	openAIUsageURL = server.URL + "/backend-api/wham/usage"
	t.Cleanup(func() {
		openAIAuthorizeURL, openAITokenURL, openAIUsageURL = oldAuthorizeURL, oldTokenURL, oldUsageURL
		server.Close()
	})
}

func testOpenAIJWT(accountID string) string {
	return testOpenAIIDToken(accountID, 0)
}

func testOpenAIIDToken(accountID string, expiresAt int64) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := `{"https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID + `"}`
	if expiresAt != 0 {
		payload += `,"exp":` + strconv.FormatInt(expiresAt, 10)
	}
	payload += `}`
	return header + "." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".signature"
}

func TestOpenAIAuthorizationUsesPKCEAndRandomState(t *testing.T) {
	first, err := newOpenAIAuthorization("http://localhost:1455/auth/callback")
	if err != nil {
		t.Fatal(err)
	}
	second, err := newOpenAIAuthorization("http://localhost:1455/auth/callback")
	if err != nil {
		t.Fatal(err)
	}
	if first.verifier == second.verifier || first.state == second.state {
		t.Fatal("PKCE verifier or state was reused")
	}
	parsed, err := url.Parse(first.url)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if len(query) != 10 {
		t.Errorf("authorize parameters = %v, want only Pi's 10 parameters", query)
	}
	if got := query.Get("response_type"); got != "code" {
		t.Errorf("response_type = %q", got)
	}
	if got := query.Get("client_id"); got != openAIClientID {
		t.Errorf("client_id = %q", got)
	}
	if got := query.Get("redirect_uri"); got != "http://localhost:1455/auth/callback" {
		t.Errorf("redirect_uri = %q", got)
	}
	if got := query.Get("scope"); got != "openid profile email offline_access" {
		t.Errorf("scope = %q", got)
	}
	if got := query.Get("state"); got != first.state {
		t.Errorf("state = %q", got)
	}
	if got := query.Get("originator"); got != "pi" {
		t.Errorf("originator = %q", got)
	}
	if got := query.Get("id_token_add_organizations"); got != "true" {
		t.Errorf("id_token_add_organizations = %q", got)
	}
	if got := query.Get("codex_cli_simplified_flow"); got != "true" {
		t.Errorf("codex_cli_simplified_flow = %q", got)
	}
	if got := query.Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q", got)
	}
	if got := query.Get("code_challenge"); got != pkceChallenge(first.verifier) {
		t.Errorf("code_challenge = %q", got)
	}
}

func TestOpenAICallbackValidatesStateAndCancellation(t *testing.T) {
	oldAddress := openAICallbackAddress
	openAICallbackAddress = "127.0.0.1:0"
	t.Cleanup(func() { openAICallbackAddress = oldAddress })

	callback, err := startOpenAICallback("expected-state")
	if err != nil {
		t.Fatal(err)
	}
	defer callback.close()

	response, err := http.Get(callback.redirectURI + "?state=wrong&code=authorization-code")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("mismatched state status = %d, want 400", response.StatusCode)
	}
	response.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := callback.wait(ctx); err == nil {
		t.Fatal("state mismatch completed the callback")
	}

	response, err = http.Get(callback.redirectURI + "?state=expected-state&error=access_denied")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("cancellation status = %d, want 400", response.StatusCode)
	}
	response.Body.Close()
	if _, err := callback.wait(context.Background()); err == nil || err.Error() != "login cancelled" {
		t.Errorf("callback error = %v, want login cancelled", err)
	}
}

func TestOpenAILoginExchangesCallbackAfterBrowserLaunchFailure(t *testing.T) {
	useConfig(t, nil)
	useInteractiveLogin(t)
	oldAddress, oldOpenURL := openAICallbackAddress, openURL
	openAICallbackAddress = "127.0.0.1:0"
	t.Cleanup(func() {
		openAICallbackAddress, openURL = oldAddress, oldOpenURL
	})
	useOpenAIEndpoints(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			t.Errorf("request path = %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := r.Form.Get("grant_type"); got != "authorization_code" {
			t.Errorf("grant_type = %q", got)
		}
		if got := r.Form.Get("code"); got != "authorization-code" {
			t.Errorf("code = %q", got)
		}
		if r.Form.Get("code_verifier") == "" {
			t.Error("missing code verifier")
		}
		_, _ = io.WriteString(w, `{"access_token":"`+testOpenAIJWT("account-id")+`","refresh_token":"rotated-refresh","expires_in":3600}`)
	})

	browserURL := make(chan string, 1)
	openURL = func(url string) error {
		browserURL <- url
		return context.DeadlineExceeded
	}
	var output, stderr bytes.Buffer
	result := make(chan int, 1)
	go func() {
		result <- runWithInput(context.Background(), []string{"login"}, interactiveInput(t, "1\n"), &output, &stderr)
	}()

	loginURL := <-browserURL
	parsed, err := url.Parse(loginURL)
	if err != nil {
		t.Fatal(err)
	}
	callbackURL := parsed.Query().Get("redirect_uri")
	if callbackURL == "" {
		t.Fatal("authorization URL omitted redirect_uri")
	}
	response, err := http.Get(callbackURL + "?state=" + url.QueryEscape(parsed.Query().Get("state")) + "&code=authorization-code")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if code := <-result; code != 0 {
		t.Fatalf("login exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(output.String(), loginURL) || !strings.Contains(output.String(), "Could not open a browser") {
		t.Errorf("login output = %q", output.String())
	}
	value, ok, err := credential("openai")
	if err != nil || !ok {
		t.Fatalf("stored credential exists = %v, err = %v", ok, err)
	}
	var stored openAICredential
	if err := json.Unmarshal(value, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.AccountID != "account-id" || stored.RefreshToken != "rotated-refresh" || stored.AccessToken == "" || stored.ExpiresAt.Before(time.Now()) {
		t.Errorf("credential = %+v", stored)
	}
	if providers, err := configuredProviders(); err != nil || len(providers) != 1 || providers[0] != "openai" {
		t.Errorf("configured providers = %v, err = %v", providers, err)
	}
}

func TestOpenAITokenUsesIDTokenClaimsAndExpiry(t *testing.T) {
	const expiresAt = 1781276043
	credential, err := decodeOpenAIToken(strings.NewReader(`{"id_token":"` + testOpenAIIDToken("account-id", expiresAt) + `","access_token":"opaque-access-token","refresh_token":"rotated-refresh"}`))
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccountID != "account-id" || credential.AccessToken != "opaque-access-token" || credential.RefreshToken != "rotated-refresh" || !credential.ExpiresAt.Equal(time.Unix(expiresAt, 0)) {
		t.Errorf("credential = %+v", credential)
	}
}

func TestOpenAIUsageNormalizesPrimaryAndWeeklyWindows(t *testing.T) {
	useOpenAIEndpoints(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/wham/usage" || r.Method != http.MethodGet {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-Id"); got != "account-id" {
			t.Errorf("ChatGPT-Account-Id = %q", got)
		}
		_, _ = io.WriteString(w, `{"rate_limit":{"primary_window":{"used_percent":25.5,"limit_window_seconds":18000,"reset_at":1781276043},"secondary_window":{"used_percent":75,"limit_window_seconds":604800,"reset_at":1781622947}},"code_review_rate_limit":{"primary_window":{"used_percent":99}},"additional_rate_limits":[{"limit_name":"ignored"}],"credits":{"balance":"ignored"}}`)
	})

	windows, err := fetchOpenAIUsage(context.Background(), openAICredential{AccessToken: "access-token", AccountID: "account-id"})
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 2 {
		t.Fatalf("windows = %+v", windows)
	}
	if got := windows[0]; got.Name != "session" || got.Duration != 5*time.Hour || got.Usage.percent() != 25.5 || !got.ResetsAt.Equal(time.Unix(1781276043, 0)) {
		t.Errorf("session = %+v", got)
	}
	if got := windows[1]; got.Name != "weekly" || got.Duration != 7*24*time.Hour || got.Usage.percent() != 75 || !got.ResetsAt.Equal(time.Unix(1781622947, 0)) {
		t.Errorf("weekly = %+v", got)
	}
}

func TestOpenAIUsageUsesPrimaryWeeklyWindowWhenSecondaryIsAbsent(t *testing.T) {
	useOpenAIEndpoints(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"rate_limit":{"primary_window":{"used_percent":25,"limit_window_seconds":604800,"reset_at":1781276043},"secondary_window":null}}`)
	})

	windows, err := fetchOpenAIUsage(context.Background(), openAICredential{AccessToken: "access-token", AccountID: "account-id"})
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 1 {
		t.Fatalf("windows = %+v", windows)
	}
	if got := windows[0]; got.Name != "weekly" || got.Duration != 7*24*time.Hour || got.Usage.percent() != 25 || !got.ResetsAt.Equal(time.Unix(1781276043, 0)) {
		t.Errorf("weekly = %+v", got)
	}
}

func TestOpenAIRefreshPersistsRotatedCredentialUnderLock(t *testing.T) {
	useConfig(t, nil)
	if err := storeCredential(context.Background(), "openai", json.RawMessage(`{"access_token":"old-access","refresh_token":"old-refresh","expires_at":"2000-01-01T00:00:00Z","account_id":"old-account"}`)); err != nil {
		t.Fatal(err)
	}
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	useOpenAIEndpoints(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if got := r.Form.Get("grant_type"); got != "refresh_token" {
				t.Errorf("grant_type = %q", got)
			}
			if got := r.Form.Get("refresh_token"); got != "old-refresh" {
				t.Errorf("refresh_token = %q", got)
			}
			close(refreshStarted)
			<-releaseRefresh
			_, _ = io.WriteString(w, `{"access_token":"`+testOpenAIJWT("new-account")+`","refresh_token":"new-refresh","expires_in":3600}`)
		case "/backend-api/wham/usage":
			if got := r.Header.Get("Authorization"); got != "Bearer "+testOpenAIJWT("new-account") {
				t.Errorf("Authorization = %q", got)
			}
			_, _ = io.WriteString(w, `{"rate_limit":{"primary_window":{"used_percent":0,"limit_window_seconds":18000,"reset_at":1781276043},"secondary_window":{"used_percent":0,"limit_window_seconds":604800,"reset_at":1781622947}}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	usageDone := make(chan error, 1)
	go func() {
		_, err := (openaiProvider{}).Usage(context.Background())
		usageDone <- err
	}()
	<-refreshStarted
	storeDone := make(chan error, 1)
	go func() {
		storeDone <- storeCredential(context.Background(), "ollama", json.RawMessage(`{"secret":"keep"}`))
	}()
	select {
	case err := <-storeDone:
		t.Fatalf("credential mutation completed while refresh held lock: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	close(releaseRefresh)
	if err := <-usageDone; err != nil {
		t.Fatal(err)
	}
	if err := <-storeDone; err != nil {
		t.Fatal(err)
	}
	value, ok, err := credential("openai")
	if err != nil || !ok {
		t.Fatalf("credential exists = %v, err = %v", ok, err)
	}
	var cred openAICredential
	if err := json.Unmarshal(value, &cred); err != nil {
		t.Fatal(err)
	}
	if cred.RefreshToken != "new-refresh" || cred.AccountID != "new-account" {
		t.Errorf("stored credential = %+v", cred)
	}
}

func TestOpenAIErrorsRedactTokens(t *testing.T) {
	useOpenAIEndpoints(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "access-token-and-refresh-token")
	})
	_, exchangeErr := exchangeOpenAICode(context.Background(), "authorization-code", "verifier", "http://localhost/callback")
	_, usageErr := fetchOpenAIUsage(context.Background(), openAICredential{AccessToken: "access-token", AccountID: "account-id"})
	_, jwtErr := decodeOpenAIToken(strings.NewReader(`{"access_token":"jwt-secret","refresh_token":"refresh-secret","expires_in":3600}`))
	for _, err := range []error{exchangeErr, usageErr, jwtErr} {
		if err == nil || strings.Contains(err.Error(), "token") || strings.Contains(err.Error(), "authorization-code") {
			t.Errorf("error = %v, leaked a secret", err)
		}
	}
}
