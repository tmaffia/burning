package main

import (
	"context"
	"errors"
	"fmt"
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

// provider reports usage for one subscription service. Real providers
// (auth, HTTP) arrive in later issues; tests register fakes here.
type provider interface {
	Name() string
	Usage(ctx context.Context) ([]usageWindow, error)
}

// registry maps configured provider names to implementations.
var registry = map[string]provider{}

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
				results[i].err = fmt.Errorf("timeout after %s", fetchTimeout)
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
