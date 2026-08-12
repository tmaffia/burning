package main

import (
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiGreen  = "\x1b[32m"
	ansiGray   = "\x1b[90m"
)

const barCells = 10

// renderHuman writes one dense line per provider, e.g.
//
//	OpenAI  5h ▕███░░░░░░░▏ 28% used · 3h12m │ 7d ▕██████░░░░▏ 61% used · 4d6h
//
// Lines wider than the terminal drop their bars; width <= 0 is unconstrained.
func renderHuman(w io.Writer, results []providerResult, now time.Time, width int, color bool) {
	if len(results) == 0 {
		fmt.Fprintln(w, "No providers configured.")
		return
	}
	for _, r := range results {
		switch {
		case r.err != nil:
			line := fmt.Sprintf("%-8s error: %s", r.name, r.err.Error())
			if color {
				line = ansiRed + line + ansiReset
			}
			fmt.Fprintln(w, line)
		case len(r.windows) == 0:
			fmt.Fprintf(w, "%-8s no usage windows\n", r.name)
		default:
			fmt.Fprintln(w, buildLine(r, now, width, color))
		}
	}
}

func buildLine(r providerResult, now time.Time, width int, color bool) string {
	showBars := true
	if width > 0 {
		parts := make([]string, len(r.windows))
		for i, win := range r.windows {
			parts[i] = windowText(win, now, false, true)
		}
		if len(fmt.Sprintf("%-8s %s", r.name, strings.Join(parts, " │ "))) > width {
			showBars = false
		}
	}
	parts := make([]string, len(r.windows))
	for i, win := range r.windows {
		parts[i] = windowText(win, now, color, showBars)
	}
	return fmt.Sprintf("%-8s %s", r.name, strings.Join(parts, " │ "))
}

func windowText(win usageWindow, now time.Time, color, showBar bool) string {
	used := clamp01(win.Used)
	pct := int(math.Round(used * 100))
	dur := fmtDuration(win.Duration)
	core := fmt.Sprintf("%s %3d%% used", dur, pct)
	if showBar {
		core = fmt.Sprintf("%s %s %3d%% used", dur, bar(used), pct)
	}
	if color {
		c := ansiGreen
		switch {
		case used >= 0.9:
			c = ansiRed
		case used >= 0.7:
			c = ansiYellow
		}
		core = c + core + ansiReset
	}
	if win.ResetsAt.IsZero() {
		return core
	}
	cd := " · " + fmtDuration(win.ResetsAt.Sub(now))
	if color {
		cd = ansiGray + cd + ansiReset
	}
	return core + cd
}

// bar renders a barCells-wide usage bar; 0% used is empty, 100% used is full.
func bar(used float64) string {
	filled := int(math.Round(clamp01(used) * barCells))
	return "▕" + strings.Repeat("█", filled) + strings.Repeat("░", barCells-filled) + "▏"
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// fmtDuration renders a duration densely: 45m, 5h, 3h12m, 4d6h.
func fmtDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d += 30 * time.Second // round to the nearest minute
	days, hours, mins := int(d.Hours())/24, int(d.Hours())%24, int(d.Minutes())%60
	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case hours > 0 && mins > 0:
		return fmt.Sprintf("%dh%dm", hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}
