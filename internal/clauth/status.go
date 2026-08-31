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

// Window is one rate-limit window reported for a profile (e.g. "5h", "7d"),
// mirroring clauth's per-profile `windows[]` entries.
type Window struct {
	Label          string    `json:"label"`
	UtilizationPct float64   `json:"utilization_pct"`
	ResetsAt       time.Time `json:"resets_at"`
}

// Profile is one clauth-managed auth profile, as reported by
// `clauth status --json`'s `profiles[]` array. clauth's real payload
// carries more fields (provider, base_url, has_live_session, fetch_status,
// stale, fetched_at, next_refresh_at, auto_start, bell_threshold, fallback,
// third_party); this subset is what herdr-draft consumes today, and
// encoding/json silently ignores the rest.
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
	Schema            int       `json:"schema"` // JSON number; verified live: "schema": 1
	ActiveProfile     string    `json:"active_profile"`
	GeneratedAt       time.Time `json:"generated_at"`
	RefreshIntervalMS int       `json:"refresh_interval_ms"`
	Profiles          []Profile `json:"profiles"`

	// Degraded is set when Schema is not the schema this package was built
	// against (1). A degraded Status still carries whatever the full parse
	// recovered (which, since clauth versions its schema for backward
	// compatibility with additive changes, is typically the complete
	// Profiles set) -- callers should treat any field beyond Profiles[].Name
	// as unreliable and render name-only entries.
	Degraded bool `json:"-"`
}

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
// whenever Schema != 1 (the schema this package was built against), but no
// error is returned -- the full parse already recovered everything Status
// models. If the full parse fails outright (a structurally incompatible
// future schema), ParseStatus falls back to decoding only the required
// subset (profiles[].name); if even that fails, ParseStatus returns an
// error.
func ParseStatus(b []byte) (Status, error) {
	var st Status
	if err := json.Unmarshal(b, &st); err == nil {
		if st.Schema != 1 {
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
		return Status{}, fmt.Errorf("clauth status --json: %w: %s", err, strings.TrimSpace(stderr.String()))
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
