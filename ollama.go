package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"time"
)

const (
	ollamaAPIKeysURL = "https://ollama.com/settings/keys"

	ollamaAuthenticationError    = "ollama_authentication"
	ollamaTimeoutError           = "ollama_timeout"
	ollamaRateLimitedError       = "ollama_rate_limited"
	ollamaUnavailableError       = "ollama_unavailable"
	ollamaMalformedResponseError = "ollama_malformed_response"
)

var ollamaUsageURL = "https://ollama.com/api/usage"

type ollamaProvider struct{}

func (ollamaProvider) Name() string { return "ollama" }

func (ollamaProvider) Usage(ctx context.Context) ([]usageWindow, error) {
	key, err := ollamaAPIKey()
	if err != nil {
		return nil, err
	}
	return fetchOllamaUsage(ctx, key)
}

func ollamaAPIKey() (string, error) {
	if key := os.Getenv("OLLAMA_API_KEY"); key != "" {
		return key, nil
	}
	value, ok, err := credential("ollama")
	if err != nil || !ok {
		return "", ollamaFailure(ollamaAuthenticationError, err)
	}
	return ollamaKey(value)
}

func ollamaKey(value json.RawMessage) (string, error) {
	var credential struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(value, &credential); err != nil || credential.Secret == "" {
		return "", ollamaFailure(ollamaAuthenticationError, err)
	}
	return credential.Secret, nil
}

func fetchOllamaUsage(ctx context.Context, key string) ([]usageWindow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ollamaUsageURL, nil)
	if err != nil {
		return nil, ollamaFailure(ollamaUnavailableError, err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ollamaFailure(ollamaTimeoutError, err)
		}
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, ollamaFailure(ollamaUnavailableError, err)
	}
	defer res.Body.Close()

	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ollamaFailure(ollamaAuthenticationError, nil)
	case http.StatusTooManyRequests:
		return nil, ollamaFailure(ollamaRateLimitedError, nil)
	default:
		return nil, ollamaFailure(ollamaUnavailableError, nil)
	}

	var response struct {
		Limits struct {
			Session struct {
				Usage *float64 `json:"usage"`
			} `json:"session"`
			Weekly struct {
				Usage *float64 `json:"usage"`
			} `json:"weekly"`
		} `json:"limits"`
	}
	decoder := json.NewDecoder(res.Body)
	if err := decoder.Decode(&response); err != nil {
		return nil, ollamaFailure(ollamaMalformedResponseError, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, ollamaFailure(ollamaMalformedResponseError, err)
	}
	if !validOllamaUsage(response.Limits.Session.Usage) || !validOllamaUsage(response.Limits.Weekly.Usage) {
		return nil, ollamaFailure(ollamaMalformedResponseError, nil)
	}
	return []usageWindow{
		{Name: "session", Duration: 5 * time.Hour, Usage: usageFromFraction(*response.Limits.Session.Usage)},
		{Name: "weekly", Duration: 7 * 24 * time.Hour, Usage: usageFromFraction(*response.Limits.Weekly.Usage)},
	}, nil
}

func validOllamaUsage(value *float64) bool {
	return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0 && *value <= 1
}

type ollamaError struct {
	code  string
	cause error
}

func (e ollamaError) Error() string { return e.code }
func (e ollamaError) Unwrap() error { return e.cause }

func ollamaFailure(code string, cause error) error {
	return ollamaError{code: code, cause: cause}
}
