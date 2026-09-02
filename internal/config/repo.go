// repo.go loads v2 spec §11's repo-level shared config: a
// `.herdr-draft.toml` committed at the repository root, so a team shares
// creation defaults instead of each person configuring their own machine.
//
// It is a SEPARATE loader from config.toml's, with a separate and much
// smaller schema, because the two files arrive by different routes and
// therefore carry different trust. config.toml is the user's own; this one
// arrives with `git clone`.
//
// Spec §11's trust model, quoted, because it is the requirement rather
// than a guideline:
//
//	A file that arrives with `git clone` may only choose among values the
//	user could already have picked in the form. It may never name a
//	command to run, a path outside the repository, or a credential.
//
// Anything else makes checking out a repository a code-execution vector.
// So the allowed set is five keys (repoAllowedKeys) and EVERY other key in
// the file -- including every key config.toml itself accepts -- is ignored
// and reported (RepoConfig.Notes). Silent omission would be worse than
// useless: someone who commits `[agents.extra_args]`, sees nothing happen
// and gets no explanation will conclude it works.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/ZviBaratz/herdr-draft/internal/gitx"
)

// RepoConfigFileName is spec §11's committed repo-level config. It is read
// from the repository ROOT -- gitx.RepoRoot's answer, which derives from
// `--git-common-dir` rather than `--show-toplevel`, so a linked worktree
// and its origin read one and the same file.
const RepoConfigFileName = ".herdr-draft.toml"

// repoAllowedKeys is spec §11's allow-list, and the single source of truth
// for what a repository may set: it drives BOTH the "is this key ignored?"
// classification below AND value extraction (repoString/repoBool refuse a
// key that is not in here). Nothing is read out of the file by any other
// route -- there is deliberately no struct with `toml` tags for this file,
// so adding a field to RepoConfig accepts nothing on its own.
//
// Every key here is top-level and scalar. That is not an accident: a table
// is where the dangerous keys live ([agents.extra_args], [linear],
// [clauth]), and keeping the allowed surface flat means a table header in
// the file is always, by construction, something to reject.
//
// CHANGING THIS SET IS A TRUST-BOUNDARY CHANGE. Read spec §11 first, and
// see TestRepoAllowedKeysIsExactlyTheSpecList, which pins it.
var repoAllowedKeys = map[string]bool{
	"branch_prefix":      true,
	"default_worktree":   true,
	"default_placement":  true,
	"default_base":       true,
	"linear_branch_name": true,
}

// repoDeniedKey is one entry on the explicit deny list: a key (or whole
// table) spec §11 names as forbidden, with the short reason the note
// carries. The reason is the point -- "ignored" alone teaches nobody where
// the trust boundary is.
type repoDeniedKey struct {
	// key is a dotted path, matched exactly or as a table prefix, so
	// "clauth" covers "clauth.enabled" and "agents.extra_args" covers
	// "agents.extra_args.claude" -- one note per forbidden thing rather
	// than one per leaf.
	key    string
	reason string
}

// repoDeniedKeys is spec §11's forbidden list, in the spec's own order.
//
// It is NOT what makes a key be ignored -- repoAllowedKeys is, and
// anything absent from it is ignored whether or not it appears here. This
// list exists for two other jobs: to give each forbidden key the specific
// reason its note should say, and to be checked against repoAllowedKeys at
// package init (see the init below), so a future contributor cannot
// quietly promote one of these to allowed.
var repoDeniedKeys = []repoDeniedKey{
	{"agents.extra_args", "it becomes part of a launched agent's command line"},
	{"agents.favorites", "a repository does not choose which agent runs on your machine"},
	{"agents.default", "a repository does not choose which agent runs on your machine"},
	{"linear.prompt_template", "it would become the agent's first instruction"},
	{"linear.api_key", "it is a credential"},
	{"linear.api_key_cmd", "it is a credential, and a command to run"},
	{"clauth", "a repository does not configure your clauth accounts"},
	{"timeouts", "a repository does not set your timeouts"},
	{"palette", "a repository does not set your colors"},
}

// init enforces the one invariant that keeps the two lists honest: nothing
// spec §11 forbids may appear on the allow-list, as itself or as a child of
// a forbidden table.
//
// This is the guard against the realistic future mistake -- a contributor
// who wants `prompt_template` to work adds it to repoAllowedKeys and
// writes an extraction for it, and every existing test still passes
// because none of them says anything about a key that used to be
// impossible. With this, that change panics on the first test run instead,
// and the panic names the key.
func init() {
	for _, d := range repoDeniedKeys {
		if repoAllowedKeys[d.key] {
			panic("config: " + RepoConfigFileName + " key " + d.key +
				" is both allowed and forbidden -- see spec §11's trust model")
		}
		for k := range repoAllowedKeys {
			if strings.HasPrefix(k, d.key+".") {
				panic("config: " + RepoConfigFileName + " key " + k +
					" is allowed but sits inside the forbidden table " + d.key +
					" -- see spec §11's trust model")
			}
		}
	}
}

