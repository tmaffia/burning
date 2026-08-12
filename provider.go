package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// usageWindow is one allowance period, e.g. a 5-hour session or a 7-day week.
type usageWindow struct {
	Name     string        // provider-defined identifier, e.g. "session"
	Duration time.Duration // the window's full duration
	Used     float64       // fraction of allowance consumed, 0..1
	ResetsAt time.Time     // when the allowance renews; zero if unknown
}

// provider reports usage for one subscription service. Real providers
// (auth, HTTP) arrive in later issues; tests register fakes here.
type provider interface {
	Name() string
	Usage(ctx context.Context) ([]usageWindow, error)
}

// registry maps configured provider names to implementations.
var registry = map[string]provider{}

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
			windows, err := p.Usage(pctx)
			if errors.Is(err, context.DeadlineExceeded) {
				err = fmt.Errorf("timeout after %s", fetchTimeout)
			}
			results[i] = providerResult{name: p.Name(), windows: windows, err: err}
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
