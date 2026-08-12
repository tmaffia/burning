package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var testNow = time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)

// fakeProvider demonstrates the provider seam without real authentication or
// network calls.
type fakeProvider struct {
	name    string
	windows []usageWindow
	err     error
	delay   time.Duration // ignores context, simulating an uncooperative provider
}

func (f fakeProvider) Name() string { return f.name }

func (f fakeProvider) Usage(context.Context) ([]usageWindow, error) {
	time.Sleep(f.delay)
	return f.windows, f.err
}

func setRegistry(ps ...provider) {
	registry = map[string]provider{}
	for _, p := range ps {
		registry[p.Name()] = p
	}
}

// useConfig points config and credential loading at a temp dir, returning it,
// and optionally writes a config file listing providers. nil providers means
// no config file exists.
func useConfig(t *testing.T, providers []string) string {
	t.Helper()
	dir := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { userConfigDir = old })
	if providers == nil {
		return dir
	}
	b, err := json.Marshal(struct {
		Providers []string `json:"providers"`
	}{providers})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "burning"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "burning", "config.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFullSuccessJSON(t *testing.T) {
	setRegistry(
		fakeProvider{name: "openai", windows: []usageWindow{
			{Name: "session", Duration: 5 * time.Hour, Usage: usageFromFraction(0.284), ResetsAt: time.Now().Add(3*time.Hour + 12*time.Minute)},
			{Name: "weekly", Duration: 7 * 24 * time.Hour, Usage: usageFromFraction(0.607), ResetsAt: time.Now().Add(4*24*time.Hour + 6*time.Hour)},
		}},
		fakeProvider{name: "ollama", windows: []usageWindow{
			{Name: "session", Duration: 5 * time.Hour, Usage: usageFromFraction(0.05)},
		}},
	)
	useConfig(t, []string{"openai", "ollama"})

	var out, errOut bytes.Buffer
	if code := run([]string{"--json"}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut.String())
	}
	if strings.Contains(out.String(), "\x1b") {
		t.Fatal("JSON output contains ANSI escapes")
	}
	var doc jsonReport
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Schema != schemaVersion {
		t.Errorf("schema = %q, want %q", doc.Schema, schemaVersion)
	}
	if len(doc.Providers) != 2 || len(doc.Errors) != 0 {
		t.Fatalf("providers = %d, errors = %d, want 2 and 0", len(doc.Providers), len(doc.Errors))
	}
	openai := doc.Providers[0]
	if openai.Name != "openai" || len(openai.Windows) != 2 {
		t.Fatalf("openai = %+v", openai)
	}
	sess := openai.Windows[0]
	if sess.Name != "session" || sess.DurationSeconds != 5*3600 {
		t.Errorf("session = %+v", sess)
	}
	if sess.UsagePercent != 0.284*100 {
		t.Errorf("usage_percent = %v, want %v (normalized precision)", sess.UsagePercent, 0.284*100)
	}
	if sess.RemainingAllowancePercent != (1-0.284)*100 {
		t.Errorf("remaining_allowance_percent = %v", sess.RemainingAllowancePercent)
	}
	if sess.ResetsAt == nil || sess.RemainingSeconds == nil || *sess.RemainingSeconds <= 0 {
		t.Errorf("resets_at = %v, remaining_seconds = %v, want set", sess.ResetsAt, sess.RemainingSeconds)
	}
	weekly := openai.Windows[1]
	if weekly.Name != "weekly" || weekly.DurationSeconds != 7*24*3600 {
		t.Errorf("weekly = %+v", weekly)
	}
	ollama := doc.Providers[1]
	if ollama.Name != "ollama" || len(ollama.Windows) != 1 {
		t.Fatalf("ollama = %+v", ollama)
	}
	if w := ollama.Windows[0]; w.ResetsAt != nil || w.RemainingSeconds != nil {
		t.Errorf("ollama window has reset info, want omitted: %+v", w)
	}
}

