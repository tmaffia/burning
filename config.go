package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// userConfigDir is overridable in tests.
var userConfigDir = os.UserConfigDir

func configPath() (string, error) {
	dir, err := userConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	return filepath.Join(dir, "burning", "config.json"), nil
}

// configuredProviders returns the provider names listed in the user's config
// file. A missing file means no providers are configured.
func configuredProviders() ([]string, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	var cfg struct {
		Providers []string `json:"providers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return cfg.Providers, nil
}

// deconfigureProvider removes provider from the configured list, preserving
// the order of the others. A provider not listed is a no-op.
func deconfigureProvider(provider string) error {
	providers, err := configuredProviders()
	if err != nil {
		return err
	}
	filtered := providers[:0]
	found := false
	for _, configured := range providers {
		if configured == provider {
			found = true
			continue
		}
		filtered = append(filtered, configured)
	}
	if !found {
		return nil
	}
	path, err := configPath()
	if err != nil {
		return err
	}
	b, err := json.Marshal(struct {
		Providers []string `json:"providers"`
	}{filtered})
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

func configureProvider(provider string) error {
	providers, err := configuredProviders()
	if err != nil {
		return err
	}
	for _, configured := range providers {
		if configured == provider {
			return nil
		}
	}
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	b, err := json.Marshal(struct {
		Providers []string `json:"providers"`
	}{append(providers, provider)})
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}
