// flags.go parses `herdr-draft create`'s command line -- spec §13's flag
// list, which mirrors the form's own fields one for one.
package create

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/ZviBaratz/herdr-draft/internal/defaults"
)

// errHelpRequested marks an explicit `-h`/`--help`, which is not a usage
// error: the usage goes to stdout and the process exits 0, the way Go's
// own flag package treats it. Only an UNKNOWN verb or a malformed flag
// exits 2 (spec §13).
var errHelpRequested = flag.ErrHelp

// onFailureKeep/onFailureClean are `--on-failure`'s two values. Keep is
// the default: a session that half-exists is evidence, and removing it is
// the choice that cannot be undone.
const (
	onFailureKeep  = "keep"
	onFailureClean = "clean"
)

// promptStdin is the `--prompt` value that reads the prompt from stdin
// (spec §13). A literal "-" is the established shell convention and the
// only value a prompt could not otherwise be, since a real prompt
// beginning with "-" is written by piping it in.
const promptStdin = "-"

// request is one parsed command line: the values, plus which flags were
// actually given.
//
// `set` is what makes "the resolver decides" separable from "the caller
// chose the zero value" for every flag whose zero is a legal answer --
// --base "" (HEAD) and --account "" (the active account) both mean
// something. Without it, an explicit empty flag would silently fall back
// to a remembered value the caller was trying to clear.
type request struct {
	project   string
	title     string
	prompt    string
	branch    string
	base      string
	placement string
	agent     string
	account   string
	issue     string
	onFailure string
	json      bool

	// worktree is the --worktree/--no-worktree pair as one tri-state: nil
	// when neither was given, so spec §10's resolved default stands.
	worktree *bool

	set map[string]bool
}

// flagNames is every flag this command accepts, in the order the usage
// text lists them. It exists so usage_test.go can assert the two stay in
// step -- a hand-written usage block is worth its clarity only if it
// cannot silently omit a flag.
var flagNames = []string{
	"project", "title", "prompt", "branch", "base",
	"worktree", "no-worktree", "placement", "agent", "account",
	"issue", "json", "on-failure",
}

// createUsage is `herdr-draft create --help`. Hand-written rather than
// flag.PrintDefaults': the --worktree/--no-worktree pair is one decision
// with two spellings, and the default column is a lie for every flag whose
// real default comes from spec §10's resolver rather than from this file.
const createUsage = `usage: herdr-draft create [flags]

Create a herdr session without opening the popup. Every flag left unset
resolves the way the form resolves it: projects.json, then the
repository's .herdr-draft.toml, then last-used.json, then config.toml,
then the built-in default.

flags:
  --project DIR      project directory (default: the working directory)
  --title TEXT       session title; required unless --issue supplies one
  --prompt TEXT      initial prompt; "-" reads it from stdin
  --branch NAME      worktree branch (default: derived from the title)
  --base REF         worktree base ref (default: HEAD)
  --worktree         create a git worktree
  --no-worktree      do not create a worktree
  --placement WHERE  new-space | tab-here | split-here (ignored with a worktree)
  --agent KIND       agent kind to start, e.g. claude
  --account NAME     clauth account to pin (claude only)
  --issue ID         seed title, branch and prompt from a Linear issue
  --json             print one JSON object instead of a human line
  --on-failure WHAT  keep | clean -- what to do with a session that failed
                     partway (default: keep)

exit codes:
  0  created
  1  the plan started and failed (--on-failure applied)
  2  bad usage, or a request that cannot be resolved
  3  herdr is unreachable

tab-here and split-here need herdr's own pane environment
(HERDR_WORKSPACE_ID / HERDR_TAB_ID / HERDR_PANE_ID), which herdr sets for
every pane. A new space or a worktree needs none of it.
`

// parseArgs parses the arguments following the `create` verb. Its output
// writer is discarded and its Usage suppressed: this package prints both
// the error and the usage itself (see Run), so the flag package's own
// half-formatted version never doubles up on it.
func parseArgs(args []string) (request, error) {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	var req request
	var worktreeOn, worktreeOff bool

	fs.StringVar(&req.project, "project", "", "")
	fs.StringVar(&req.title, "title", "", "")
	fs.StringVar(&req.prompt, "prompt", "", "")
	fs.StringVar(&req.branch, "branch", "", "")
	fs.StringVar(&req.base, "base", "", "")
	fs.BoolVar(&worktreeOn, "worktree", false, "")
	fs.BoolVar(&worktreeOff, "no-worktree", false, "")
	fs.StringVar(&req.placement, "placement", "", "")
	fs.StringVar(&req.agent, "agent", "", "")
	fs.StringVar(&req.account, "account", "", "")
	fs.StringVar(&req.issue, "issue", "", "")
	fs.BoolVar(&req.json, "json", false, "")
	fs.StringVar(&req.onFailure, "on-failure", onFailureKeep, "")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return request{}, errHelpRequested
		}
		return request{}, err
	}
	if fs.NArg() > 0 {
		return request{}, fmt.Errorf("unexpected argument %q -- create takes flags only", fs.Arg(0))
	}

	req.set = map[string]bool{}
	fs.Visit(func(f *flag.Flag) { req.set[f.Name] = true })

	if req.set["worktree"] && req.set["no-worktree"] {
		return request{}, errors.New("--worktree and --no-worktree contradict each other")
	}
	switch {
	case req.set["worktree"]:
		v := worktreeOn
		req.worktree = &v
	case req.set["no-worktree"]:
		// --no-worktree is the negative spelling, so its own value is
		// inverted: --no-worktree=false is a request FOR a worktree, which
		// is the only reading that keeps the two flags each other's
		// mirror.
		v := !worktreeOff
		req.worktree = &v
	}

	if err := req.validate(); err != nil {
		return request{}, err
	}
	return req, nil
}

// validate checks the values this file can judge on its own -- the two
// closed vocabularies. Everything else (an unknown agent kind, a title
// that never materialized, a placement whose context is missing) needs the
// loaded configuration and is checked in resolve.go.
func (r request) validate() error {
	if r.set["placement"] {
		if _, ok := defaults.ParsePlacement(r.placement); !ok {
			return fmt.Errorf("unknown --placement %q: expected new-space, tab-here or split-here", r.placement)
		}
	}
	switch r.onFailure {
	case onFailureKeep, onFailureClean:
	default:
		return fmt.Errorf("unknown --on-failure %q: expected keep or clean", r.onFailure)
	}
	return nil
}

// readPrompt resolves --prompt, reading stdin for the "-" form. It returns
// (text, true) only when --prompt was actually given: an absent flag has
// to stay distinct from an empty one so a Linear issue's own seeded prompt
// can still apply.
func readPrompt(r request, stdin io.Reader) (string, bool, error) {
	if !r.set["prompt"] {
		return "", false, nil
	}
	if r.prompt != promptStdin {
		return r.prompt, true, nil
	}
	b, err := io.ReadAll(stdin)
	if err != nil {
		return "", false, fmt.Errorf("reading the prompt from stdin: %w", err)
	}
	// A trailing newline is an artifact of how the text was piped in, not
	// part of the prompt; interior whitespace is left exactly as written.
	return strings.TrimRight(string(b), "\n"), true, nil
}