func TestHumanReport(t *testing.T) {
	setRegistry(fakeProvider{name: "openai", windows: []usageWindow{
		{Name: "session", Duration: 5 * time.Hour, Usage: usageFromFraction(0.284), ResetsAt: time.Now().Add(3*time.Hour + 12*time.Minute)},
		{Name: "weekly", Duration: 7 * 24 * time.Hour, Usage: usageFromFraction(0.607), ResetsAt: time.Now().Add(4*24*time.Hour + 6*time.Hour)},
	}})
	useConfig(t, []string{"openai"})

	var out bytes.Buffer
	if code := run(nil, &out, &bytes.Buffer{}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	got := out.String()
	for _, want := range []string{"█", "░", "5h", "7d", "28% usage", "61% usage", "3h12m", "4d6h", "│"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestPartialFailure(t *testing.T) {
	setRegistry(
		fakeProvider{name: "openai", windows: []usageWindow{{Name: "session", Duration: 5 * time.Hour, Usage: usageFromFraction(0.5)}}},
		fakeProvider{name: "ollama", err: errors.New("boom")},
	)
	useConfig(t, []string{"openai", "ollama"})

	var out bytes.Buffer
	if code := run([]string{"--json"}, &out, &bytes.Buffer{}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	var doc jsonReport
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Providers) != 1 || doc.Providers[0].Name != "openai" {
		t.Errorf("providers = %+v", doc.Providers)
	}
	if len(doc.Errors) != 1 || doc.Errors[0].Provider != "ollama" || doc.Errors[0].Message != "boom" {
		t.Errorf("errors = %+v", doc.Errors)
	}

	var human bytes.Buffer
	if code := run(nil, &human, &bytes.Buffer{}); code != 1 {
		t.Fatalf("human exit = %d, want 1", code)
	}
	if !strings.Contains(human.String(), "error: boom") {
		t.Errorf("human output missing error line:\n%s", human.String())
	}
}

func TestTimeout(t *testing.T) {
	setRegistry(fakeProvider{name: "openai", delay: 100 * time.Millisecond})
	useConfig(t, []string{"openai"})
	old := fetchTimeout
	fetchTimeout = 5 * time.Millisecond
	t.Cleanup(func() { fetchTimeout = old })

	var out bytes.Buffer
	if code := run([]string{"--json"}, &out, &bytes.Buffer{}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	var doc jsonReport
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Errors) != 1 || !strings.Contains(doc.Errors[0].Message, "timeout after 5ms") {
		t.Errorf("errors = %+v", doc.Errors)
	}
}

// TestCancelStopsFetch proves Ctrl-C reaches the report path, not just
// login/logout: a canceled context aborts provider fetches instead of waiting
// out fetchTimeout.
func TestCancelStopsFetch(t *testing.T) {
	setRegistry(fakeProvider{name: "openai", delay: 100 * time.Millisecond})
	useConfig(t, []string{"openai"})
	old := fetchTimeout
	fetchTimeout = 5 * time.Second // long enough that only cancellation can end the fetch
	t.Cleanup(func() { fetchTimeout = old })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var out bytes.Buffer
	if code := runWithInput(ctx, []string{"--json"}, os.Stdin, &out, &bytes.Buffer{}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	var doc jsonReport
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Errors) != 1 || !strings.Contains(doc.Errors[0].Message, "context canceled") {
		t.Errorf("errors = %+v, want a cancellation error", doc.Errors)
	}
}

func TestNoProviders(t *testing.T) {
	useConfig(t, nil) // no config file

	var out bytes.Buffer
	if code := run([]string{"--json"}, &out, &bytes.Buffer{}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var doc jsonReport
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Providers == nil || len(doc.Providers) != 0 {
		t.Errorf("providers = %+v, want empty array", doc.Providers)
	}

	var human bytes.Buffer
	if code := run(nil, &human, &bytes.Buffer{}); code != 0 {
		t.Fatalf("human exit = %d, want 0", code)
	}
	if !strings.Contains(human.String(), "No providers configured.") {
		t.Errorf("human output = %q", human.String())
	}
}

func TestUnknownProvider(t *testing.T) {
	useConfig(t, []string{"ghost"})

	var out bytes.Buffer
	if code := run([]string{"--json"}, &out, &bytes.Buffer{}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	var doc jsonReport
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Errors) != 1 || !strings.Contains(doc.Errors[0].Message, `unknown provider "ghost"`) {
		t.Errorf("errors = %+v", doc.Errors)
	}
}

func TestVersionFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--version"}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if got := out.String(); got != "burning dev\n" {
		t.Errorf("version output = %q", got)
	}
}

func TestHelpFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--help"}, &out, &errOut); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(errOut.String(), "Usage:") {
		t.Errorf("help output = %q", errOut.String())
	}
}

func TestBadFlagAndArg(t *testing.T) {
	for _, args := range [][]string{{"--nope"}, {"extra"}} {
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut); code != 2 {
			t.Errorf("args %v: exit = %d, want 2", args, code)
		}
	}
}

func TestBarEdges(t *testing.T) {
	if got := bar(usageFromFraction(0)); got != "▕░░░░░░░░░░▏" {
		t.Errorf("bar(0) = %q, want empty bar", got)
	}
	if got := bar(usageFromFraction(1)); got != "▕██████████▏" {
		t.Errorf("bar(1) = %q, want full bar", got)
	}
	if got := bar(usageFromFraction(0.284)); got != "▕███░░░░░░░▏" {
		t.Errorf("bar(0.284) = %q", got)
	}
}

