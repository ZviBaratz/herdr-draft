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
	// "/"`).
	BranchPrefix     string `toml:"branch_prefix"`
	DefaultWorktree  bool   `toml:"default_worktree"`
	DefaultPlacement string `toml:"default_placement"`

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

// defaultBranchPrefix returns the lowercased current OS user's username
// plus "/", or "user/" when the current user can't be determined (e.g. no
// passwd entry in a minimal container).
func defaultBranchPrefix() string {
	u, err := user.Current()
	if err != nil || u.Username == "" {
		return "user/"
	}
	return strings.ToLower(u.Username) + "/"
}

// Load reads $configDir/config.toml and returns the resulting Config, with
// this package's defaults filled in for every key the file omits. A
// missing config file is not an error -- it returns all defaults. Unknown
// keys in the file are ignored rather than rejected (no strict/disallow-
// unknown-fields mode), so an older herdr-draft binary tolerates a newer
// config file.
func Load(configDir string) (Config, error) {
	cfg := defaults()

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
	return cfg, nil
}
