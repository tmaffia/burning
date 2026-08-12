package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// configFile is the on-disk config payload.
type configFile struct {
	Providers []string `json:"providers"`
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
	var cfg configFile
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return cfg.Providers, nil
}

// writeConfiguredProviders is the single route for config mutations: it
// ensures the directory exists, then writes owner-only.
func writeConfiguredProviders(providers []string) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	b, err := json.Marshal(configFile{Providers: providers})
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

// deconfigureProvider removes provider from the configured list, preserving
// the order of the others. A provider not listed is a no-op.
func deconfigureProvider(provider string) error {
	providers, err := configuredProviders()
	if err != nil {
		return err
	}
	filtered := slices.DeleteFunc(providers, func(p string) bool { return p == provider })
	if len(filtered) == len(providers) {
		return nil
	}
	return writeConfiguredProviders(filtered)
}

func configureProvider(provider string) error {
	providers, err := configuredProviders()
	if err != nil {
		return err
	}
	if slices.Contains(providers, provider) {
		return nil
	}
	return writeConfiguredProviders(append(providers, provider))
}