func TestUsageNormalization(t *testing.T) {
	if got := usageFromFraction(-0.1); got.percent() != 0 || got.remainingAllowancePercent() != 100 {
		t.Errorf("usageFromFraction(-0.1) = %+v", got)
	}
	if got := usageFromFraction(1.1); got.percent() != 100 || got.remainingAllowancePercent() != 0 {
		t.Errorf("usageFromFraction(1.1) = %+v", got)
	}
}

func TestRenderHumanWholePercentages(t *testing.T) {
	res := []providerResult{{name: "openai", windows: []usageWindow{
		{Name: "session", Duration: 5 * time.Hour, Usage: usageFromFraction(0.286), ResetsAt: testNow.Add(3*time.Hour + 12*time.Minute)},
	}}}
	var b bytes.Buffer
	renderHuman(&b, res, testNow, 0, false)
	if got := b.String(); !strings.Contains(got, "29% usage") || strings.Contains(got, "28.6") {
		t.Errorf("human output = %q, want whole-number 29%% usage", got)
	}
}

func TestRenderHumanNarrowFallback(t *testing.T) {
	res := []providerResult{{name: "openai", windows: []usageWindow{
		{Name: "session", Duration: 5 * time.Hour, Usage: usageFromFraction(0.284), ResetsAt: testNow.Add(3*time.Hour + 12*time.Minute)},
		{Name: "weekly", Duration: 7 * 24 * time.Hour, Usage: usageFromFraction(0.607), ResetsAt: testNow.Add(4*24*time.Hour + 6*time.Hour)},
	}}}
	var b bytes.Buffer
	renderHuman(&b, res, testNow, 0, false)
	wide := b.String()
	if !strings.Contains(wide, "█") {
		t.Errorf("unconstrained output lost bars:\n%s", wide)
	}
	b.Reset()
	renderHuman(&b, res, testNow, utf8.RuneCountInString(strings.TrimSuffix(wide, "\n")), false)
	if got := b.String(); !strings.Contains(got, "█") {
		t.Errorf("output measured in terminal columns lost bars:\n%s", got)
	}
	b.Reset()
	renderHuman(&b, res, testNow, 40, false)
	narrow := b.String()
	if strings.Contains(narrow, "█") {
		t.Errorf("narrow output still has bars:\n%s", narrow)
	}
	if !strings.Contains(narrow, "28% usage") {
		t.Errorf("narrow output lost data:\n%s", narrow)
	}
}

func TestRenderHumanColors(t *testing.T) {
	res := []providerResult{
		{name: "openai", windows: []usageWindow{{Name: "session", Duration: 5 * time.Hour, Usage: usageFromFraction(0.284)}}},
		{name: "ollama", err: errors.New("boom")},
	}
	var b bytes.Buffer
	renderHuman(&b, res, testNow, 0, true)
	if got := b.String(); !strings.Contains(got, "\x1b[") {
		t.Errorf("colored output has no ANSI: %q", got)
	}
	b.Reset()
	renderHuman(&b, res, testNow, 0, false)
	if got := b.String(); strings.Contains(got, "\x1b[") {
		t.Errorf("plain output has ANSI: %q", got)
	}
}

func TestRenderJSONExact(t *testing.T) {
	res := []providerResult{{name: "openai", windows: []usageWindow{
		{Name: "session", Duration: 5 * time.Hour, Usage: usageFromFraction(0.284), ResetsAt: testNow.Add(3*time.Hour + 12*time.Minute)},
	}}}
	var b bytes.Buffer
	if err := renderJSON(&b, res, testNow); err != nil {
		t.Fatal(err)
	}
	var doc jsonReport
	if err := json.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	w := doc.Providers[0].Windows[0]
	if w.UsagePercent != 0.284*100 || w.RemainingAllowancePercent != (1-0.284)*100 {
		t.Errorf("percents = %v/%v", w.UsagePercent, w.RemainingAllowancePercent)
	}
	if w.ResetsAt == nil || !w.ResetsAt.Equal(testNow.Add(3*time.Hour+12*time.Minute)) {
		t.Errorf("resets_at = %v", w.ResetsAt)
	}
	if w.RemainingSeconds == nil || *w.RemainingSeconds != 3*3600+12*60 {
		t.Errorf("remaining_seconds = %v, want 11520", w.RemainingSeconds)
	}
	if w.DurationSeconds != 5*3600 {
		t.Errorf("duration_seconds = %d", w.DurationSeconds)
	}
}

func TestFmtDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0m"},
		{45 * time.Minute, "45m"},
		{5 * time.Hour, "5h"},
		{3*time.Hour + 12*time.Minute, "3h12m"},
		{7 * 24 * time.Hour, "7d"},
		{4*24*time.Hour + 6*time.Hour, "4d6h"},
		{-5 * time.Minute, "0m"},
	}
	for _, c := range cases {
		if got := fmtDuration(c.d); got != c.want {
			t.Errorf("fmtDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
