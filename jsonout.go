package main

import (
	"encoding/json"
	"io"
	"time"
)

// schemaVersion identifies the JSON report format.
const schemaVersion = "burning.usage.v1"

type jsonWindow struct {
	Name                      string     `json:"name"`
	DurationSeconds           int64      `json:"duration_seconds"`
	UsagePercent              float64    `json:"usage_percent"`
	RemainingAllowancePercent float64    `json:"remaining_allowance_percent"`
	ResetsAt                  *time.Time `json:"resets_at,omitempty"`
	RemainingSeconds          *int64     `json:"remaining_seconds,omitempty"`
}

type jsonProvider struct {
	Name    string       `json:"name"`
	Windows []jsonWindow `json:"windows"`
}

type jsonError struct {
	Provider string `json:"provider"`
	Message  string `json:"message"`
}

type jsonReport struct {
	Schema      string         `json:"schema"`
	GeneratedAt time.Time      `json:"generated_at"`
	Providers   []jsonProvider `json:"providers"`
	Errors      []jsonError    `json:"errors,omitempty"`
}

// renderJSON writes the schema-v1 report: Usage and Remaining Allowance
// percentages at full normalized precision, structured errors, and no ANSI.
func renderJSON(w io.Writer, results []providerResult, now time.Time) error {
	doc := jsonReport{Schema: schemaVersion, GeneratedAt: now.UTC(), Providers: []jsonProvider{}}
	for _, r := range results {
		if r.err != nil {
			doc.Errors = append(doc.Errors, jsonError{Provider: r.name, Message: r.err.Error()})
			continue
		}
		p := jsonProvider{Name: r.name, Windows: []jsonWindow{}}
		for _, win := range r.windows {
			jw := jsonWindow{
				Name:                      win.Name,
				DurationSeconds:           int64(win.Duration / time.Second),
				UsagePercent:              win.Usage.percent(),
				RemainingAllowancePercent: win.Usage.remainingAllowancePercent(),
			}
			if !win.ResetsAt.IsZero() {
				t := win.ResetsAt.UTC()
				jw.ResetsAt = &t
				rem := int64(win.ResetsAt.Sub(now) / time.Second)
				jw.RemainingSeconds = &rem
			}
			p.Windows = append(p.Windows, jw)
		}
		doc.Providers = append(doc.Providers, p)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
