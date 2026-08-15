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

const ollamaAPIKeysURL = "https://ollama.com/settings/keys"

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

// login opens Ollama Cloud's API-key page, prompts for the key, and verifies
// it against the usage endpoint before it's stored.
func (ollamaProvider) login(ctx context.Context, stdin *os.File, stdout io.Writer) (json.RawMessage, error) {
	fmt.Fprintf(stdout, "Opening %s\n", ollamaAPIKeysURL)
	if err := openURL(ollamaAPIKeysURL); err != nil {
		return nil, errors.New("could not open Ollama Cloud API-key page")
	}
	value, err := readSecret(ctx, stdin, stdout)
	if err != nil {
		return nil, err
	}
	secret, err := ollamaCredentialSecret(value)
	if err != nil {
		return nil, err
	}
	verifyCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	if _, err := fetchOllamaUsage(verifyCtx, secret); err != nil {
		return nil, err
	}
	return value, nil
}

func ollamaCredential() (string, error) {
	if secret := os.Getenv("OLLAMA_API_KEY"); secret != "" {
		return secret, nil
	}
	value, ok, err := credential("ollama")
	if err != nil || !ok {
		return "", providerFailure("ollama", categoryAuthentication, err)
	}
	return ollamaCredentialSecret(value)
}

func ollamaCredentialSecret(value json.RawMessage) (string, error) {
	var credential struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(value, &credential); err != nil || credential.Secret == "" {
		return "", providerFailure("ollama", categoryAuthentication, err)
	}
	return credential.Secret, nil
}

func fetchOllamaUsage(ctx context.Context, secret string) ([]usageWindow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ollamaUsageURL, nil)
	if err != nil {
		return nil, providerFailure("ollama", categoryUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, providerFailure("ollama", categoryTimeout, err)
		}
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, providerFailure("ollama", categoryUnavailable, err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, providerFailure("ollama", classifyHTTPStatus(res.StatusCode), nil)
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
		return nil, providerFailure("ollama", categoryMalformedResponse, err)
	}
	if !validOllamaUsage(response.Limits.Session.Usage) || !validOllamaUsage(response.Limits.Weekly.Usage) {
		return nil, providerFailure("ollama", categoryMalformedResponse, nil)
	}
	return []usageWindow{
		{Name: "session", Duration: 5 * time.Hour, Usage: usageFromFraction(*response.Limits.Session.Usage)},
		{Name: "weekly", Duration: 7 * 24 * time.Hour, Usage: usageFromFraction(*response.Limits.Weekly.Usage)},
	}, nil
}

func validOllamaUsage(value *float64) bool {
	return value != nil && *value >= 0 && *value <= 1
}
