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

// configuredProviders returns the provider names listed in the user's config
// file. A missing file means no providers are configured.
func configuredProviders() ([]string, error) {
	dir, err := userConfigDir()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "burning", "config.json"))
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
