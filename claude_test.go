package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func useClaudeEndpoints(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	oldAuthorizeURL, oldTokenURL, oldUsageURL := claudeAuthorizeURL, claudeTokenURL, claudeUsageURL
	claudeAuthorizeURL = server.URL + "/oauth/authorize"
	claudeTokenURL = server.URL + "/oauth/token"
	claudeUsageURL = server.URL + "/api/oauth/usage"
	t.Cleanup(func() {
		claudeAuthorizeURL, claudeTokenURL, claudeUsageURL = oldAuthorizeURL, oldTokenURL, oldUsageURL
		server.Close()
	})
}

func TestClaudeAuthorizationUsesPKCEAndState(t *testing.T) {
	first, err := newClaudeAuthorization("http://127.0.0.1:54321/callback", "first-state")
	if err != nil {
		t.Fatal(err)
	}
	second, err := newClaudeAuthorization("http://127.0.0.1:54321/callback", "second-state")
	if err != nil {
		t.Fatal(err)
	}
	if first.verifier == second.verifier {
		t.Fatal("PKCE verifier was reused")
	}
	parsed, err := url.Parse(first.url)
	if err != nil {
		t.Fatal(err)
	}
	want := url.Values{
		"client_id":             {claudeClientID},
		"code":                  {"true"},
		"code_challenge":        {pkceChallenge(first.verifier)},
		"code_challenge_method": {"S256"},
		"redirect_uri":          {"http://127.0.0.1:54321/callback"},
		"response_type":         {"code"},
		"scope":                 {claudeScope},
		"state":                 {"first-state"},
	}
	if got := parsed.Query().Encode(); got != want.Encode() {
		t.Errorf("authorize parameters = %s, want %s", got, want.Encode())
	}
}

