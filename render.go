package main

import (
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiGreen  = "\x1b[32m"
	ansiGray   = "\x1b[90m"
)

const barCells = 10

// Column widths for windowText; fixed so fields line up across providers.
const (
	colDur = 6 // "13h27m"
	colPct = 4 // "100%"
	colCd  = 6 // "23h59m"
)

// colWidth is the width of one window column including the bar.
const colWidth = colDur + 1 + barCells + 2 + 1 + colPct + 1 + colCd

// renderHuman writes one dense line per provider, e.g.
//
//	ollama  45m ▕░░░░░░░░░░▏   4%      · 7d ▕██░░░░░░░░▏  19%
//	openai                     · 7d ▕██████░░░░▏  61%  6d12h
//	claude   5h ▕██░░░░░░░░▏  20%  7h10m · 7d ▕█░░░░░░░░░▏   8%  5d10h
//
// Windows share columns by position (session under session, weekly under
// weekly); a missing window or reset leaves a gap. Lines wider than the
// terminal drop their bars; width <= 0 is unconstrained.
func renderHuman(w io.Writer, results []providerResult, now time.Time, width int, color bool) {
	if len(results) == 0 {
		fmt.Fprintln(w, "No providers configured.")
		return
	}
	cols := 0
	for _, r := range results {
		for _, w := range r.windows {
			if i := windowCol(w); i+1 > cols {
				cols = i + 1
			}
		}
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
			fmt.Fprintln(w, buildLine(r, now, width, color, cols))
		}
	}
}

func buildLine(r providerResult, now time.Time, width int, color bool, cols int) string {
	showBars := true
	if width > 0 {
		if utf8.RuneCountInString(line(r, now, cols, false, true)) > width {
			showBars = false
		}
	}
	return line(r, now, cols, color, showBars)
}

// windowCol is the column a window belongs to; providers report session
// and weekly windows by name.
func windowCol(win usageWindow) int {
	switch win.Name {
	case "weekly":
		return 1
	default: // "session" and unknown names
		return 0
	}
}

func line(r providerResult, now time.Time, cols int, color, showBars bool) string {
	parts := make([]string, cols)
	blank := strings.Repeat(" ", colWidth)
	if !showBars {
		blank = strings.Repeat(" ", colDur+1+colPct+1+colCd)
	}
	for i := range parts {
		parts[i] = blank
	}
	for _, win := range r.windows {
		parts[windowCol(win)] = windowText(win, now, color, showBars)
	}
	last := len(parts) - 1
	for last > 0 && strings.TrimSpace(parts[last]) == "" {
		last--
	}
	return strings.TrimRight(fmt.Sprintf("%-8s %s", r.name, strings.Join(parts[:last+1], " · ")), " ")
}

func windowText(win usageWindow, now time.Time, color, showBar bool) string {
	pct := int(math.Round(win.Usage.percent()))
	head := fmt.Sprintf("%*s", colDur, fmtDuration(win.Duration))
	if showBar {
		head += " " + bar(win.Usage)
	}
	head += fmt.Sprintf(" %*d%%", colPct-1, pct)
	cd := ""
	if !win.ResetsAt.IsZero() {
		cd = fmtDuration(win.ResetsAt.Sub(now))
	}
	if color {
		c := ansiGreen
		switch {
		case win.Usage.fraction >= 0.9:
			c = ansiRed
		case win.Usage.fraction >= 0.7:
			c = ansiYellow
		}
		return c + head + ansiReset + ansiGray + fmt.Sprintf(" %*s", colCd, cd) + ansiReset
	}
	return head + fmt.Sprintf(" %*s", colCd, cd)
}

// bar renders a barCells-wide usage bar; 0% usage is empty, 100% is full.
func bar(u usage) string {
	filled := int(math.Round(u.fraction * barCells))
	return "▕" + strings.Repeat("█", filled) + strings.Repeat("░", barCells-filled) + "▏"
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