// RepoConfig is one loaded .herdr-draft.toml: the handful of values a
// repository is allowed to choose, plus the visible report of everything
// in the file that was ignored.
//
// The pointer fields distinguish "the file omits this key" (nil, so the
// tier below applies) from a value that happens to be false -- the same
// reason State.LastWorktree and ProjectDefaults.Worktree are pointers.
// The string fields use "" for the same purpose, which is
// defaults.Resolve's own convention: a consequence is that a repo config
// cannot express "no branch prefix" or "base = HEAD" by writing an empty
// string, only by omitting the key and letting the tiers below decide.
//
// The zero value is a valid "no repo config": every field unset, no notes.
type RepoConfig struct {
	// BranchPrefix is spec §11's `branch_prefix`, ALREADY validated:
	// LoadRepoConfig runs it through gitx.ValidateBranchPrefix and drops a
	// rejected one to "" with the reason on Notes, so what reaches
	// defaults.Resolve is either usable or absent. Dropping to "" is what
	// makes the fallback land on the USER's own configured prefix (the
	// next tier down) rather than on the built-in default -- config.Load's
	// own rejected-prefix fallback, which is a different question with a
	// different answer.
	BranchPrefix string
	// DefaultWorktree is spec §11's `default_worktree`.
	DefaultWorktree *bool
	// DefaultPlacement is spec §11's `default_placement`, in config.toml's
	// own vocabulary ("new-space"/"tab-here"/"split-here"). An
	// unrecognized value is not rejected here: defaults.ParsePlacement
	// already treats one as "this tier supplies nothing".
	DefaultPlacement string
	// DefaultBase is spec §11's `default_base`: the default base ref for a
	// worktree branch. It reaches `herdr worktree create --base <value>`
	// as argv, which is a `git clone`-delivered value meeting a flag
	// parser -- closed at the runner boundary (internal/herdrc's argv
	// hardening), not here, so that one rule covers every flag rather than
	// one validator per field.
	DefaultBase string
	// LinearBranchName is spec §11's `linear_branch_name`: whether a
	// chosen Linear issue's own branchName owns the branch. nil (the key
	// is absent) leaves the built-in true.
	LinearBranchName *bool

	// Notes is the visible report: one short line per thing in the file
	// that was ignored, and why. Empty when the file was absent, or when
	// everything in it was allowed.
	//
	// This is the same degrade-with-a-reason shape the rest of the plugin
	// uses (Config.BranchPrefixWarning, app.Setup.LinearUnavailable): the
	// form still opens, the allowed keys still apply, and the reason
	// travels with the value instead of vanishing.
	Notes []string
}

// LoadRepoConfig reads <repoRoot>/.herdr-draft.toml and returns what a
// repository is allowed to choose, plus a note for everything else the
// file contained.
//
// It never fails. An empty repoRoot (the project is not a repository, or
// its root could not be resolved), a missing file, an unreadable one and
// an unparseable one all yield a RepoConfig that supplies no values --
// with a note for the last two, since those are a file someone wrote and
// expects to work. A malformed file must never block the form: that is the
// established degrade-with-a-reason behavior, and this file in particular
// is one a teammate can break for everyone at once.
//
// Nothing here is cached. The caller re-reads on every project change (see
// internal/app's debounced dir check), because which repository the form
// points at is exactly what decides which file this is.
func LoadRepoConfig(repoRoot string) RepoConfig {
	if repoRoot == "" {
		return RepoConfig{}
	}

	path := filepath.Join(repoRoot, RepoConfigFileName)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return RepoConfig{}
		}
		return RepoConfig{Notes: []string{fmt.Sprintf("could not read %s: %v", RepoConfigFileName, err)}}
	}

	// Decoded into a plain map, not into a struct with `toml` tags, and
	// deliberately: the map is both the key census (through the returned
	// MetaData) and the only value source, so the allow-list below is the
	// single gate everything passes through. A struct target would give a
	// second, silent one.
	var raw map[string]any
	md, derr := toml.Decode(string(b), &raw)
	if derr != nil {
		return RepoConfig{Notes: []string{fmt.Sprintf("ignoring %s: %v", RepoConfigFileName, derr)}}
	}

	notes := rejectedKeyNotes(md)

	rc := RepoConfig{
		BranchPrefix:     repoString(raw, "branch_prefix", &notes),
		DefaultWorktree:  repoBool(raw, "default_worktree", &notes),
		DefaultPlacement: repoString(raw, "default_placement", &notes),
		DefaultBase:      repoString(raw, "default_base", &notes),
		LinearBranchName: repoBool(raw, "linear_branch_name", &notes),
	}

	// The prefix is prepended raw by gitx.BranchSlug and the result becomes
	// the argv element after `herdr worktree create --branch`, so it is
	// validated at the point it is first trusted -- the same rule
	// config.Load applies to the user's own, by the same free function, and
	// the reason that function is not welded to either loader.
	//
	// The FALLBACK is what differs: config.Load falls back to the built-in
	// default, because there is nothing under it; here the value is simply
	// dropped, which lands on the user's own configured prefix one tier
	// down. A repository that writes an unusable prefix should not be able
	// to reach past the user's own choice to the built-in.
	if verr := gitx.ValidateBranchPrefix(rc.BranchPrefix); verr != nil {
		notes = append(notes, fmt.Sprintf("ignoring branch_prefix %q: %v; using your own configured prefix",
			rc.BranchPrefix, verr))
		rc.BranchPrefix = ""
	}

	rc.Notes = notes
	return rc
}

