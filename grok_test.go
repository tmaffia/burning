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

func useGrokEndpoints(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	oldAuthorizeURL, oldTokenURL, oldUsageURL := grokAuthorizeURL, grokTokenURL, grokUsageURL
	grokAuthorizeURL = server.URL + "/oauth2/authorize"
	grokTokenURL = server.URL + "/oauth2/token"
	grokUsageURL = server.URL + "/v1/billing?format=credits"
	t.Cleanup(func() {
		grokAuthorizeURL, grokTokenURL, grokUsageURL = oldAuthorizeURL, oldTokenURL, oldUsageURL
		server.Close()
	})
}

func TestGrokUsageNormalizesCreditUsagePercent(t *testing.T) {
	windows, err := decodeGrokUsage(strings.NewReader(`{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-08-12T10:00:00Z","end":"2026-08-19T10:00:00Z"},"creditUsagePercent":46,"onDemandCap":{"val":100},"onDemandUsed":{"val":99},"billingPeriodEnd":"2026-09-01T00:00:00Z"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 1 {
		t.Fatalf("windows = %+v", windows)
	}
	got := windows[0]
	if got.Name != "weekly" || got.Duration != 7*24*time.Hour || got.Usage.percent() != 46 || !got.ResetsAt.Equal(time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("weekly = %+v", got)
	}
}

func TestGrokUsageFallsBackToOnDemandRatio(t *testing.T) {
	windows, err := decodeGrokUsage(strings.NewReader(`{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-08-12T10:00:00Z","end":"2026-08-19T10:00:00Z"},"onDemandCap":{"val":80},"onDemandUsed":{"val":20}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 1 {
		t.Fatalf("windows = %+v", windows)
	}
	if got := windows[0]; got.Name != "weekly" || got.Usage.percent() != 25 {
		t.Errorf("weekly = %+v", got)
	}
}

func TestGrokUsageTreatsMissingPercentAsZero(t *testing.T) {
	windows, err := decodeGrokUsage(strings.NewReader(`{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-08-12T10:00:00Z","end":"2026-08-19T10:00:00Z"},"onDemandCap":{"val":0}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 1 {
		t.Fatalf("windows = %+v", windows)
	}
	if got := windows[0]; got.Name != "weekly" || got.Usage.percent() != 0 || !got.ResetsAt.Equal(time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("weekly = %+v", got)
	}
}

func TestGrokUsageRejectsMissingPeriod(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"config":{}}`,
		`{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_MONTHLY","start":"2026-08-01T00:00:00Z","end":"2026-09-01T00:00:00Z"}}}`,
		`{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY"},"billingPeriodEnd":"not-a-time"}}`,
	} {
		_, err := decodeGrokUsage(strings.NewReader(body))
		failure, ok := errors.AsType[providerError](err)
		if !ok || failure.code != providerErrorCode("grok", categoryMalformedResponse) {
			t.Errorf("body %s: error = %v, want %s", body, err, providerErrorCode("grok", categoryMalformedResponse))
		}
	}
}

func TestGrokUsageFallsBackToBillingPeriodEnd(t *testing.T) {
	windows, err := decodeGrokUsage(strings.NewReader(`{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-08-12T10:00:00Z"},"creditUsagePercent":10,"billingPeriodEnd":"2026-08-20T00:00:00Z"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := windows[0]; got.Duration != 7*24*time.Hour || !got.ResetsAt.Equal(time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("weekly = %+v", got)
	}
}

func TestGrokAuthorizationUsesPKCEAndState(t *testing.T) {
	first, err := newGrokAuthorization("http://127.0.0.1:56121/callback", "first-state")
	if err != nil {
		t.Fatal(err)
	}
	second, err := newGrokAuthorization("http://127.0.0.1:56121/callback", "second-state")
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
		"client_id":             {grokClientID},
		"code_challenge":        {pkceChallenge(first.verifier)},
		"code_challenge_method": {"S256"},
		"redirect_uri":          {"http://127.0.0.1:56121/callback"},
		"response_type":         {"code"},
		"scope":                 {grokScope},
		"state":                 {"first-state"},
	}
	if got := parsed.Query().Encode(); got != want.Encode() {
		t.Errorf("authorize parameters = %s, want %s", got, want.Encode())
	}
}

func TestGrokLoginStoresCredential(t *testing.T) {
	useConfig(t, nil)
	useInteractiveLogin(t)
	oldAddress := grokCallbackAddress
	grokCallbackAddress = "127.0.0.1:0"
	t.Cleanup(func() { grokCallbackAddress = oldAddress })
	useGrokEndpoints(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth2/token" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("client_id") != grokClientID || r.Form.Get("code") != "authorization-code" || r.Form.Get("code_verifier") == "" {
			t.Errorf("token request = %v", r.Form)
		}
		_, _ = io.WriteString(w, `{"access_token":"access-token","refresh_token":"refresh-token","expires_in":21600}`)
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
		result <- runWithInput(context.Background(), []string{"login"}, interactiveInput(t, "grok\n"), &output, &stderr)
	}()
	var loginURL string
	select {
	case loginURL = <-browserURL:
	case code := <-result:
		t.Fatalf("login exit = %d before opening a Grok browser flow; stderr = %q", code, stderr.String())
	case <-time.After(time.Second):
		t.Fatal("Grok login did not open a browser flow")
	}
	parsed, err := url.Parse(loginURL)
	if err != nil {
		t.Fatal(err)
	}
	callbackURL := parsed.Query().Get("redirect_uri")
	if !strings.HasPrefix(callbackURL, "http://127.0.0.1:") || !strings.HasSuffix(callbackURL, "/callback") {
		t.Fatalf("redirect_uri = %q, want Grok loopback callback", callbackURL)
	}
	response, err := http.Get(callbackURL + "?state=" + url.QueryEscape(parsed.Query().Get("state")) + "&code=authorization-code")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if code := <-result; code != 0 {
		t.Fatalf("login exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(output.String(), "Grok") || !strings.Contains(output.String(), loginURL) || !strings.Contains(output.String(), "Could not open a browser") {
		t.Errorf("login output = %q", output.String())
	}
	value, ok, err := credential("grok")
	if err != nil || !ok {
		t.Fatalf("stored credential exists = %v, err = %v", ok, err)
	}
	var stored grokCredential
	if err := json.Unmarshal(value, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.AccessToken != "access-token" || stored.RefreshToken != "refresh-token" || stored.ExpiresAt.Before(time.Now()) {
		t.Errorf("credential = %+v", stored)
	}
	if providers, err := configuredProviders(); err != nil || len(providers) != 1 || providers[0] != "grok" {
		t.Errorf("configured providers = %v, err = %v", providers, err)
	}
}

func TestGrokReportUsesV1(t *testing.T) {
	useConfig(t, []string{"grok"})
	credentialValue, err := json.Marshal(grokCredential{AccessToken: "access-token", RefreshToken: "refresh-token", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err := storeCredential(context.Background(), "grok", credentialValue); err != nil {
		t.Fatal(err)
	}
	useGrokEndpoints(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.RawQuery; got != "format=credits" {
			t.Errorf("query = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = io.WriteString(w, `{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-08-12T10:00:00Z","end":"2026-08-19T10:00:00Z"},"creditUsagePercent":46}}`)
	})

	var out, errOut bytes.Buffer
	if code := run([]string{"--json"}, &out, &errOut); code != 0 {
		t.Fatalf("report exit = %d, stderr = %q", code, errOut.String())
	}
	var report jsonReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Providers) != 1 || report.Providers[0].Name != "grok" || len(report.Providers[0].Windows) != 1 {
		t.Fatalf("report = %+v", report)
	}
	if got := report.Providers[0].Windows[0]; got.Name != "weekly" || got.DurationSeconds != 7*24*60*60 || got.UsagePercent != 46 || got.RemainingAllowancePercent != 54 || got.ResetsAt == nil || got.RemainingSeconds == nil {
		t.Errorf("weekly = %+v", got)
	}
}

func TestGrokRefreshPersistsRotatedCredential(t *testing.T) {
	useConfig(t, nil)
	if err := storeCredential(context.Background(), "grok", json.RawMessage(`{"access_token":"old-access","refresh_token":"old-refresh","expires_at":"2000-01-01T00:00:00Z"}`)); err != nil {
		t.Fatal(err)
	}
	useGrokEndpoints(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "old-refresh" || r.Form.Get("client_id") != grokClientID {
				t.Errorf("token request = %v", r.Form)
			}
			_, _ = io.WriteString(w, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":21600}`)
		case "/v1/billing":
			if got := r.Header.Get("Authorization"); got != "Bearer new-access" {
				t.Errorf("Authorization = %q", got)
			}
			_, _ = io.WriteString(w, `{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-08-12T10:00:00Z","end":"2026-08-19T10:00:00Z"},"creditUsagePercent":0}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	if _, err := (grokProvider{}).Usage(context.Background()); err != nil {
		t.Fatal(err)
	}
	value, ok, err := credential("grok")
	if err != nil || !ok {
		t.Fatalf("credential exists = %v, err = %v", ok, err)
	}
	var stored grokCredential
	if err := json.Unmarshal(value, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.AccessToken != "new-access" || stored.RefreshToken != "new-refresh" || stored.ExpiresAt.Before(time.Now()) {
		t.Errorf("stored credential = %+v", stored)
	}
}

func TestGrokUsageErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"invalid credentials", http.StatusUnauthorized, "", providerErrorCode("grok", categoryAuthentication)},
		{"rate limited", http.StatusTooManyRequests, "", providerErrorCode("grok", categoryRateLimited)},
		{"unavailable", http.StatusServiceUnavailable, "", providerErrorCode("grok", categoryUnavailable)},
		{"malformed body", http.StatusOK, `{}`, providerErrorCode("grok", categoryMalformedResponse)},
		{"out of range percent", http.StatusOK, `{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-08-12T10:00:00Z","end":"2026-08-19T10:00:00Z"},"creditUsagePercent":101}}`, providerErrorCode("grok", categoryMalformedResponse)},
	} {
		t.Run(test.name, func(t *testing.T) {
			useGrokEndpoints(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			})
			_, err := fetchGrokUsage(context.Background(), grokCredential{AccessToken: "access-token"})
			failure, ok := errors.AsType[providerError](err)
			if !ok || failure.code != test.want {
				t.Errorf("error = %v, want code %q", err, test.want)
			}
		})
	}
}

func TestGrokUsageCancellation(t *testing.T) {
	useGrokEndpoints(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := fetchGrokUsage(ctx, grokCredential{AccessToken: "access-token"})
	failure, ok := errors.AsType[providerError](err)
	if !ok || failure.code != providerErrorCode("grok", categoryTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want %s wrapping deadline exceeded", err, providerErrorCode("grok", categoryTimeout))
	}
}
