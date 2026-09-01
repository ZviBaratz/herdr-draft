// Command herdr-draft is the plugin binary spec §5 describes: herdr
// launches it in a popup pane with $HERDR_PLUGIN_CONTEXT_JSON/
// $HERDR_PLUGIN_CONFIG_DIR/$HERDR_PLUGIN_STATE_DIR/$HERDR_BIN_PATH set in
// its environment. This replaces Task 2's smoke binary; internal/app.
// Bootstrap owns spec §9's pre-open refusal and every other piece of
// startup work -- this file is deliberately thin: read the plugin
// environment, construct the production Deps, hand both to
// app.Bootstrap, and run the resulting tea.Program.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/app"
	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

// defaultHerdrBin/defaultClauthBin are the PATH-relative fallbacks used
// when $HERDR_BIN_PATH is unset (herdr documents it as always set for a
// launched plugin, but falling back rather than refusing on an unset
// value is cheap insurance) or clauth has no configured location at all
// (spec §12 has no config key for clauth's own binary path).
const (
	defaultHerdrBin  = "herdr"
	defaultClauthBin = "clauth"
)

// clauthStatusFilePath resolves clauth's own on-disk status feed path
// (spec §11: "prefer reading the daemon's ~/.clauth/status.json when
// fresh"), or "" when the home directory can't be determined -- clauth.Load
// falls back to invoking the CLI directly when StatusFile is "".
func clauthStatusFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".clauth", "status.json")
}

func main() {
	env := app.Env{
		ContextJSON: os.Getenv("HERDR_PLUGIN_CONTEXT_JSON"),
		ConfigDir:   os.Getenv("HERDR_PLUGIN_CONFIG_DIR"),
		StateDir:    os.Getenv("HERDR_PLUGIN_STATE_DIR"),
	}

	herdrBin := os.Getenv("HERDR_BIN_PATH")
	if herdrBin == "" {
		herdrBin = defaultHerdrBin
	}
	runner := &herdrc.CLIRunner{Bin: herdrBin}

	clauthSrc := app.NewClauthSource(clauth.LoadOpts{
		StatusFile: clauthStatusFilePath(),
		CLIBin:     defaultClauthBin,
	})
	gitSrc := app.NewGitSource()

	model, err := app.Bootstrap(env, runner, clauthSrc, gitSrc, app.Clock{})
	if err != nil {
		// spec §9's pre-open refusal: plain-text error to stderr, exit 1,
		// before the form ever renders.
		fmt.Fprintln(os.Stderr, "herdr-draft:", err)
		os.Exit(1)
	}

	// bubbletea v2.0.8 has no tea.WithAltScreen()/mouse-enabling
	// tea.NewProgram option at all (verified against
	// charm.land/bubbletea/v2@v2.0.8's options.go: WithContext/WithOutput/
	// WithInput/WithEnvironment/WithoutSignalHandler/WithoutCatchPanics/
	// WithoutSignals/WithoutRenderer/WithFilter/WithFPS/WithColorProfile/
	// WithWindowSize is the complete list) -- AltScreen and MouseMode are
	// both set on the tea.View app.Model.View() returns instead; see its
	// own doc comment.
	if _, err := tea.NewProgram(model).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "herdr-draft:", err)
		os.Exit(1)
	}
}