// rejectedKeyNotes walks every key the file actually contains and reports
// the ones a repository may not set -- which is every key not in
// repoAllowedKeys, whether or not repoDeniedKeys anticipated it.
//
// Two shaping rules keep the report to one line per thing the writer did:
//
//   - A forbidden TABLE absorbs its own contents. `[clauth]` plus
//     `clauth.enabled` is one note about `clauth`, not two, because the
//     table is the decision and the keys under it are its details.
//   - A table header that is NOT itself forbidden and has children says
//     nothing on its own; the children speak. So `[agents]` with
//     `favorites` under it reports `agents.favorites`, which is the key
//     the writer typed, rather than a vague `agents`.
func rejectedKeyNotes(md toml.MetaData) []string {
	keys := md.Keys()
	var notes []string
	reported := map[string]bool{}
	add := func(key, reason string) {
		if reported[key] {
			return
		}
		reported[key] = true
		notes = append(notes, fmt.Sprintf("ignoring %s: %s", key, reason))
	}

	for _, k := range keys {
		path := k.String()
		if repoAllowedKeys[path] {
			continue
		}
		if d, ok := deniedFor(path); ok {
			add(d.key, d.reason)
			continue
		}
		if md.Type(k...) == "Hash" && hasChildKey(keys, path) {
			continue
		}
		add(path, "not a key a repository may set")
	}
	return notes
}

// deniedFor finds the deny-list entry covering path -- an exact match, or
// the LONGEST entry path sits inside as a table. Longest wins so
// "agents.extra_args.claude" is attributed to "agents.extra_args" even if
// a broader "agents" entry were ever added.
func deniedFor(path string) (repoDeniedKey, bool) {
	var best repoDeniedKey
	found := false
	for _, d := range repoDeniedKeys {
		if path != d.key && !strings.HasPrefix(path, d.key+".") {
			continue
		}
		if !found || len(d.key) > len(best.key) {
			best, found = d, true
		}
	}
	return best, found
}

// hasChildKey reports whether any key in keys sits under path as a table
// entry.
func hasChildKey(keys []toml.Key, path string) bool {
	for _, k := range keys {
		if strings.HasPrefix(k.String(), path+".") {
			return true
		}
	}
	return false
}

// repoString reads an allowed top-level string key. A key of the wrong
// TOML type is ignored with its own note rather than silently becoming a
// zero value: a repository that writes `default_placement = 3` has made a
// mistake worth naming.
func repoString(raw map[string]any, key string, notes *[]string) string {
	v, ok := lookupAllowed(raw, key)
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		*notes = append(*notes, fmt.Sprintf("ignoring %s: expected a string", key))
		return ""
	}
	return s
}

// repoBool reads an allowed top-level boolean key, returning nil for an
// absent (or wrongly typed) one so the tier below still applies.
func repoBool(raw map[string]any, key string, notes *[]string) *bool {
	v, ok := lookupAllowed(raw, key)
	if !ok {
		return nil
	}
	b, ok := v.(bool)
	if !ok {
		*notes = append(*notes, fmt.Sprintf("ignoring %s: expected true or false", key))
		return nil
	}
	return &b
}

// lookupAllowed is the single read path out of a decoded .herdr-draft.toml,
// and it panics for a key that is not on the allow-list.
//
// That panic is the second half of the fail-closed design (init is the
// first): extraction is impossible without an allow-list entry, and an
// allow-list entry for anything spec §11 forbids panics at init. A
// contributor therefore cannot make a forbidden key take effect by editing
// one place, and neither edit can be silent.
func lookupAllowed(raw map[string]any, key string) (any, bool) {
	if !repoAllowedKeys[key] {
		panic("config: " + RepoConfigFileName + " key " + key +
			" is read but not on the allow-list -- see spec §11's trust model")
	}
	v, ok := raw[key]
	return v, ok
}
