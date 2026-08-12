package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func interactiveInput(t *testing.T, input string) *os.File {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString(input); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	return reader
}

func useInteractiveLogin(t *testing.T) {
	t.Helper()
	oldTerminal, oldPassword := isTerminal, readPassword
	isTerminal = func(int) bool { return true }
	readPassword = func(int) ([]byte, error) { return []byte("secret"), nil }
	t.Cleanup(func() {
		isTerminal, readPassword = oldTerminal, oldPassword
	})
}

func TestLoginAndLogoutSelectProvider(t *testing.T) {
	useConfig(t, nil)
	setRegistry(t, fakeProvider{name: "openai"}) // exercise the shared pasted-credential path
	useInteractiveLogin(t)
	if err := storeCredential(context.Background(), "ollama", json.RawMessage(`{"token":"keep"}`)); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runWithInput(context.Background(), []string{"login"}, interactiveInput(t, "1\n"), &out, &errOut); code != 0 {
		t.Fatalf("login exit = %d, stderr = %q", code, errOut.String())
	}
	if strings.Contains(out.String(), "secret") || strings.Contains(errOut.String(), "secret") {
		t.Fatal("credential appeared in command output")
	}
	if got, ok, err := credential("openai"); err != nil || !ok || string(got) != `{"secret":"secret"}` {
		t.Errorf("openai credential was not stored: exists = %v, err = %v", ok, err)
	}

	out.Reset()
	errOut.Reset()
	if code := runWithInput(context.Background(), []string{"logout"}, interactiveInput(t, "1\n"), &out, &errOut); code != 0 {
		t.Fatalf("logout exit = %d, stderr = %q", code, errOut.String())
	}
	if _, ok, err := credential("openai"); err != nil || ok {
		t.Errorf("openai credential exists = %v, err = %v", ok, err)
	}
	if _, ok, err := credential("ollama"); err != nil || !ok {
		t.Errorf("ollama credential exists = %v, err = %v", ok, err)
	}
}

func TestLogoutRemovesProviderAndCredential(t *testing.T) {
	useConfig(t, []string{"openai", "ollama"})
	setRegistry(t,
		fakeProvider{name: "openai", windows: []usageWindow{{Name: "session", Duration: 5 * time.Hour, Usage: usageFromFraction(0.5)}}},
		fakeProvider{name: "ollama", windows: []usageWindow{{Name: "session", Duration: 5 * time.Hour, Usage: usageFromFraction(0.1)}}},
	)
	useInteractiveLogin(t)
	if err := storeCredential(context.Background(), "openai", json.RawMessage(`{"token":"a"}`)); err != nil {
		t.Fatal(err)
	}
	if err := storeCredential(context.Background(), "ollama", json.RawMessage(`{"token":"b"}`)); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if code := runWithInput(context.Background(), []string{"logout"}, interactiveInput(t, "1\n"), &out, &errOut); code != 0 {
		t.Fatalf("logout exit = %d, stderr = %q", code, errOut.String())
	}

	if _, ok, err := credential("openai"); err != nil || ok {
		t.Errorf("openai credential exists = %v, err = %v", ok, err)
	}
	if _, ok, err := credential("ollama"); err != nil || !ok {
		t.Errorf("ollama credential exists = %v, err = %v", ok, err)
	}

	providers, err := configuredProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 || providers[0] != "ollama" {
		t.Errorf("configured providers = %v, want [ollama]", providers)
	}

	var jsonOut bytes.Buffer
	if code := run([]string{"--json"}, &jsonOut, &bytes.Buffer{}); code != 0 {
		t.Fatalf("json report exit = %d", code)
	}
	var doc jsonReport
	if err := json.Unmarshal(jsonOut.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Providers) != 1 || doc.Providers[0].Name != "ollama" || len(doc.Errors) != 0 {
		t.Errorf("providers = %+v, errors = %+v", doc.Providers, doc.Errors)
	}

	var human bytes.Buffer
	if code := run(nil, &human, &bytes.Buffer{}); code != 0 {
		t.Fatalf("human report exit = %d", code)
	}
	if strings.Contains(human.String(), "openai") {
		t.Errorf("human output mentions logged-out provider:\n%s", human.String())
	}
}