func TestClaudeLoginStoresCredential(t *testing.T) {
	useConfig(t, nil)
	useInteractiveLogin(t)
	useClaudeEndpoints(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		var tokenRequest map[string]string
		if err := json.NewDecoder(r.Body).Decode(&tokenRequest); err != nil {
			t.Fatal(err)
		}
		if tokenRequest["grant_type"] != "authorization_code" || tokenRequest["client_id"] != claudeClientID || tokenRequest["code"] != "authorization-code" || tokenRequest["code_verifier"] == "" || tokenRequest["state"] == "" {
			t.Errorf("token request = %v", tokenRequest)
		}
		_, _ = io.WriteString(w, `{"access_token":"access-token","refresh_token":"refresh-token","expires_in":3600}`)
	})
	oldOpenURL := openURL
	browserURL := make(chan string, 1)
	openURL = func(url string) error {
		browserURL <- url
		return context.DeadlineExceeded
	}
	t.Cleanup(func() { openURL = oldOpenURL })

	var output, stderr bytes.Buffer
	result := make(chan int, 1)
	go func() {
		result <- runWithInput(context.Background(), []string{"login"}, interactiveInput(t, "claude\n"), &output, &stderr)
	}()
	var loginURL string
	select {
	case loginURL = <-browserURL:
	case code := <-result:
		t.Fatalf("login exit = %d before opening a Claude browser flow; stderr = %q", code, stderr.String())
	case <-time.After(time.Second):
		t.Fatal("Claude login did not open a browser flow")
	}
	parsed, err := url.Parse(loginURL)
	if err != nil {
		t.Fatal(err)
	}
	callbackURL := parsed.Query().Get("redirect_uri")
	if callbackURL != "http://localhost:53692/callback" {
		t.Fatalf("redirect_uri = %q, want Claude's registered callback", callbackURL)
	}
	response, err := http.Get(callbackURL + "?state=" + url.QueryEscape(parsed.Query().Get("state")) + "&code=authorization-code")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if code := <-result; code != 0 {
		t.Fatalf("login exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(output.String(), "Claude") || !strings.Contains(output.String(), loginURL) || !strings.Contains(output.String(), "Could not open a browser") {
		t.Errorf("login output = %q", output.String())
	}
	value, ok, err := credential("claude")
	if err != nil || !ok {
		t.Fatalf("stored credential exists = %v, err = %v", ok, err)
	}
	var stored claudeCredential
	if err := json.Unmarshal(value, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.AccessToken != "access-token" || stored.RefreshToken != "refresh-token" || stored.ExpiresAt.Before(time.Now()) {
		t.Errorf("credential = %+v", stored)
	}
	if providers, err := configuredProviders(); err != nil || len(providers) != 1 || providers[0] != "claude" {
		t.Errorf("configured providers = %v, err = %v", providers, err)
	}
}

func TestClaudeReportUsesV1(t *testing.T) {
	useConfig(t, []string{"claude"})
	credentialValue, err := json.Marshal(claudeCredential{AccessToken: "access-token", RefreshToken: "refresh-token", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err := storeCredential(context.Background(), "claude", credentialValue); err != nil {
		t.Fatal(err)
	}
	useClaudeEndpoints(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"limits":[{"kind":"session","utilization":25,"resets_at":"2026-08-12T15:00:00Z"},{"kind":"weekly_all","utilization":75,"resets_at":"2026-08-19T10:00:00Z"}]}`)
	})

	var out, errOut bytes.Buffer
	if code := run([]string{"--json"}, &out, &errOut); code != 0 {
		t.Fatalf("report exit = %d, stderr = %q", code, errOut.String())
	}
	var report jsonReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Providers) != 1 || report.Providers[0].Name != "claude" || len(report.Providers[0].Windows) != 2 {
		t.Fatalf("report = %+v", report)
	}
	if got := report.Providers[0].Windows[0]; got.Name != "session" || got.DurationSeconds != 5*60*60 || got.UsagePercent != 25 || got.RemainingAllowancePercent != 75 || got.ResetsAt == nil || got.RemainingSeconds == nil {
		t.Errorf("session = %+v", got)
	}
	if got := report.Providers[0].Windows[1]; got.Name != "weekly" || got.DurationSeconds != 7*24*60*60 || got.UsagePercent != 75 || got.RemainingAllowancePercent != 25 || got.ResetsAt == nil || got.RemainingSeconds == nil {
		t.Errorf("weekly = %+v", got)
	}
}

func TestClaudeRefreshPersistsRotatedCredential(t *testing.T) {
	useConfig(t, nil)
	if err := storeCredential(context.Background(), "claude", json.RawMessage(`{"access_token":"old-access","refresh_token":"old-refresh","expires_at":"2000-01-01T00:00:00Z"}`)); err != nil {
		t.Fatal(err)
	}
	useClaudeEndpoints(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			var tokenRequest map[string]string
			if err := json.NewDecoder(r.Body).Decode(&tokenRequest); err != nil {
				t.Fatal(err)
			}
			if tokenRequest["grant_type"] != "refresh_token" || tokenRequest["refresh_token"] != "old-refresh" || tokenRequest["client_id"] != claudeClientID {
				t.Errorf("token request = %v", tokenRequest)
			}
			_, _ = io.WriteString(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`)
		case "/api/oauth/usage":
			if got := r.Header.Get("Authorization"); got != "Bearer new-access" {
				t.Errorf("Authorization = %q", got)
			}
			_, _ = io.WriteString(w, `{"limits":[{"kind":"session","percent":0,"resets_at":"2026-08-12T15:00:00Z"},{"kind":"weekly_all","percent":0,"resets_at":"2026-08-19T10:00:00Z"}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	if _, err := (claudeProvider{}).Usage(context.Background()); err != nil {
		t.Fatal(err)
	}
	value, ok, err := credential("claude")
	if err != nil || !ok {
		t.Fatalf("credential exists = %v, err = %v", ok, err)
	}
	var stored claudeCredential
	if err := json.Unmarshal(value, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.AccessToken != "new-access" || stored.RefreshToken != "new-refresh" || stored.ExpiresAt.Before(time.Now()) {
		t.Errorf("stored credential = %+v", stored)
	}
}

func TestClaudeUsageErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"invalid credentials", http.StatusUnauthorized, "", providerErrorCode("claude", categoryAuthentication)},
		{"rate limited", http.StatusTooManyRequests, "", providerErrorCode("claude", categoryRateLimited)},
		{"unavailable", http.StatusServiceUnavailable, "", providerErrorCode("claude", categoryUnavailable)},
		{"malformed body", http.StatusOK, `{}`, providerErrorCode("claude", categoryMalformedResponse)},
		{"malformed shared window", http.StatusOK, `{"limits":[{"kind":"session","utilization":101,"resets_at":"2026-08-12T15:00:00Z"},{"kind":"weekly_all","utilization":0,"resets_at":"2026-08-19T10:00:00Z"}]}`, providerErrorCode("claude", categoryMalformedResponse)},
	} {
		t.Run(test.name, func(t *testing.T) {
			useClaudeEndpoints(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			})
			_, err := fetchClaudeUsage(context.Background(), claudeCredential{AccessToken: "access-token"})
			failure, ok := errors.AsType[providerError](err)
			if !ok || failure.code != test.want {
				t.Errorf("error = %v, want code %q", err, test.want)
			}
		})
	}
}

func TestClaudeUsageCancellation(t *testing.T) {
	useClaudeEndpoints(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := fetchClaudeUsage(ctx, claudeCredential{AccessToken: "access-token"})
	failure, ok := errors.AsType[providerError](err)
	if !ok || failure.code != providerErrorCode("claude", categoryTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want %s wrapping deadline exceeded", err, providerErrorCode("claude", categoryTimeout))
	}
}

func TestClaudeUsageNormalizesSharedWindows(t *testing.T) {
	useClaudeEndpoints(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/oauth/usage" || r.Method != http.MethodGet {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("anthropic-beta"); got != "oauth-2025-04-20" {
			t.Errorf("anthropic-beta = %q", got)
		}
		_, _ = io.WriteString(w, `{"limits":[{"kind":"session","utilization":25.5,"resets_at":"2026-08-12T15:00:00Z"},{"kind":"weekly_all","utilization":75,"resets_at":"2026-08-19T10:00:00Z"},{"kind":"weekly_scoped","utilization":99,"resets_at":"2026-08-19T10:00:00Z"}],"extra_usage":{"utilization":99}}`)
	})

	windows, err := fetchClaudeUsage(context.Background(), claudeCredential{AccessToken: "access-token"})
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 2 {
		t.Fatalf("windows = %+v", windows)
	}
	if got := windows[0]; got.Name != "session" || got.Duration != 5*time.Hour || got.Usage.percent() != 25.5 || !got.ResetsAt.Equal(time.Date(2026, 8, 12, 15, 0, 0, 0, time.UTC)) {
		t.Errorf("session = %+v", got)
	}
	if got := windows[1]; got.Name != "weekly" || got.Duration != 7*24*time.Hour || got.Usage.percent() != 75 || !got.ResetsAt.Equal(time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("weekly = %+v", got)
	}
}
