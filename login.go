package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"golang.org/x/term"
)

var (
	isTerminal   = term.IsTerminal
	readPassword = term.ReadPassword
	openURL      = openBrowser
)

func runLogin(ctx context.Context, args []string, stdin *os.File, stdout, stderr io.Writer) int {
	return runCredentialCommand(ctx, "login", "saved", args, stdin, stdout, stderr, func(p knownProvider) error {
		value, err := registry[p.name].login(ctx, stdin, stdout)
		if err != nil {
			return err
		}
		if err := storeCredential(ctx, p.name, value); err != nil {
			return err
		}
		return configureProvider(p.name)
	})
}

func runLogout(ctx context.Context, args []string, stdin *os.File, stdout, stderr io.Writer) int {
	return runCredentialCommand(ctx, "logout", "removed", args, stdin, stdout, stderr, func(p knownProvider) error {
		if err := deconfigureProvider(p.name); err != nil {
			return err
		}
		return removeCredential(ctx, p.name)
	})
}

// runCredentialCommand is the shape login and logout share: reject arguments,
// choose a Provider interactively, apply the change, confirm it.
func runCredentialCommand(ctx context.Context, verb, outcome string, args []string, stdin *os.File, stdout, stderr io.Writer, change func(knownProvider) error) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "burning: %s: unexpected argument %q\n", verb, args[0])
		return 2
	}
	provider, err := chooseProvider(ctx, stdin, stdout)
	if err == nil {
		err = change(provider)
	}
	if errors.Is(err, context.Canceled) {
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "burning: %s: %v\n", verb, err)
		return 2
	}
	fmt.Fprintf(stdout, "%s credential %s.\n", provider.label, outcome)
	return 0
}

// readSecret prompts for a credential without echoing it and returns the value
// to store. Errors never carry the credential itself.
func readSecret(ctx context.Context, stdin *os.File, stdout io.Writer) (json.RawMessage, error) {
	fmt.Fprint(stdout, "Credential: ")
	fd := int(stdin.Fd())
	state, stateErr := term.GetState(fd)
	type result struct {
		secret []byte
		err    error
	}
	done := make(chan result, 1)
	go func() {
		secret, err := readPassword(fd)
		done <- result{secret, err}
	}()
	select {
	case <-ctx.Done():
		if stateErr == nil {
			_ = term.Restore(fd, state)
		}
		fmt.Fprintln(stdout)
		return nil, ctx.Err()
	case r := <-done:
		fmt.Fprintln(stdout)
		if r.err != nil {
			return nil, errors.New("could not read credential")
		}
		defer clear(r.secret)
		if len(bytes.TrimSpace(r.secret)) == 0 {
			return nil, errors.New("credential is required")
		}
		return json.Marshal(struct {
			Secret string `json:"secret"`
		}{string(r.secret)})
	}
}

func openBrowser(url string) error {
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	return exec.Command(command, url).Start()
}

func chooseProvider(ctx context.Context, stdin *os.File, stdout io.Writer) (knownProvider, error) {
	if !isTerminal(int(stdin.Fd())) {
		return knownProvider{}, errors.New("stdin is not a terminal")
	}
	fmt.Fprintln(stdout, "Choose a provider:")
	for i, provider := range knownProviders {
		fmt.Fprintf(stdout, "  %d) %s\n", i+1, provider.label)
	}
	fmt.Fprint(stdout, "Provider: ")
	choice, err := readChoice(ctx, stdin)
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(stdout)
		return knownProvider{}, err
	}
	if err != nil {
		return knownProvider{}, errors.New("could not read provider")
	}
	for i, provider := range knownProviders {
		if choice == fmt.Sprint(i+1) || strings.EqualFold(choice, provider.name) {
			return provider, nil
		}
	}
	return knownProvider{}, errors.New("unknown provider")
}

// ponytail: Fscan stays blocked until stdin closes; the process exits on cancel.
func readChoice(ctx context.Context, stdin *os.File) (string, error) {
	type result struct {
		choice string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		var choice string
		_, err := fmt.Fscan(stdin, &choice)
		done <- result{choice, err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-done:
		return r.choice, r.err
	}
}
