package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// usage is the normalized fraction of an allowance consumed.
type usage struct {
	fraction float64
}

func usageFromFraction(fraction float64) usage {
	return usage{fraction: min(max(fraction, 0), 1)}
}

func (u usage) percent() float64                   { return u.fraction * 100 }
func (u usage) remainingAllowancePercent() float64 { return (1 - u.fraction) * 100 }

// usageWindow represents one provider-defined Usage Window, e.g. a 5-hour session.
type usageWindow struct {
	Name     string
	Duration time.Duration
	Usage    usage
	ResetsAt time.Time // zero if unknown
}

// provider reports Usage for one subscription service and owns its complete
// login flow.
type provider interface {
	Name() string
	Usage(ctx context.Context) ([]usageWindow, error)
	// login runs the provider's own credential flow — a browser OAuth
	// exchange or a pasted-key prompt — and returns the JSON credential to
	// store.
	login(ctx context.Context, stdin *os.File, stdout io.Writer) (json.RawMessage, error)
}

// providerError is a Provider failure carrying a stable code alongside its
// human-readable message, wrapping the underlying cause when there is one.
type providerError struct {
	code    string
	message string
	cause   error
}

func (e providerError) Error() string { return e.message }
func (e providerError) Unwrap() error { return e.cause }

// providerErrorCategory is a stable failure category shared by every
// provider; a provider's full error code is name + "_" + category.
type providerErrorCategory string

const (
	categoryAuthentication    providerErrorCategory = "authentication"
	categoryTimeout           providerErrorCategory = "timeout"
	categoryRateLimited       providerErrorCategory = "rate_limited"
	categoryUnavailable       providerErrorCategory = "unavailable"
	categoryMalformedResponse providerErrorCategory = "malformed_response"
)

// providerErrorCode derives a provider's stable error code, e.g.
// "ollama_authentication".
func providerErrorCode(provider string, category providerErrorCategory) string {
	return provider + "_" + string(category)
}

// providerErrorMessage returns the human-readable message for a shared
// category, matching the prose contract every provider's errors use.
func providerErrorMessage(category providerErrorCategory) string {
	switch category {
	case categoryAuthentication:
		return "authentication failed"
	case categoryTimeout:
		return fmt.Sprintf("timeout after %s", fetchTimeout)
	case categoryRateLimited:
		return "rate limited"
	case categoryUnavailable:
		return "unavailable"
	case categoryMalformedResponse:
		return "malformed response"
	default:
		return string(category)
	}
}

func providerFailure(provider string, category providerErrorCategory, cause error) error {
	return providerError{code: providerErrorCode(provider, category), message: providerErrorMessage(category), cause: cause}
}

// classifyHTTPStatus maps a non-2xx response status to the shared error
// category used by every provider's HTTP calls.
func classifyHTTPStatus(status int) providerErrorCategory {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return categoryAuthentication
	case http.StatusTooManyRequests:
		return categoryRateLimited
	default:
		return categoryUnavailable
	}
}

// registry maps configured provider names to implementations.
var registry = map[string]provider{
	"openai": openaiProvider{},
	"ollama": ollamaProvider{},
	"claude": claudeProvider{},
}

// knownProvider is a Provider Burning ships support for: the name used in
// config.json, auth.json and registry, plus the label shown to humans.
type knownProvider struct {
	name  string
	label string
}

// knownProviders is the single list of supported Providers, in menu order.
// Adding a Provider means adding it here and registering its implementation
// under the same name.
var knownProviders = []knownProvider{
	{name: "openai", label: "OpenAI Codex"},
	{name: "ollama", label: "Ollama Cloud"},
	{name: "claude", label: "Claude"},
}

// fetchTimeout bounds each provider call; providers run concurrently.
var fetchTimeout = 10 * time.Second

type providerResult struct {
	name    string
	windows []usageWindow
	err     error
}

// fetchAll queries every configured provider concurrently, each under its own
// fetchTimeout, keeping config order in the results. Unknown names become
// structured errors.
func fetchAll(ctx context.Context, names []string) []providerResult {
	results := make([]providerResult, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		p, ok := registry[name]
		if !ok {
			results[i] = providerResult{name: name, err: fmt.Errorf("unknown provider %q", name)}
			continue
		}
		wg.Add(1)
		go func(i int, p provider) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, fetchTimeout)
			defer cancel()
			name := p.Name()
			// ponytail: a provider ignoring cancellation may leak this goroutine;
			// isolate providers in processes if that happens in practice.
			done := make(chan providerResult, 1)
			go func() {
				windows, err := p.Usage(pctx)
				done <- providerResult{name: name, windows: windows, err: err}
			}()
			select {
			case results[i] = <-done:
			case <-pctx.Done():
				results[i] = providerResult{name: name, err: pctx.Err()}
			}
			if errors.Is(results[i].err, context.DeadlineExceeded) {
				if _, ok := results[i].err.(providerError); !ok {
					results[i].err = fmt.Errorf("timeout after %s", fetchTimeout)
				}
			}
		}(i, p)
	}
	wg.Wait()
	return results
}

func exitCode(results []providerResult) int {
	for _, r := range results {
		if r.err != nil {
			return 1
		}
	}
	return 0
}
