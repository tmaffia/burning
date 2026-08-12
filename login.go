package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

type loginProvider struct {
	name  string
	label string
}

var (
	loginProviders = []loginProvider{
		{name: "openai", label: "OpenAI Codex"},
		{name: "ollama", label: "Ollama Cloud"},
	}
	isTerminal   = term.IsTerminal
	readPassword = term.ReadPassword
)

func runLogin(ctx context.Context, args []string, stdin *os.File, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "burning: login: unexpected argument %q\n", args[0])
		return 2
	}
	provider, err := chooseLoginProvider(stdin, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "burning: login: %v\n", err)
		return 2
	}
	fmt.Fprint(stdout, "Credential: ")
	secret, err := readPassword(int(stdin.Fd()))
	fmt.Fprintln(stdout)
	if err != nil {
		fmt.Fprintln(stderr, "burning: login: could not read credential")
		return 2
	}
	defer clear(secret)
	if len(bytes.TrimSpace(secret)) == 0 {
		fmt.Fprintln(stderr, "burning: login: credential is required")
		return 2
	}
	value, err := json.Marshal(struct {
		Secret string `json:"secret"`
	}{string(secret)})
	if err != nil {
		fmt.Fprintln(stderr, "burning: login: could not store credential")
		return 2
	}
	if err := storeCredential(ctx, provider.name, value); err != nil {
		fmt.Fprintf(stderr, "burning: login: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "%s credential saved.\n", provider.label)
	return 0
}

func runLogout(ctx context.Context, args []string, stdin *os.File, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "burning: logout: unexpected argument %q\n", args[0])
		return 2
	}
	provider, err := chooseLoginProvider(stdin, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "burning: logout: %v\n", err)
		return 2
	}
	if err := removeCredential(ctx, provider.name); err != nil {
		fmt.Fprintf(stderr, "burning: logout: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "%s credential removed.\n", provider.label)
	return 0
}

func chooseLoginProvider(stdin *os.File, stdout io.Writer) (loginProvider, error) {
	if !isTerminal(int(stdin.Fd())) {
		return loginProvider{}, fmt.Errorf("stdin is not a terminal")
	}
	fmt.Fprintln(stdout, "Choose a provider:")
	for i, provider := range loginProviders {
		fmt.Fprintf(stdout, "  %d) %s\n", i+1, provider.label)
	}
	fmt.Fprint(stdout, "Provider: ")
	var choice string
	if _, err := fmt.Fscan(stdin, &choice); err != nil {
		return loginProvider{}, fmt.Errorf("could not read provider")
	}
	for i, provider := range loginProviders {
		if choice == fmt.Sprint(i+1) || strings.EqualFold(choice, provider.name) {
			return provider, nil
		}
	}
	return loginProvider{}, fmt.Errorf("unknown provider")
}

func clear(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
