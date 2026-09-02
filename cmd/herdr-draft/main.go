// Command herdr-draft is the plugin binary spec §5 describes: herdr
// launches it in a popup pane with $HERDR_PLUGIN_CONTEXT_JSON/
// $HERDR_PLUGIN_CONFIG_DIR/$HERDR_PLUGIN_STATE_DIR/$HERDR_BIN_PATH set in
// its environment. This replaces Task 2's smoke binary; internal/app.
// Bootstrap owns spec §9's pre-open refusal and every other piece of
// startup work -- this file is deliberately thin: read the plugin
// environment, construct the production Deps, hand both to
// app.Bootstrap, and run the resulting tea.Program.
//
// v2 spec §13 adds a second entry point on the same binary: with no
// arguments this is the popup, exactly as before, and `herdr-draft create`
// is the headless path. Dispatch is the only thing this file knows about
// it -- internal/create owns the verb, so it can be tested without
// executing a binary.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/app"
	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/create"
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

// usage is the whole binary's own help: two entry points, one line each.
// The create verb's own flags live in internal/create, printed by
// `herdr-draft create --help`.
const usage = `herdr-draft -- herdr's new-session plugin

usage:
  herdr-draft                 open the new-session popup (how herdr launches it)
  herdr-draft create [flags]  create a session without the popup
  herdr-draft help            print this

run "herdr-draft create --help" for the create flags.
`

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
	os.Exit(dispatch(os.Args[1:], os.Stdout, os.Stderr, runPopup, runCreate))
}

// dispatch routes a command line to its verb and returns the process exit
// code (v2 spec §13: "main.go dispatches on os.Args[1]: absent means the
// popup, exactly as today; an unknown verb prints usage and exits 2").
//
// popup and createVerb are passed in rather than called directly so this
// -- the one piece of routing logic in package main -- is testable without
// starting a tea.Program or talking to herdr.
//
// `help`/`-h`/`--help` are not "unknown verbs": they print the usage on
// stdout and exit 0, the way asking a program for its help always should,
// and the way Go's own flag package treats -h. Only a verb this binary
// does not have exits 2.
func dispatch(args []string, stdout, stderr io.Writer, popup func() int, createVerb func(args []string) int) int {
	if len(args) == 0 {
		return popup()
	}
	switch args[0] {
	case "create":
		return createVerb(args[1:])
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "herdr-draft: unknown command %q\n\n", args[0])
		fmt.Fprint(stderr, usage)
		return 2
	}
}

// herdrRunner builds the one production herdrc.Runner both entry points
// use, from $HERDR_BIN_PATH.
func herdrRunner() *herdrc.CLIRunner {
	bin := os.Getenv("HERDR_BIN_PATH")
	if bin == "" {
		bin = defaultHerdrBin
	}
	return &herdrc.CLIRunner{Bin: bin}
}

// runPopup is the original entry point, unchanged: spec §5's plugin
// invocation.
func runPopup() int {
	env := app.Env{
		ContextJSON: os.Getenv("HERDR_PLUGIN_CONTEXT_JSON"),
		ConfigDir:   os.Getenv("HERDR_PLUGIN_CONFIG_DIR"),
		StateDir:    os.Getenv("HERDR_PLUGIN_STATE_DIR"),
	}

	clauthSrc := app.NewClauthSource(clauth.LoadOpts{
		StatusFile: clauthStatusFilePath(),
		CLIBin:     defaultClauthBin,
	})
	gitSrc := app.NewGitSource()

	model, err := app.Bootstrap(env, herdrRunner(), clauthSrc, gitSrc, app.Clock{})
	if err != nil {
		// spec §9's pre-open refusal: plain-text error to stderr, exit 1,
		// before the form ever renders.
		fmt.Fprintln(os.Stderr, "herdr-draft:", err)
		return 1
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
		return 1
	}
	return 0
}

// runCreate is spec §13's headless verb. The plugin context is read here
// exactly as runPopup reads it -- it is normally absent for this path,
// which is why the three per-pane variables are read alongside it.
func runCreate(args []string) int {
	return create.Run(context.Background(), args, create.Env{
		ConfigDir:   os.Getenv("HERDR_PLUGIN_CONFIG_DIR"),
		StateDir:    os.Getenv("HERDR_PLUGIN_STATE_DIR"),
		ContextJSON: os.Getenv("HERDR_PLUGIN_CONTEXT_JSON"),
		PluginID:    os.Getenv("HERDR_PLUGIN_ID"),
		WorkspaceID: os.Getenv("HERDR_WORKSPACE_ID"),
		TabID:       os.Getenv("HERDR_TAB_ID"),
		PaneID:      os.Getenv("HERDR_PANE_ID"),
	}, create.Deps{
		Runner: herdrRunner(),
		Git:    app.NewGitSource(),
	})
}
