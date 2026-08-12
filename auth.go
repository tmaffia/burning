package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

const (
	authFileName = "auth.json"
	lockFileName = "auth.lock"
)

type authFile struct {
	Credentials map[string]json.RawMessage `json:"credentials"`
}

func credential(provider string) (json.RawMessage, bool, error) {
	if provider == "" {
		return nil, false, errors.New("auth: provider is required")
	}
	path, err := authPath()
	if err != nil {
		return nil, false, err
	}
	auth, err := readAuth(path)
	if err != nil {
		return nil, false, err
	}
	value, ok := auth.Credentials[provider]
	return append(json.RawMessage(nil), value...), ok, nil
}

func storeCredential(ctx context.Context, provider string, value json.RawMessage) error {
	if provider == "" {
		return errors.New("auth: provider is required")
	}
	if !json.Valid(value) {
		return errors.New("auth: credential is not valid JSON")
	}
	return mutateCredentials(ctx, func(auth *authFile) bool {
		auth.Credentials[provider] = append(json.RawMessage(nil), value...)
		return true
	})
}

func removeCredential(ctx context.Context, provider string) error {
	if provider == "" {
		return errors.New("auth: provider is required")
	}
	return mutateCredentials(ctx, func(auth *authFile) bool {
		if _, ok := auth.Credentials[provider]; !ok {
			return false
		}
		delete(auth.Credentials, provider)
		return true
	})
}

func mutateCredentials(ctx context.Context, mutate func(*authFile) bool) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	path, lock, err := lockAuth(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = unix.Flock(int(lock.Fd()), unix.LOCK_UN)
		_ = lock.Close()
	}()

	auth, err := readAuth(path)
	if err != nil {
		return err
	}
	if !mutate(&auth) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	return writeAuth(path, auth)
}

func authDirectory() (string, error) {
	base, err := userConfigDir()
	if err != nil {
		return "", fmt.Errorf("auth: config directory: %w", err)
	}
	return filepath.Join(base, "burning"), nil
}

// authPath resolves the credential file without creating anything.
func authPath() (string, error) {
	dir, err := authDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, authFileName), nil
}

// lockAuth creates the owner-only config directory, takes the exclusive lock,
// and returns the credential file path to operate on.
func lockAuth(ctx context.Context) (string, *os.File, error) {
	dir, err := authDirectory()
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, fmt.Errorf("auth: create config directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", nil, fmt.Errorf("auth: secure config directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, lockFileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", nil, fmt.Errorf("auth: open lock: %w", err)
	}
	if err := os.Chmod(lock.Name(), 0o600); err != nil {
		_ = lock.Close()
		return "", nil, fmt.Errorf("auth: secure lock: %w", err)
	}
	for {
		err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return filepath.Join(dir, authFileName), lock, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			_ = lock.Close()
			return "", nil, fmt.Errorf("auth: lock: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = lock.Close()
			return "", nil, fmt.Errorf("auth: %w", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func readAuth(path string) (authFile, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return authFile{Credentials: make(map[string]json.RawMessage)}, nil
	}
	if err != nil {
		return authFile{}, fmt.Errorf("auth: read credentials: %w", err)
	}
	var auth authFile
	if err := json.Unmarshal(b, &auth); err != nil {
		return authFile{}, fmt.Errorf("auth: decode credentials: %w", err)
	}
	if auth.Credentials == nil {
		auth.Credentials = make(map[string]json.RawMessage)
	}
	return auth, nil
}

func writeAuth(path string, auth authFile) error {
	b, err := json.Marshal(auth)
	if err != nil {
		return fmt.Errorf("auth: encode credentials: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".auth-")
	if err != nil {
		return fmt.Errorf("auth: create temporary credentials: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("auth: secure temporary credentials: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		return fmt.Errorf("auth: write credentials: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("auth: sync credentials: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("auth: close credentials: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("auth: replace credentials: %w", err)
	}
	return nil
}
