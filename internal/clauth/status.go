// Package clauth reads the on-disk status feed clauth (a Claude auth/quota
// manager) publishes for other tools to consume, and falls back to invoking
// the clauth CLI directly (`clauth status --json`) when that feed is stale
// or absent. It never runs a clauth subcommand that mutates state (e.g.
// `clauth <profile>`, `clauth start`, `clauth login`) -- only the read-only
// `status --json` invocation.
package clauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// WarnThreshold is the utilization percentage at or past which herdr-draft
// counts a usage window as rate-limited: the popup's account row warns
// there, and the spawn skill tells a spawning agent to raise it in its
// confirmation (#215). One constant, because the popup and the skill giving
// a person two different numbers for "nearly out" would be worse than
// either.
//
// 95, not v2's 100 (v3 spec §10.2): it is clauth's own default auto-switch
// trip point, and at 100 a profile sitting at 98% -- a real, observed live
// value -- warned nowhere, in the popup's row or its panel.
const WarnThreshold = 95.0

// Window is one rate-limit window reported for a profile (e.g. "5h", "7d"),
// mirroring clauth's per-profile `windows[]` entries. ResetsAt is a pointer
// because clauth reports it as JSON null for a window that has no reset
// scheduled (confirmed live: a "Cached"-fetch-status profile's "5h" window
// can carry `"resets_at": null`) -- nil means "no reset time reported",
// distinct from any real, non-zero timestamp.
type Window struct {
	Label          string     `json:"label"`
	UtilizationPct float64    `json:"utilization_pct"`
	ResetsAt       *time.Time `json:"resets_at"`
}

// Profile is one clauth-managed auth profile, as reported by
// `clauth status --json`'s `profiles[]` array. clauth's real payload
// carries more fields (provider, base_url, has_live_session, fetch_status,
// stale, fetched_at, next_refresh_at, auto_start, bell_threshold, fallback,
// third_party); this subset is what herdr-draft consumes today, and
// encoding/json silently ignores the rest. Two of the unmodeled fields
// (fetched_at, next_refresh_at) are timestamps too -- neither was observed
// null in either live capture, but since Profile never unmarshals them at
// all, even if they were null it would pose no parse hazard.
type Profile struct {
	Name       string   `json:"name"`
	Active     bool     `json:"active"`
	Tier       string   `json:"tier"`
	AuthStatus string   `json:"auth_status"`
	Windows    []Window `json:"windows"`
}

// Status is clauth's status feed, as reported by `clauth status --json` or
// mirrored to its on-disk status file.
type Status struct {
	Schema            int       `json:"schema"` // JSON number; verified live: "schema": 1, and 2 from clauth 0.15.2
	ActiveProfile     string    `json:"active_profile"`
	GeneratedAt       time.Time `json:"generated_at"`
	RefreshIntervalMS int       `json:"refresh_interval_ms"`
	Profiles          []Profile `json:"profiles"`

	// Degraded is set when Schema is not one this package was built
	// against (knownSchemas) -- including a payload that omits the schema
	// field entirely, which decodes Schema to its zero value (0). A
	// degraded Status still carries whatever the full parse recovered
	// (which, since clauth versions its schema for backward compatibility
	// with additive changes, is typically the complete Profiles set) --
	// callers should treat any field beyond Profiles[].Name as unreliable
	// and render name-only entries.
	Degraded bool `json:"-"`
}

// knownSchemas are the status schemas this package has read clauth's reason
// for, and found nothing it depends on in: 1, and 2, which clauth 0.15.2
// writes (#238).
//
// Schema 2 renamed one auth_status value, `expiring` to `expired`
// (https://github.com/uwuclxdy/clauth/blob/v0.15.2/src/daemon/status_json.rs#L33-L36),
// and #238 admitted it on the grounds that nothing here keyed on either
// word: every reader asked only whether the status was "ok". Reading it as
// degraded cost the account row every profile's plan, usage windows and
// auth state for nothing, so the full parse was right and stays right.
//
// That GROUND is now gone, and the admission ritual is the poorer for it
// (#243). This package keys on the literal values -- AuthOK, AuthExpired
// and AuthBroken in auth.go, one of which refuses a launch outright -- so a
// bump that renamed `expired` again would be admitted here by a reader who
// checked only the field set, and would silently reclassify a live
// credential. Two clauses, then, not one:
//
//   - Checking field presence and JSON type does not admit a schema, and
//     never did. clauth bumps "ONLY on a breaking change; additive fields do
//     not bump it"
//     (https://github.com/uwuclxdy/clauth/blob/v0.15.2/wiki/Daemon.md#L143),
//     so a bump that keeps every field's type changed some field's MEANING.
//   - And the auth_status VALUES are checked too, against auth.go's
//     constants, at the release tag -- but a schema bump is NOT when to
//     check them. clauth has renamed one of these words once (`expiring`
//     to `expired`, the schema 2 bump) and ADDED one without a bump at all
//     (`unknown`, named as additive under schema 2 in clauth's own
//     evolution rule). The rule is about fields; it has never promised the
//     value set is closed under a schema. So auth.go's unrecognised arm is
//     what actually catches the next one, and this clause is the smaller
//     half: read the values whenever the floor moves, bump or no bump.
//
// A new schema goes here only after reading clauth's stated reason for the
// bump, at the release tag, and confirming both clauses -- and the reason is
// cited here beside the others.
var knownSchemas = map[int]bool{1: true, 2: true}

