package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type e2eWindow struct {
	Name                      string     `json:"name"`
	DurationSeconds           int64      `json:"duration_seconds"`
	UsagePercent              float64    `json:"usage_percent"`
	RemainingAllowancePercent float64    `json:"remaining_allowance_percent"`
	ResetsAt                  *time.Time `json:"resets_at"`
	RemainingSeconds          *int64     `json:"remaining_seconds"`
}

type e2eProvider struct {
	Name    string      `json:"name"`
	Windows []e2eWindow `json:"windows"`
}

type e2eError struct {
	Provider string `json:"provider"`
	Message  string `json:"message"`
}

type e2eReport struct {
	Schema      string        `json:"schema"`
	GeneratedAt time.Time     `json:"generated_at"`
	Providers   []e2eProvider `json:"providers"`
	Errors      []e2eError    `json:"errors"`
}

func buildExecutable(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "burning")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build executable: %v\n%s", err, output)
	}
	return binary
}

func runExecutable(t *testing.T, binary string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return out.String(), errOut.String(), exitErr.ExitCode()
		}
		t.Fatalf("run executable: %v", err)
	}
	return out.String(), errOut.String(), 0
}

func TestExecutableSmoke(t *testing.T) {
	binary := buildExecutable(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	for _, test := range []struct {
		name  string
		args  []string
		exit  int
		check func(*testing.T, string, string)
	}{
		{
			name: "default report", exit: 0,
			check: func(t *testing.T, stdout, stderr string) {
				if stdout != "No providers configured.\n" || stderr != "" {
					t.Errorf("stdout = %q, stderr = %q", stdout, stderr)
				}
			},
		},
		{
			name: "JSON report", args: []string{"--json"}, exit: 0,
			check: func(t *testing.T, stdout, stderr string) {
				var report e2eReport
				if err := json.Unmarshal([]byte(stdout), &report); err != nil {
					t.Fatal(err)
				}
				if report.Schema != "burning.usage.v1" || len(report.Providers) != 0 || len(report.Errors) != 0 || stderr != "" {
					t.Errorf("report = %+v, stderr = %q", report, stderr)
				}
			},
		},
		{
			name: "version", args: []string{"--version"}, exit: 0,
			check: func(t *testing.T, stdout, stderr string) {
				if stdout != "burning dev\n" || stderr != "" {
					t.Errorf("stdout = %q, stderr = %q", stdout, stderr)
				}
			},
		},
		{
			name: "help", args: []string{"--help"}, exit: 0,
			check: func(t *testing.T, stdout, stderr string) {
				if stdout != "" || !strings.Contains(stderr, "Usage:") {
					t.Errorf("stdout = %q, stderr = %q", stdout, stderr)
				}
			},
		},
		{
			name: "bad flag", args: []string{"--nope"}, exit: 2,
			check: func(t *testing.T, stdout, stderr string) {
				if stdout != "" || !strings.Contains(stderr, "flag provided but not defined: -nope") {
					t.Errorf("stdout = %q, stderr = %q", stdout, stderr)
				}
			},
		},
		{
			name: "unexpected argument", args: []string{"unexpected"}, exit: 2,
			check: func(t *testing.T, stdout, stderr string) {
				if stdout != "" || !strings.Contains(stderr, `unexpected argument "unexpected"`) {
					t.Errorf("stdout = %q, stderr = %q", stdout, stderr)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr, exitCode := runExecutable(t, binary, test.args...)
			if exitCode != test.exit {
				t.Fatalf("exit = %d, want %d; stdout = %q, stderr = %q", exitCode, test.exit, stdout, stderr)
			}
			test.check(t, stdout, stderr)
		})
	}
}

func TestLiveProviders(t *testing.T) {
	if os.Getenv("BURNING_E2E") == "" {
		t.Skip("set BURNING_E2E=1 to check configured providers")
	}

	stdout, stderr, exitCode := runExecutable(t, buildExecutable(t), "--json")
	if exitCode != 0 {
		t.Errorf("exit = %d; stdout = %q, stderr = %q", exitCode, stdout, stderr)
	}

	var report e2eReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("decode live report: %v; stderr = %q", err, stderr)
	}
	if report.Schema != "burning.usage.v1" {
		t.Errorf("schema = %q, want burning.usage.v1", report.Schema)
	}
	if report.GeneratedAt.IsZero() {
		t.Error("live report has no generated_at")
	}
	if len(report.Providers) == 0 {
		t.Error("no configured providers reported usage")
	}
	if len(report.Errors) != 0 {
		t.Errorf("provider errors = %v", report.Errors)
	}
	configured, err := configuredProviders()
	if err != nil {
		t.Errorf("read configured providers: %v", err)
	}
	if len(configured) == 0 {
		t.Error("no providers configured")
	}
	reported := make(map[string]bool)
	for _, provider := range report.Providers {
		reported[provider.Name] = true
		if provider.Name == "" {
			t.Error("report has a provider without a name")
		}
		if len(provider.Windows) == 0 {
			t.Errorf("provider %q has no usage windows", provider.Name)
		}
		for _, window := range provider.Windows {
			if window.Name == "" || window.DurationSeconds <= 0 || window.UsagePercent < 0 || window.UsagePercent > 100 || window.RemainingAllowancePercent < 0 || window.RemainingAllowancePercent > 100 || math.Abs(window.UsagePercent+window.RemainingAllowancePercent-100) > 1e-9 || (window.ResetsAt == nil) != (window.RemainingSeconds == nil) || window.RemainingSeconds != nil && *window.RemainingSeconds < 0 {
				t.Errorf("provider %q has an invalid usage window: %+v", provider.Name, window)
			}
		}
	}
	for _, provider := range configured {
		if !reported[provider] {
			t.Errorf("configured provider %q was not reported", provider)
		}
	}
}
