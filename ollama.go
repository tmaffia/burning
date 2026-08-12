package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	secret, err := ollamaCredential()
	if err != nil {
		return nil, err
	}
	return fetchOllamaUsage(ctx, secret)
}

// prepareLogin opens Ollama Cloud's API-key page before the credential prompt.
func (ollamaProvider) prepareLogin(stdout io.Writer) error {
	fmt.Fprintf(stdout, "Opening %s\n", ollamaAPIKeysURL)
	if err := openURL(ollamaAPIKeysURL); err != nil {
		return errors.New("could not open Ollama Cloud API-key page")
	}
	return nil
}

// verifyLogin checks a newly entered credential against the usage endpoint
// before it's stored.
func (ollamaProvider) verifyLogin(ctx context.Context, value json.RawMessage) error {
	secret, err := ollamaCredentialSecret(value)
	if err != nil {
		return err
	}
	_, err = fetchOllamaUsage(ctx, secret)
	return err
}

func ollamaCredential() (string, error) {
	if secret := os.Getenv("OLLAMA_API_KEY"); secret != "" {
		return secret, nil
	}
	value, ok, err := credential("ollama")
	if err != nil || !ok {
		return "", ollamaFailure(ollamaAuthenticationError, err)
	}
	return ollamaCredentialSecret(value)
}

func ollamaCredentialSecret(value json.RawMessage) (string, error) {
	var credential struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(value, &credential); err != nil || credential.Secret == "" {
		return "", ollamaFailure(ollamaAuthenticationError, err)
	}
	return credential.Secret, nil
}

func fetchOllamaUsage(ctx context.Context, secret string) ([]usageWindow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ollamaUsageURL, nil)
	if err != nil {
		return nil, ollamaFailure(ollamaUnavailableError, err)
	}
	req.Header.Set("Authorization", "Bearer "+secret)
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

	type ollamaLimit struct {
		Usage *float64 `json:"usage"`
	}
	var response struct {
		Limits struct {
			Session ollamaLimit `json:"session"`
			Weekly  ollamaLimit `json:"weekly"`
		} `json:"limits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
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
	return value != nil && *value >= 0 && *value <= 1
}

type ollamaError struct {
	code    string
	message string
	cause   error
}

func (e ollamaError) Error() string { return e.message }
func (e ollamaError) Unwrap() error { return e.cause }

// ollamaErrorMessage returns the human-readable message for a stable ollama
// error code, matching the prose contract other providers' errors use.
func ollamaErrorMessage(code string) string {
	switch code {
	case ollamaAuthenticationError:
		return "authentication failed"
	case ollamaTimeoutError:
		return fmt.Sprintf("timeout after %s", fetchTimeout)
	case ollamaRateLimitedError:
		return "rate limited"
	case ollamaUnavailableError:
		return "unavailable"
	case ollamaMalformedResponseError:
		return "malformed response"
	default:
		return code
	}
}

func ollamaFailure(code string, cause error) error {
	return ollamaError{code: code, message: ollamaErrorMessage(code), cause: cause}
}
