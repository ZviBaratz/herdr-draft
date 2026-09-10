// Package config loads herdr-draft's plugin configuration and persists its
// small amount of loss-tolerant UI state, per spec §12.
//
// Config is read from $HERDR_PLUGIN_CONFIG_DIR/config.toml. Every key is
// optional -- a missing file, or a file that omits some keys, falls back to
// this package's defaults for whatever it doesn't specify. Unknown keys
// (typos, future additions read by a newer herdr-draft) are silently
// ignored rather than treated as a parse error, so an older binary never
// breaks on a newer config file.
package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/ZviBaratz/herdr-draft/internal/gitx"
)

// configFileName is the config file's name inside $HERDR_PLUGIN_CONFIG_DIR
// (spec §12: "$HERDR_PLUGIN_CONFIG_DIR/config.toml").
const configFileName = "config.toml"

// LinearConfig is the optional `[linear]` table (spec §12).
type LinearConfig struct {
	// APIKeyCmd is a command (argv, no shell) whose stdout is the Linear
	// API key, e.g. ["pass", "show", "linear-api-key"].
	APIKeyCmd []string `toml:"api_key_cmd"`
	// APIKey is the Linear API key given directly in the config file.
	// Spec §12 discourages this in favor of APIKeyCmd, and the file's
	// permissions must be checked (0600) before this key is trusted --
	// that check is the loader's caller's responsibility, not this
	// package's.
	APIKey string `toml:"api_key"`
	// PromptTemplate overrides the built-in commit/PR prompt template
	// (spec §10); empty means "use the built-in default".
	PromptTemplate string `toml:"prompt_template"`
}

// Launch modes for `[clauth] launch`.
//
// ClauthLaunchStart is the DEFAULT, and that is a decision rather than an
// ordering: `clauth start <profile> --` means the same thing on every machine
// that has clauth. The wrapper mode types a bare `claude` into the pane and
// depends on the user's login shell resolving it to a function of their own
// -- where such a function exists it can register a session holder and launch
// through a team-lead helper, and where it does not (which is everywhere by
// default) `claude` is the plain binary: the credential is still isolated by
// CLAUDE_CONFIG_DIR, but everything the wrapper was for is silently gone. A
// default that quietly means something weaker on every machine but its
// author's is the wrong default.
const (
	ClauthLaunchStart   = "start"
	ClauthLaunchWrapper = "wrapper"
)

// ClauthConfig is the optional `[clauth]` table (spec §12).
type ClauthConfig struct {
	// Enabled is a pointer because omitting this key means "auto-detect",
	// which is distinct from explicitly disabling clauth integration.
	Enabled *bool  `toml:"enabled"`
	Default string `toml:"default"`

	// Picker names an executable implementing herdr-draft's account-picker
	// protocol (see internal/picker) -- a command resolved on PATH, or an
	// absolute path. Empty means no picker, which is the default and the
	// normal case.
	//
	// It must be NAMED. herdr-draft never goes looking for one, because
	// finding a program with the expected name is not the same as finding the
	// program that was meant, and a plugin that silently adopted a same-named
	// stranger to route account credentials through would be worse than a
	// plugin with no picker at all. Even a named one is probed once before it
	// is trusted (app.Bootstrap).
	Picker string `toml:"picker"`

	// Launch selects how a pinned account is launched: ClauthLaunchStart (the
	// default) or ClauthLaunchWrapper. See the constants.
	//
	// An unrecognised value falls back to the default with the reason on
	// Config.ClauthLaunchWarning; it never makes Load fail.
	Launch string `toml:"launch"`
}

// AgentsConfig is the optional `[agents]` table (spec §12).
type AgentsConfig struct {
	Favorites []string `toml:"favorites"`
	Default   string   `toml:"default"`
	// ExtraArgs is the optional `[agents.extra_args]` sub-table: extra CLI
	// args per agent kind, keyed by agent name.
	ExtraArgs map[string][]string `toml:"extra_args"`
}

