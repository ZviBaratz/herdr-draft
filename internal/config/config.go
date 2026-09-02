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

// ClauthConfig is the optional `[clauth]` table (spec §12).
type ClauthConfig struct {
	// Enabled is a pointer because omitting this key means "auto-detect",
	// which is distinct from explicitly disabling clauth integration.
	Enabled *bool  `toml:"enabled"`
	Default string `toml:"default"`
}

// AgentsConfig is the optional `[agents]` table (spec §12).
type AgentsConfig struct {
	Favorites []string `toml:"favorites"`
	Default   string   `toml:"default"`
	// ExtraArgs is the optional `[agents.extra_args]` sub-table: extra CLI
	// args per agent kind, keyed by agent name.
	ExtraArgs map[string][]string `toml:"extra_args"`
}

// TimeoutsConfig is the optional `[timeouts]` table (spec §12).
type TimeoutsConfig struct {
	DetectionMS  int `toml:"detection_ms"`
	PromptWaitMS int `toml:"prompt_wait_ms"`
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

	Linear   LinearConfig   `toml:"linear"`
	Clauth   ClauthConfig   `toml:"clauth"`
	Agents   AgentsConfig   `toml:"agents"`
	Timeouts TimeoutsConfig `toml:"timeouts"`

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
		Agents: AgentsConfig{
			Favorites: []string{"claude"},
		},
		Timeouts: TimeoutsConfig{
			DetectionMS:  30000,
			PromptWaitMS: 120000,
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
	return cfg, nil
}
