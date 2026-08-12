package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func useOllamaServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	old := ollamaUsageURL
	ollamaUsageURL = server.URL
	t.Cleanup(func() {
		ollamaUsageURL = old
		server.Close()
	})
}

func TestOllamaUsage(t *testing.T) {
	t.Run("normalizes both usage windows", func(t *testing.T) {
		useOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/" {
				t.Errorf("request = %s %s, want GET /", r.Method, r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer api-key" {
				t.Errorf("Authorization = %q", got)
			}
			_, _ = w.Write([]byte(`{"limits":{"session":{"usage":0.25,"models":"ignored"},"weekly":{"usage":0.75}},"activity":{"cost":"ignored"}}`))
		})

		windows, err := fetchOllamaUsage(context.Background(), "api-key")
		if err != nil {
			t.Fatal(err)
		}
		if len(windows) != 2 {
			t.Fatalf("windows = %+v", windows)
		}
		if got := windows[0]; got.Name != "session" || got.Duration != 5*time.Hour || got.Usage.percent() != 25 || !got.ResetsAt.IsZero() {
			t.Errorf("session = %+v", got)
		}
		if got := windows[1]; got.Name != "weekly" || got.Duration != 7*24*time.Hour || got.Usage.percent() != 75 || !got.ResetsAt.IsZero() {
			t.Errorf("weekly = %+v", got)
		}
	})

	for _, test := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"invalid credentials", http.StatusUnauthorized, "", ollamaAuthenticationError},
		{"rate limited", http.StatusTooManyRequests, "", ollamaRateLimitedError},
		{"unavailable", http.StatusServiceUnavailable, "", ollamaUnavailableError},
		{"malformed body", http.StatusOK, "{}", ollamaMalformedResponseError},
		{"out of range", http.StatusOK, `{"limits":{"session":{"usage":1.1},"weekly":{"usage":0}}}`, ollamaMalformedResponseError},
		{"negative usage", http.StatusOK, `{"limits":{"session":{"usage":-0.1},"weekly":{"usage":0}}}`, ollamaMalformedResponseError},
	} {
		t.Run(test.name, func(t *testing.T) {
			useOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			})
			if _, err := fetchOllamaUsage(context.Background(), "api-key"); err == nil || err.Error() != test.want {
				t.Errorf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestOllamaUsageCancellation(t *testing.T) {
	useOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, err := fetchOllamaUsage(ctx, "api-key"); err == nil || err.Error() != ollamaTimeoutError || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want %s wrapping deadline exceeded", err, ollamaTimeoutError)
	}
}

func TestOllamaProviderPrefersEnvironmentCredential(t *testing.T) {
	useConfig(t, nil)
	if err := storeCredential(context.Background(), "ollama", json.RawMessage(`{"secret":"stored-key"}`)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OLLAMA_API_KEY", "environment-key")
	useOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer environment-key" {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"limits":{"session":{"usage":0},"weekly":{"usage":0}}}`))
	})
	if _, err := (ollamaProvider{}).Usage(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOllamaReportUsesV1(t *testing.T) {
	useConfig(t, []string{"ollama"})
	if err := storeCredential(context.Background(), "ollama", json.RawMessage(`{"secret":"api-key"}`)); err != nil {
		t.Fatal(err)
	}
	oldRegistry := registry
	registry = map[string]provider{"ollama": ollamaProvider{}}
	t.Cleanup(func() { registry = oldRegistry })
	useOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"limits":{"session":{"usage":0.25},"weekly":{"usage":0.75}}}`))
	})

	var out, errOut bytes.Buffer
	if code := run([]string{"--json"}, &out, &errOut); code != 0 {
		t.Fatalf("report exit = %d, stderr = %q", code, errOut.String())
	}
	var report jsonReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Providers) != 1 || len(report.Providers[0].Windows) != 2 {
		t.Fatalf("report = %+v", report)
	}
	if got := report.Providers[0].Windows[0]; got.DurationSeconds != 5*60*60 || got.UsagePercent != 25 || got.ResetsAt != nil || got.RemainingSeconds != nil {
		t.Errorf("session = %+v", got)
	}
}

func TestOllamaLoginOpensVerifiesAndStoresKey(t *testing.T) {
	useConfig(t, nil)
	useInteractiveLogin(t)
	useOllamaServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"limits":{"session":{"usage":0},"weekly":{"usage":0}}}`))
	})
	oldOpenURL := openURL
	openURL = func(url string) error {
		if url != ollamaAPIKeysURL {
			t.Errorf("URL = %q", url)
		}
		return nil
	}
	t.Cleanup(func() { openURL = oldOpenURL })

	var out, errOut bytes.Buffer
	if code := runWithInput(context.Background(), []string{"login"}, interactiveInput(t, "2\n"), &out, &errOut); code != 0 {
		t.Fatalf("login exit = %d, stderr = %q", code, errOut.String())
	}
	if got, ok, err := credential("ollama"); err != nil || !ok || string(got) != `{"secret":"secret"}` {
		t.Errorf("credential = %s, exists = %v, err = %v", got, ok, err)
	}
	if providers, err := configuredProviders(); err != nil || len(providers) != 1 || providers[0] != "ollama" {
		t.Errorf("configured providers = %v, error = %v", providers, err)
	}
	if code := runWithInput(context.Background(), []string{"login"}, interactiveInput(t, "2\n"), &out, &errOut); code != 0 {
		t.Fatalf("second login exit = %d, stderr = %q", code, errOut.String())
	}
	if providers, err := configuredProviders(); err != nil || len(providers) != 1 || providers[0] != "ollama" {
		t.Errorf("configured providers after re-login = %v, error = %v", providers, err)
	}
}
