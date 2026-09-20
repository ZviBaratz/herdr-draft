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
	"runtime/debug"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/app"
	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/create"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/skill"
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

// usage is the whole binary's own help: one line per entry point. The
// create verb's own flags live in internal/create, printed by
// `herdr-draft create --help`.
const usage = `herdr-draft -- herdr's new-session plugin

usage:
  herdr-draft                 open the new-session popup (how herdr launches it)
  herdr-draft create [flags]  create a session without the popup
  herdr-draft skill           print the agent skill; see README
  herdr-draft version         print the version
  herdr-draft help            print this

run "herdr-draft create --help" for the create flags.
`

// build is the `git describe` of the tree this binary was compiled from,
// stamped by `just build`:
//
//	go build -ldflags "-X main.build=$(git describe --tags --always --dirty)"
//
// It is a SEPARATE fact from the version, not an override of it, which is
// the distinction that makes both honest. The version is what
// herdr-plugin.toml declares and what herdr displays for the install; the
// build is which commit produced this particular binary. Collapsing them
// meant that before any tag existed `git describe --always` fell back to a
// bare hash and the version line stopped naming a version at all.
//
// Empty by default, and normally empty for an installed plugin: herdr's
// own `[[build]]` runs a plain argv with no shell, so there is nowhere to
// run `git describe`. That is fine -- an install is exactly the case where
// the manifest version is the whole truth.
var build = ""

// versionString is what `herdr-draft version` prints: the version first,
// then the two facts a bug report needs and nobody can reconstruct from
// it -- which plugin id this binary answers to (they are installable side
// by side, under different ids, from forks) and what it was built with.
//
// The commit line appears only when the toolchain actually stamped one.
// It does not always: a build from a git WORKTREE produces no vcs.*
// settings at all, silently, even with -buildvcs=true -- which is how
// this repository is developed, so a version command that promised a
// commit would be empty exactly where it is most used.
func versionString() string {
	var b strings.Builder
	fmt.Fprintf(&b, "herdr-draft %s\n", herdrc.Version)
	fmt.Fprintf(&b, "  plugin id  %s\n", herdrc.PluginID)
	if build != "" {
		fmt.Fprintf(&b, "  build      %s\n", build)
	}

	if bi, ok := debug.ReadBuildInfo(); ok {
		var rev, modified string
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value
			}
		}
		if rev != "" {
			if len(rev) > 12 {
				rev = rev[:12]
			}
			if modified == "true" {
				rev += " (modified)"
			}
			fmt.Fprintf(&b, "  commit     %s\n", rev)
		}
		fmt.Fprintf(&b, "  built with %s\n", bi.GoVersion)
	}
	return b.String()
}

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
	os.Exit(dispatch(os.Args[1:], os.Stdout, os.Stderr, runPopup, runCreate,
		func() int { return runSkill(os.Stdout, os.Stderr) }))
}

// dispatch routes a command line to its verb and returns the process exit
// code (v2 spec §13: "main.go dispatches on os.Args[1]: absent means the
// popup, exactly as today; an unknown verb prints usage and exits 2").
//
// popup, createVerb and skillVerb are passed in rather than called
// directly so this -- the one piece of routing logic in package main -- is
// testable without starting a tea.Program or talking to herdr.
//
// `help`/`-h`/`--help` are not "unknown verbs": they print the usage on
// stdout and exit 0, the way asking a program for its help always should,
// and the way Go's own flag package treats -h. Only a verb this binary
// does not have exits 2.
func dispatch(args []string, stdout, stderr io.Writer, popup func() int, createVerb func(args []string) int, skillVerb func() int) int {
	if len(args) == 0 {
		return popup()
	}
	switch args[0] {
	case "create":
		return createVerb(args[1:])
	case "skill":
		return skillVerb()
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	// Both spellings, for the same reason help takes three: asking a
	// program what it is should not require guessing which convention its
	// author picked.
	case "version", "--version", "-V":
		fmt.Fprint(stdout, versionString())
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

	// The popup's own Lifetime: the context the form's off-model work runs
	// on, and the thing runProgram tears down on the way out (#211).
	lt := app.NewLifetime()

	model, err := app.Bootstrap(env, herdrRunner(), clauthSrc, gitSrc, app.Clock{}, lt)
	if err != nil {
		// spec §9's pre-open refusal: plain-text error to stderr, exit 1,
		// before the form ever renders.
		fmt.Fprintln(os.Stderr, "herdr-draft:", err)
		return 1
	}

	return runProgram(os.Stderr, lt, func() error {
		// bubbletea v2.0.8 has no tea.WithAltScreen()/mouse-enabling
		// tea.NewProgram option at all (verified against
		// charm.land/bubbletea/v2@v2.0.8's options.go: WithContext/WithOutput/
		// WithInput/WithEnvironment/WithoutSignalHandler/WithoutCatchPanics/
		// WithoutSignals/WithoutRenderer/WithFilter/WithFPS/WithColorProfile/
		// WithWindowSize is the complete list) -- AltScreen and MouseMode are
		// both set on the tea.View app.Model.View() returns instead; see its
		// own doc comment.
		_, err := tea.NewProgram(model).Run()
		return err
	})
}

// shutdownGrace is how long the process stays alive, after the form is gone,
// for the cancellation of its own background work to be delivered (#211).
//
// It is sized for the KILL, not for the call that is being killed. Measured
// on 2026-09-20 over twenty runs against a `#!/bin/sh` + `sleep` stub: from
// cancel to a dead picker, 1.4ms worst case; from cancel to exec.Cmd.Wait
// returning, 2.05s, because the picker's orphaned `sleep` inherits the stdout
// pipe and holds Wait open for the whole of picker.pickerWaitDelay. A real
// picker shells out per account, so that slow shape is the ordinary one --
// and waiting it out would hold the popup on screen for two seconds after the
// form is gone, waiting on a process that has already been dead for all but
// the first millisecond of it. A quarter of a second is two orders of
// magnitude above what the kill needs and imperceptible when it is spent.
const shutdownGrace = 250 * time.Millisecond

// runProgram runs the popup and then, ALWAYS, tears down the work it started:
// cancel, then wait (bounded) for the cancellation to land before this returns
// and the process exits.
//
// The wait is the half that is easy to leave out and does not work without --
// see app.Lifetime.Shutdown. It happens before the error is reported, and on
// the failure path too, because a popup that died still must not leave a
// picker behind to write a ledger entry for the session it did not create.
//
// run and the writer are parameters so this is testable without a real
// tea.Program and a real terminal, the way dispatch and runSkill are.
func runProgram(stderr io.Writer, lt *app.Lifetime, run func() error) int {
	err := run()
	lt.Shutdown(shutdownGrace)
	if err != nil {
		fmt.Fprintln(stderr, "herdr-draft:", err)
		return 1
	}
	return 0
}

// runSkill is the production `skill` verb: the real executable path and
// the real version. Writers are parameters rather than os.Stdout/os.Stderr
// captured inside, so TestRunSkillStampsTheBuildersVersion can pin the one
// thing skill.Run itself cannot -- that this call site passes
// herdrc.Version and not some other string.
func runSkill(stdout, stderr io.Writer) int {
	return skill.Run(stdout, stderr, os.Executable, herdrc.Version)
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
		// The popup's own loader, so a profile the account row would mark
		// signed out is refused here too (#147).
		Clauth: app.NewClauthSource(clauth.LoadOpts{
			StatusFile: clauthStatusFilePath(),
			CLIBin:     defaultClauthBin,
		}),
	})
}
