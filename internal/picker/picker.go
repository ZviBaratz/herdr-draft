// Package picker speaks herdr-draft's ACCOUNT-PICKER PROTOCOL: a contract an
// external executable satisfies, not a particular tool.
//
// The distinction is the whole reason this package exists. herdr-draft is a
// public plugin installed with `herdr plugin install ZviBaratz/herdr-draft`,
// and the picker this protocol was extracted from is not a standalone
// program: it sources a shell framework, a tenant table and a set of zsh
// functions, so naming it would have made a whole private toolchain a hidden
// prerequisite of a public plugin. Documenting the interface instead costs
// one README section and turns an accidental coupling into something anyone
// can implement.
//
// The contract, in full:
//
//	<picker> --dir <path> --json [--strict] [--dry-run]
//
//	stdout  one JSON object with the keys Result models
//	exit    0 picked · 2 refused-exhausted · 3 refused-backpressure
//	        4 bad-table/unsupported/no default · 5 no profiles · 64 usage
//
// --strict asks for headless semantics (no interactive fallback); --dry-run
// asks the picker not to write its ledger and not to build an account
// directory, so config_dir comes back null. Unknown keys are ignored, so a
// picker may say more than the contract asks.
package picker

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// The protocol's exit codes. Anything else is an undocumented failure and is
// reported as one rather than guessed at.
const (
	ExitPicked       = 0
	ExitExhausted    = 2
	ExitBackpressure = 3
	ExitBadTable     = 4
	ExitNoProfiles   = 5
	ExitUsage        = 64
)

// CodeMeaning is the protocol's own English for an exit code -- the fallback
// text a refusal shows when the picker itself supplied no `reason`.
//
// Deliberately phrased for the person reading the account row, not for a
// log: "pool exhausted" is what has happened to them, where "exit 2" is what
// happened to a process they never ran.
func CodeMeaning(code int) string {
	switch code {
	case ExitPicked:
		return "picked"
	case ExitExhausted:
		return "pool exhausted"
	case ExitBackpressure:
		return "machine under load"
	case ExitBadTable:
		return "no account table to pick from"
	case ExitNoProfiles:
		return "no profiles registered"
	case ExitUsage:
		return "picker rejected its own arguments"
	default:
		return fmt.Sprintf("picker exited %d", code)
	}
}

// Usage is the picked profile's rate-limit utilisation. Every field is a
// POINTER because the protocol reports the key on every exit path and nulls
// what it could not determine -- and 0% and "unknown" are opposite facts
// about an account, which a plain float64 would collapse into one.
type Usage struct {
	FiveHour  *float64 `json:"five_hour"`
	Weekly    *float64 `json:"weekly"`
	CacheAgeS *float64 `json:"cache_age_s"`
}

// Resets is when each usage window next resets, as the picker reported it --
// an opaque string, kept verbatim. herdr-draft never parses these: the
// protocol fixes no format, and a picker reporting "14:50" is as conformant
// as one reporting RFC 3339.
type Resets struct {
	FiveHour string `json:"five_hour"`
	Weekly   string `json:"weekly"`
}

// Skipped is one candidate the picker considered and passed over. Only the
// two documented fields are modelled; real pickers carry more.
type Skipped struct {
	Profile string `json:"profile"`
	Why     string `json:"why"`
}

// Machine is the picker's view of the host it would launch on. Pointers for
// the same reason Usage's are.
type Machine struct {
	Load1       *float64 `json:"load1"`
	NCPU        *float64 `json:"ncpu"`
	SwapUsedPct *float64 `json:"swap_used_pct"`
}

