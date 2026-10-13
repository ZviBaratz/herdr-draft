package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/ZviBaratz/herdr-draft/internal/pathx"
)

// LocalMachine is the selector for this machine, always: no saved herdr
// machine can take the name (machine spec §4.1).
const LocalMachine = "local"

// MachineConfig is the optional `[machine]` table (machine spec §5.1): which
// herdr machine a session opens on when nothing else says.
//
// config.toml-only, and never remembered (§2, decisions 1 and 2). A
// repository cannot set it (repo.go's repoDeniedKeys), and neither memory
// file stores it, so a one-off `local` for a browser session never becomes
// the next session's default.
type MachineConfig struct {
	// Configured is whether the file has a [machine] table at all, even an
	// empty one. The popup's machine row exists only then (§9.1), which is
	// what keeps the plugin unchanged for anyone who has not opted in.
	Configured bool `toml:"-"`
	// Default is the selector a project no rule matches opens on. "" is
	// the builtin, local.
	Default string `toml:"default"`
	// Paths is [machine.paths] as written: directory to selector. Load
	// turns it into Rules and nothing else reads it.
	Paths map[string]string `toml:"paths"`
	// Rules is Paths made usable: each directory expanded and canonicalised
	// the way the project key it is matched against is (pathx.CanonicalKey),
	// sorted by directory. defaults.Machine matches them.
	Rules []MachineRule `toml:"-"`
	// Warnings is one line per rule Load could not use, and why. The entry
	// is dropped; the reason is not.
	Warnings []string `toml:"-"`
}

// MachineRule is one [machine.paths] entry: the projects at or under Dir
// open on Selector.
type MachineRule struct {
	Dir      string
	Selector string
}

// Offered is the selectors a person can choose (machine spec §4.2): local
// first, then every machine the table names, once each, default before the
// rules. The popup's row offers exactly these and `create --machine`
// accepts exactly these, because every create flag has a form control
// behind it. A saved machine the table never names -- a production host,
// say -- is in neither.
func (m MachineConfig) Offered() []string {
	out := []string{LocalMachine}
	add := func(sel string) {
		if sel != "" && !slices.Contains(out, sel) {
			out = append(out, sel)
		}
	}
	add(m.Default)
	for _, r := range m.Rules {
		add(r.Selector)
	}
	return out
}

// loadMachineRules turns [machine.paths] into Rules, reporting every entry
// it cannot use rather than dropping it silently: a relative directory (it
// would match relative to wherever the plugin happened to start), an empty
// selector, and a second spelling of a directory already ruled on. Keys are
// read in sorted order so which of two duplicates wins is not a map's
// choice.
//
// The directories are canonicalised here, once, because the project key
// they are matched against is (v2 spec §10's pathx.CanonicalKey): a rule
// written through a symlink must still match the project it points at.
// That is a stat per rule, at the same moment config.toml itself is read.
func loadMachineRules(paths map[string]string) (rules []MachineRule, warnings []string) {
	keys := make([]string, 0, len(paths))
	for k := range paths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	seen := map[string]string{}
	for _, k := range keys {
		sel := strings.TrimSpace(paths[k])
		expanded := pathx.ExpandTilde(k)
		switch {
		case !filepath.IsAbs(expanded):
			warnings = append(warnings, fmt.Sprintf("ignoring [machine.paths] %q: not an absolute path (write it from ~ or /)", k))
			continue
		case sel == "":
			warnings = append(warnings, fmt.Sprintf("ignoring [machine.paths] %q: it names no machine", k))
			continue
		}
		dir := pathx.CanonicalKey(expanded)
		if first, dup := seen[dir]; dup {
			warnings = append(warnings, fmt.Sprintf("ignoring [machine.paths] %q: the same directory as %q", k, first))
			continue
		}
		seen[dir] = k
		rules = append(rules, MachineRule{Dir: dir, Selector: sel})
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Dir < rules[j].Dir })
	return rules, warnings
}