func TestCredentialStoreRoundTripAndPermissions(t *testing.T) {
	dir := useConfig(t, nil)
	want := json.RawMessage(`{"token":"secret"}`)
	if err := storeCredential(context.Background(), "openai", want); err != nil {
		t.Fatal(err)
	}

	got, ok, err := credential("openai")
	if err != nil || !ok {
		t.Fatalf("credential missing: exists = %v, err = %v", ok, err)
	}
	if string(got) != string(want) {
		t.Error("credential did not round-trip")
	}
	for _, path := range []string{filepath.Join(dir, "burning"), filepath.Join(dir, "burning", "auth.json")} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		wantMode := os.FileMode(0o700)
		if filepath.Base(path) == "auth.json" {
			wantMode = 0o600
		}
		if got := info.Mode().Perm(); got != wantMode {
			t.Errorf("%s mode = %o, want %o", path, got, wantMode)
		}
	}
}

func TestCredentialStoreWritesAtomically(t *testing.T) {
	dir := useConfig(t, nil)
	if err := storeCredential(context.Background(), "openai", json.RawMessage(`{"token":"old"}`)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "burning", "auth.json")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := storeCredential(context.Background(), "openai", json.RawMessage(`{"token":"new"}`)); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Fatal("auth.json was updated in place")
	}
}

func TestCredentialStoreReplacementAndDeletion(t *testing.T) {
	useConfig(t, nil)
	if err := storeCredential(context.Background(), "openai", json.RawMessage(`{"token":"old"}`)); err != nil {
		t.Fatal(err)
	}
	if err := storeCredential(context.Background(), "openai", json.RawMessage(`{"token":"new"}`)); err != nil {
		t.Fatal(err)
	}
	if err := storeCredential(context.Background(), "ollama", json.RawMessage(`{"key":"keep"}`)); err != nil {
		t.Fatal(err)
	}
	if err := removeCredential(context.Background(), "openai"); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := credential("openai"); err != nil || ok {
		t.Errorf("openai credential exists = %v, err = %v", ok, err)
	}
	got, ok, err := credential("ollama")
	if err != nil || !ok || string(got) != `{"key":"keep"}` {
		t.Errorf("ollama credential was not retained: exists = %v, err = %v", ok, err)
	}
}

func TestCredentialStoreCancellationPreservesCredential(t *testing.T) {
	useConfig(t, nil)
	if err := storeCredential(context.Background(), "openai", json.RawMessage(`{"token":"old"}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := storeCredential(ctx, "openai", json.RawMessage(`{"token":"new"}`)); err == nil {
		t.Fatal("storeCredential with canceled context succeeded")
	}

	got, ok, err := credential("openai")
	if err != nil || !ok || string(got) != `{"token":"old"}` {
		t.Errorf("credential changed: exists = %v, err = %v", ok, err)
	}
}

func TestCredentialStoreCancellationWhileLockedPreservesCredential(t *testing.T) {
	dir := useConfig(t, nil)
	if err := storeCredential(context.Background(), "openai", json.RawMessage(`{"token":"old"}`)); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, "burning", "auth.lock"), os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(time.Millisecond, cancel)
	if err := storeCredential(ctx, "openai", json.RawMessage(`{"token":"new"}`)); err == nil {
		t.Fatal("storeCredential succeeded after cancellation")
	}
	got, ok, err := credential("openai")
	if err != nil || !ok || string(got) != `{"token":"old"}` {
		t.Errorf("credential changed: exists = %v, err = %v", ok, err)
	}
}

func TestCredentialStoreConcurrentMutations(t *testing.T) {
	useConfig(t, nil)
	const count = 20
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			provider := "provider-" + string(rune('a'+i))
			if err := storeCredential(context.Background(), provider, json.RawMessage(`{"token":"secret"}`)); err != nil {
				t.Errorf("store %s: %v", provider, err)
			}
		}()
	}
	wg.Wait()

	for i := range count {
		provider := "provider-" + string(rune('a'+i))
		if _, ok, err := credential(provider); err != nil || !ok {
			t.Errorf("credential %s exists = %v, err = %v", provider, ok, err)
		}
	}
}