// minimalStatus is the required subset ParseStatus falls back to when the
// full Status shape fails to parse -- i.e. a future schema changed a field's
// type or structure outright rather than just adding fields.
type minimalStatus struct {
	Profiles []struct {
		Name string `json:"name"`
	} `json:"profiles"`
}

// ParseStatus decodes b -- the verbatim output of `clauth status --json`,
// or the contents of clauth's on-disk status file -- into a Status.
//
// clauth's schema field lets it evolve the payload: ParseStatus first
// attempts a full parse into Status. If that succeeds, Degraded is set
// whenever Schema is not in knownSchemas, but no
// error is returned -- the full parse already recovered everything Status
// models. If the full parse fails outright (a structurally incompatible
// future schema), ParseStatus falls back to decoding only the required
// subset (profiles[].name); if even that fails, ParseStatus returns an
// error.
func ParseStatus(b []byte) (Status, error) {
	var st Status
	if err := json.Unmarshal(b, &st); err == nil {
		if !knownSchemas[st.Schema] {
			st.Degraded = true
		}
		return st, nil
	}

	var minimal minimalStatus
	if err := json.Unmarshal(b, &minimal); err != nil {
		return Status{}, fmt.Errorf("parse clauth status: %w", err)
	}

	degraded := Status{Degraded: true}
	for _, p := range minimal.Profiles {
		degraded.Profiles = append(degraded.Profiles, Profile{Name: p.Name})
	}
	return degraded, nil
}

// LoadOpts configures Load.
type LoadOpts struct {
	// StatusFile is the path to clauth's on-disk status file (typically
	// ~/.clauth/status.json). Load reads it when it exists and is fresh;
	// empty means no status file is available.
	StatusFile string
	// CLIBin is the clauth executable to invoke (e.g. "clauth", or an
	// absolute path) when StatusFile is absent or stale. Empty means no CLI
	// fallback is available.
	CLIBin string
	// Now returns the current time, used to judge StatusFile freshness.
	// Nil means time.Now.
	Now func() time.Time
}

// now returns opts.Now, defaulting to time.Now when unset.
func (opts LoadOpts) now() time.Time {
	if opts.Now != nil {
		return opts.Now()
	}
	return time.Now()
}

// Load returns clauth's current status, preferring StatusFile when it is
// fresh -- generated_at + 2×refresh_interval_ms is after Now() -- and
// falling back to invoking `CLIBin status --json` otherwise. It returns an
// error only when neither source is usable: StatusFile is absent, unreadable,
// unparseable, or stale, and CLIBin is empty or its invocation fails.
func Load(ctx context.Context, opts LoadOpts) (Status, error) {
	now := opts.now()

	if opts.StatusFile != "" {
		if st, ok := loadFreshStatusFile(opts.StatusFile, now); ok {
			return st, nil
		}
	}

	if opts.CLIBin == "" {
		return Status{}, fmt.Errorf("clauth status: no fresh status file at %q and no CLI binary configured", opts.StatusFile)
	}

	cmd := exec.CommandContext(ctx, opts.CLIBin, "status", "--json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// The wrapping keeps exec.ErrNotFound reachable through
		// errors.Is, which the app layer needs: "clauth is not installed"
		// and "clauth is installed and broken" are the same error value
		// here but must produce opposite UI. Not installed is the normal
		// case for most people and shows nothing; broken is worth saying.
		if s := strings.TrimSpace(stderr.String()); s != "" {
			return Status{}, fmt.Errorf("clauth status --json: %w: %s", err, s)
		}
		return Status{}, fmt.Errorf("clauth status --json: %w", err)
	}
	return ParseStatus(stdout.Bytes())
}

// loadFreshStatusFile reads and parses path, returning (Status, true) only
// when it exists, parses successfully, and is fresh as of now. Any failure
// (missing file, read error, parse error, or staleness) returns
// (Status{}, false) so the caller falls back to the CLI.
func loadFreshStatusFile(path string, now time.Time) (Status, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Status{}, false
	}

	st, err := ParseStatus(b)
	if err != nil {
		return Status{}, false
	}

	freshUntil := st.GeneratedAt.Add(2 * time.Duration(st.RefreshIntervalMS) * time.Millisecond)
	if !freshUntil.After(now) {
		return Status{}, false
	}
	return st, true
}