// WorktreeConfig is the optional `[worktree]` table.
//
// It is deliberately a table of its own rather than another top-level key
// beside default_worktree: every key here is a herdr worktree-creation
// FLAG, not a form default, and `.herdr-draft.toml`'s allow-list is flat
// by construction so that "a table header in the file is always, by
// construction, something to reject" (repo.go). Putting trust_repository
// in a table therefore makes it unreachable from a cloned repository by
// the same mechanism that already rejects [clauth] and [palette], instead
// of relying on someone remembering to keep it off the allow-list.
type WorktreeConfig struct {
	// TrustRepository adds `--trust-repository` to `herdr worktree
	// create`, which trusts a verified repository for that ONE request
	// rather than changing global git configuration -- herdr 0.9.0
	// (#3044). Absent means false: this relaxes a git safety check, so it
	// is opt-in, and the user opting in is the whole point of the key.
	//
	// A POINTER, unlike default_worktree beside it, because nil and false
	// must stay distinguishable. defaults() gives default_worktree a real
	// `true`, so config.toml genuinely supplies one for every file and
	// attributing it to config.toml is honest. Nothing supplies this key,
	// so a plain bool would make "the file never mentioned it" and "the
	// file set it to false" the same value -- and the resolver would then
	// report `from config.toml` for a file with no [worktree] table at
	// all. Provenance the user can see has to be earned, not assumed.
	//
	// A repository can never set this. See repo.go's repoDeniedKeys.
	TrustRepository *bool `toml:"trust_repository"`
}

// TimeoutsConfig is the optional `[timeouts]` table (spec §12).
type TimeoutsConfig struct {
	DetectionMS  int `toml:"detection_ms"`
	PromptWaitMS int `toml:"prompt_wait_ms"`
	// TrustWaitMS is how long the popup waits for a PERSON to answer a
	// blocking dialog that stopped the launch -- Claude Code's first-run
	// trust prompt, in practice, which every worktree hits the first time
	// (#115).
	//
	// A separate number from DetectionMS on purpose, and not a bigger
	// DetectionMS: that one is tuned for a machine painting a status
	// transition in tens of seconds, and raising it to a human's timescale
	// would make every ordinary detection failure take five minutes to
	// report. Only the popup consults it; headless `create` has nobody at
	// the keyboard and keeps failing fast.
	TrustWaitMS int `toml:"trust_wait_ms"`
}

// Config is herdr-draft's plugin configuration, mirroring spec §12's
// config.toml example field-for-field.
type Config struct {
	// BranchPrefix defaults to the lowercased current user's username plus
	// "/" (spec §12's config.toml comment: `default: lowercased $USER +
	// "/"`). A value Load reads from the file is always one
	// gitx.ValidateBranchPrefix accepted -- see BranchPrefixWarning.
	BranchPrefix     string `toml:"branch_prefix"`
	DefaultWorktree  bool   `toml:"default_worktree"`
	DefaultPlacement string `toml:"default_placement"`

	// BranchPrefixWarning is non-empty when the file's own branch_prefix
	// was rejected by gitx.ValidateBranchPrefix and BranchPrefix therefore
	// holds the built-in default instead: it is the short reason, naming
	// both the rejected value and its replacement. Never decoded from the
	// file (`toml:"-"`) -- it is Load's own output.
	//
	// This is the degrade-with-a-reason shape the rest of the plugin uses
	// for an optional input it cannot trust (app.Setup.LinearUnavailable is
	// the other): the plugin still opens, the feature still works off the
	// default, and the reason travels with the value instead of vanishing.
	// A bad branch_prefix is a typo, not a reason to refuse startup.
	BranchPrefixWarning string `toml:"-"`

	// ClauthLaunchWarning is non-empty when `[clauth] launch` named a mode
	// this binary does not know and ClauthLaunchStart was used instead: the
	// short reason, naming both. Never decoded from the file -- Load's own
	// output, in the same degrade-with-a-reason shape BranchPrefixWarning
	// documents above.
	ClauthLaunchWarning string `toml:"-"`

	Linear   LinearConfig   `toml:"linear"`
	Clauth   ClauthConfig   `toml:"clauth"`
	Agents   AgentsConfig   `toml:"agents"`
	Timeouts TimeoutsConfig `toml:"timeouts"`
	Worktree WorktreeConfig `toml:"worktree"`

	// Palette is the optional `[palette]` table: an escape hatch for
	// overriding herdr theme colors when detection is wrong (spec §7,
	// §12). Nil when the config file omits the table entirely.
	Palette map[string]string `toml:"palette"`
}

// defaults returns Config's built-in defaults (spec §12), used for any key
// a config.toml omits -- including the case where the file itself is
// missing entirely.
func defaults() Config {
	return Config{
		BranchPrefix:     defaultBranchPrefix(),
		DefaultWorktree:  true,
		DefaultPlacement: "new-space",
		Clauth:           ClauthConfig{Launch: ClauthLaunchStart},
		Agents: AgentsConfig{
			Favorites: []string{"claude"},
		},
		Timeouts: TimeoutsConfig{
			DetectionMS:  30000,
			PromptWaitMS: 120000,
			TrustWaitMS:  300000,
		},
	}
}