// Result is one picker answer. String fields decode empty for a JSON null,
// which is what the protocol sends for anything it could not determine -- so
// "" means "not reported", uniformly, and no caller has to distinguish
// absent from null.
type Result struct {
	Tenant      string `json:"tenant"`
	TenantState string `json:"tenant_state"`
	Profile     string `json:"profile"`
	Tier        string `json:"tier"`
	// ConfigDir is the CLAUDE_CONFIG_DIR the picked profile's credential
	// lives in -- empty under --dry-run by contract, and the only thing that
	// makes `[clauth] launch = "wrapper"` possible (see plan.Build).
	ConfigDir string    `json:"config_dir"`
	Class     string    `json:"class"`
	Reason    string    `json:"reason"`
	Overflow  bool      `json:"overflow"`
	Usage     Usage     `json:"usage"`
	ResetsAt  Resets    `json:"resets_at"`
	Machine   Machine   `json:"machine"`
	Warnings  []string  `json:"warnings"`
	Skipped   []Skipped `json:"skipped"`
}

// RefusalError is a picker that ran, answered, and said no. Code is the
// protocol exit code; Reason is the picker's own `reason` field when it
// supplied one.
//
// A distinct type rather than a formatted string because both callers branch
// on it: internal/app moves focus to the manual account rows and renders
// Short() on the row, and internal/create fails the request BEFORE any
// worktree or pane exists. A refusal is a decision, not a malfunction.
type RefusalError struct {
	Code   int
	Reason string
}

// Short is the one line a row or a stderr progress line shows: the picker's
// own reason when it gave one, else the code's documented meaning.
func (e *RefusalError) Short() string {
	if r := strings.TrimSpace(e.Reason); r != "" {
		return flatten(r)
	}
	return CodeMeaning(e.Code)
}

func (e *RefusalError) Error() string { return "picker refused: " + e.Short() }

// flatten collapses a multi-line reason to one line. A picker's reason may be
// a paragraph -- the real one's bad-table reason is a full sentence with an
// embedded git error -- and every surface this reaches, a one-line form row
// or a one-line-per-step progress log, has exactly one line for it.
func flatten(s string) string { return strings.Join(strings.Fields(s), " ") }

// Decode parses one picker's stdout. It requires a JSON OBJECT: a picker
// invoked without --json prints the bare profile name, and silently accepting
// that would make a mis-wired invocation look like a successful pick with
// every field empty.
func Decode(b []byte) (Result, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		return Result{}, fmt.Errorf("picker output is not a JSON object (did the invocation omit --json?): %w", err)
	}
	var r Result
	if err := json.Unmarshal(b, &r); err != nil {
		return Result{}, fmt.Errorf("picker output: %w", err)
	}
	return r, nil
}

// documentedKeys are the keys a candidate executable must actually emit
// before herdr-draft will trust it (CLI.Probe). Deliberately a SUBSET of what
// Result models: these are the ones a wrong-but-same-named program would not
// have, and requiring the full set would reject a conformant picker that
// omits a key it has nothing to say about.
//
// The usage.* entries are here rather than a bare `usage` because an empty
// object would satisfy the outer key while telling herdr-draft nothing.
var documentedKeys = []string{"profile", "config_dir", "reason", "usage.five_hour", "usage.weekly", "usage.cache_age_s"}

// MissingKeys returns the documented keys b does not contain, sorted. An
// empty return means b is shaped like a picker answer.
//
// Key PRESENCE, not value: every one of these is null on some legitimate exit
// path, so a check on values would reject the picker's own refusal output.
// Presence is what distinguishes "a program that implements this protocol"
// from "a program that happens to print JSON".
func MissingKeys(b []byte) []string {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return append([]string(nil), documentedKeys...)
	}
	var usage map[string]json.RawMessage
	if raw, ok := top["usage"]; ok {
		_ = json.Unmarshal(raw, &usage)
	}

	var missing []string
	for _, key := range documentedKeys {
		name, sub, nested := strings.Cut(key, ".")
		if nested {
			if _, ok := usage[sub]; !ok {
				missing = append(missing, key)
			}
			continue
		}
		if _, ok := top[name]; !ok {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	return missing
}
