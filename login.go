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
	return runCredentialCommand("login", "saved", args, stdin, stdout, stderr, func(p knownProvider) error {
		if p.name == "ollama" {
			fmt.Fprintf(stdout, "Opening %s\n", ollamaAPIKeysURL)
			if err := openURL(ollamaAPIKeysURL); err != nil {
				return errors.New("could not open Ollama Cloud API-key page")
			}
		}
		value, err := readSecret(stdin, stdout)
		if err != nil {
			return err
		}
		if p.name == "ollama" {
			key, err := ollamaKey(value)
			if err != nil {
				return err
			}
			verifyCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
			defer cancel()
			if _, err := fetchOllamaUsage(verifyCtx, key); err != nil {
				return err
			}
		}
		return storeCredential(ctx, p.name, value)
	})
}

func runLogout(ctx context.Context, args []string, stdin *os.File, stdout, stderr io.Writer) int {
	return runCredentialCommand("logout", "removed", args, stdin, stdout, stderr, func(p knownProvider) error {
		return removeCredential(ctx, p.name)
	})
}

// runCredentialCommand is the shape login and logout share: reject arguments,
// choose a Provider interactively, apply the change, confirm it.
func runCredentialCommand(verb, outcome string, args []string, stdin *os.File, stdout, stderr io.Writer, change func(knownProvider) error) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "burning: %s: unexpected argument %q\n", verb, args[0])
		return 2
	}
	provider, err := chooseProvider(stdin, stdout)
	if err == nil {
		err = change(provider)
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
func readSecret(stdin *os.File, stdout io.Writer) (json.RawMessage, error) {
	fmt.Fprint(stdout, "Credential: ")
	secret, err := readPassword(int(stdin.Fd()))
	fmt.Fprintln(stdout)
	if err != nil {
		return nil, errors.New("could not read credential")
	}
	defer clear(secret)
	if len(bytes.TrimSpace(secret)) == 0 {
		return nil, errors.New("credential is required")
	}
	return json.Marshal(struct {
		Secret string `json:"secret"`
	}{string(secret)})
}

func openBrowser(url string) error {
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	return exec.Command(command, url).Start()
}

func chooseProvider(stdin *os.File, stdout io.Writer) (knownProvider, error) {
	if !isTerminal(int(stdin.Fd())) {
		return knownProvider{}, errors.New("stdin is not a terminal")
	}
	fmt.Fprintln(stdout, "Choose a provider:")
	for i, provider := range knownProviders {
		fmt.Fprintf(stdout, "  %d) %s\n", i+1, provider.label)
	}
	fmt.Fprint(stdout, "Provider: ")
	var choice string
	if _, err := fmt.Fscan(stdin, &choice); err != nil {
		return knownProvider{}, errors.New("could not read provider")
	}
	for i, provider := range knownProviders {
		if choice == fmt.Sprint(i+1) || strings.EqualFold(choice, provider.name) {
			return provider, nil
		}
	}
	return knownProvider{}, errors.New("unknown provider")
}