// fallbackBranchPrefix is the last-resort prefix: used when the current OS
// user can't be determined, and when their username makes a prefix even
// gitx.ValidateBranchPrefix won't take.
const fallbackBranchPrefix = "user/"

// defaultBranchPrefix returns the lowercased current OS user's username
// plus "/", or "user/" when the current user can't be determined (e.g. no
// passwd entry in a minimal container).
//
// The username is run through gitx.ValidateBranchPrefix too, because it is
// not guaranteed to be ref-safe either -- a Windows "DOMAIN\user" is the
// obvious case, and this value is the very thing Load falls back TO, so it
// had better not be the one broken prefix nothing checks. Ordinary
// usernames containing "." or "-" pass unchanged.
func defaultBranchPrefix() string {
	u, err := user.Current()
	if err != nil || u.Username == "" {
		return fallbackBranchPrefix
	}
	prefix := strings.ToLower(u.Username) + "/"
	if gitx.ValidateBranchPrefix(prefix) != nil {
		return fallbackBranchPrefix
	}
	return prefix
}

// Load reads $configDir/config.toml and returns the resulting Config, with
// this package's defaults filled in for every key the file omits. A
// missing config file is not an error -- it returns all defaults. Unknown
// keys in the file are ignored rather than rejected (no strict/disallow-
// unknown-fields mode), so an older herdr-draft binary tolerates a newer
// config file.
//
// An unusable branch_prefix falls back to the default the same way an
// omitted one does, with the reason left on BranchPrefixWarning; it never
// makes Load fail. Only a file that cannot be read or parsed does, which
// is what app.Bootstrap turns into spec §9's pre-open refusal.
func Load(configDir string) (Config, error) {
	cfg := defaults()
	defaultPrefix := cfg.BranchPrefix

	// An empty configDir means THERE IS NO CONFIG DIRECTORY -- never the
	// caller's working directory. filepath.Join("", "config.toml") is
	// "config.toml", a relative path, so without this the plugin would
	// read whatever config.toml happens to sit in the directory it was
	// started from: some other project's, parsed as this plugin's, with a
	// parse failure in it refusing to open at all.
	//
	// herdr exports HERDR_PLUGIN_CONFIG_DIR only to a launched PLUGIN, so
	// an unset one is the normal case for anything else -- `just smoke`,
	// the headless verb outside a plugin pane, and a curious person
	// running the binary by hand. This is also the defaults-only entry
	// point this function used to lack, which internal/create had to fake
	// with an empty temporary directory.
	if configDir == "" {
		return cfg, nil
	}

	path := filepath.Join(configDir, configFileName)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("load config: read %s: %w", path, err)
	}

	if _, err := toml.Decode(string(b), &cfg); err != nil {
		return Config{}, fmt.Errorf("load config: parse %s: %w", path, err)
	}

	// The prefix reaches `herdr worktree create --branch <value>` as an
	// argv element by way of gitx.BranchSlug, so it is validated at the
	// point it is first trusted rather than at the point it is used. The
	// same gitx.ValidateBranchPrefix call is what a repo-supplied
	// .herdr-draft.toml will need; only the fallback differs there.
	if verr := gitx.ValidateBranchPrefix(cfg.BranchPrefix); verr != nil {
		cfg.BranchPrefixWarning = fmt.Sprintf("ignoring branch_prefix %q: %v; using %q",
			cfg.BranchPrefix, verr, defaultPrefix)
		cfg.BranchPrefix = defaultPrefix
	}

	// `[clauth] launch` is validated here, at the point it is first trusted,
	// for the same reason branch_prefix is: it decides what gets TYPED into a
	// pane's shell, and an unknown mode must resolve to the conservative one
	// rather than to whatever the zero value happens to be. An empty value is
	// the file omitting the key -- toml.Decode leaves a key the file does not
	// mention untouched, so this arm also covers a [clauth] table with no
	// launch in it -- not an error.
	switch cfg.Clauth.Launch {
	case "":
		cfg.Clauth.Launch = ClauthLaunchStart
	case ClauthLaunchStart, ClauthLaunchWrapper:
	default:
		cfg.ClauthLaunchWarning = fmt.Sprintf("ignoring [clauth] launch %q: expected %q or %q; using %q",
			cfg.Clauth.Launch, ClauthLaunchStart, ClauthLaunchWrapper, ClauthLaunchStart)
		cfg.Clauth.Launch = ClauthLaunchStart
	}
	return cfg, nil
}
