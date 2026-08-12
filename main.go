// Command burning reports coding-agent subscription usage.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/term"
)

// version is the release version; release builds stamp it via
//
//	go build -ldflags "-X main.version=v0.1.0"
var version = "dev"

const helpText = `burning reports coding-agent subscription usage.

Usage:
  burning [--json]

Flags:
  --json      print the report as JSON schema v1 instead of the human format
  --version   print the version and exit
  --help      show this help

Exit codes:
  0  all providers reported usage (or none are configured)
  1  one or more providers failed
  2  fatal error (bad flags, unreadable config)

Providers are read from $XDG_CONFIG_HOME/burning/config.json
(~/Library/Application Support/burning/config.json on macOS), e.g.
  {"providers": ["openai", "ollama"]}
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("burning", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, helpText) }
	asJSON := fs.Bool("json", false, "print the report as JSON schema v1 instead of the human format")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "burning %s\n", version)
		return 0
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "burning: unexpected argument %q\n\n", fs.Arg(0))
		fmt.Fprint(stderr, helpText)
		return 2
	}

	names, err := configuredProviders()
	if err != nil {
		fmt.Fprintf(stderr, "burning: %v\n", err)
		return 2
	}
	results := fetchAll(context.Background(), names)
	now := time.Now()

	if *asJSON {
		if err := renderJSON(stdout, results, now); err != nil {
			fmt.Fprintf(stderr, "burning: %v\n", err)
			return 2
		}
		return exitCode(results)
	}

	width := 0 // unconstrained when piped; bars always shown
	tty := term.IsTerminal(int(os.Stdout.Fd()))
	if tty {
		if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
			width = w
		}
	}
	renderHuman(stdout, results, now, width, tty && os.Getenv("NO_COLOR") == "")
	return exitCode(results)
}
