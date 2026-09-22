// flags.go parses `herdr-draft create`'s command line -- v2 spec §13's flag
// list, which mirrors the form's own fields one for one.
package create

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/ZviBaratz/herdr-draft/internal/agentopts"
	"github.com/ZviBaratz/herdr-draft/internal/defaults"
)

// errHelpRequested marks an explicit `-h`/`--help`, which is not a usage
// error: the usage goes to stdout and the process exits 0, the way Go's
// own flag package treats it. Only an UNKNOWN verb or a malformed flag
// exits 2 (v2 spec §13).
var errHelpRequested = flag.ErrHelp

// onFailureKeep/onFailureClean are `--on-failure`'s two values. Keep is
// the default: a session that half-exists is evidence, and removing it is
// the choice that cannot be undone.
const (
	onFailureKeep  = "keep"
	onFailureClean = "clean"
)

// promptStdin is the `--prompt` value that reads the prompt from stdin
// (v2 spec §13). A literal "-" is the established shell convention and the
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
	workspace string
	agent     string
	account   string
	issue     string
	onFailure string
	json      bool
	// dryRun is --dry-run (#209): the whole pre-flight, then a report of
	// what would be created, and nothing else.
	dryRun bool

	// worktree is the --worktree/--no-worktree pair as one tri-state: nil
	// when neither was given, so v2 spec §10's resolved default stands.
	worktree *bool

	// reap is --reap/--no-reap as one tri-state, the worktree pair's shape
	// (reap spec §7.1): nil when neither was given, so the resolved
	// [reaper] mark_ready stands. reapOn/reapOff are the two raw flag
	// values it is computed from; they live here rather than as
	// registerFlags parameters so that function's signature -- which
	// spawnskill_contract_test.go calls -- stays put.
	reap            *bool
	reapOn, reapOff bool

	// options holds one value per agent session option any kind declares,
	// keyed by option name (agentopts.Names) -- the storage its flag
	// (agentopts.FlagName) is bound to. Whether a flag was given is set's
	// answer, as for every other flag: `--effort inherit` is a request, and
	// an absent --effort is not.
	options map[string]*string

	set map[string]bool
}

// createUsage is `herdr-draft create --help`. Hand-written rather than
// flag.PrintDefaults': the --worktree/--no-worktree pair is one decision
// with two spellings, and the default column is a lie for every flag whose
// real default comes from v2 spec §10's resolver rather than from this file.
const createUsage = `usage: herdr-draft create [flags]

Create a herdr session without opening the popup. Every flag left unset
resolves the way the form resolves it: projects.json, then the
repository's .herdr-draft.toml, then last-used.json, then config.toml,
then the built-in default.

flags:
  --project DIR      project directory (default: the working directory)
  --title TEXT       session title; required unless --issue supplies one.
                     Either is kept to what the form's title row holds:
                     a tab, CR or LF becomes a space, other control
                     characters and invalid UTF-8 are dropped, and it is
                     cut to 32 runes
  --prompt TEXT      initial prompt; "-" reads it from stdin. It, or the
                     one --issue seeds, keeps tabs and line breaks (a
                     CRLF or a lone CR becomes one line break); other
                     control characters and invalid UTF-8 are dropped
  --branch NAME      worktree branch (default: derived from the title)
  --base REF         worktree base ref (default: HEAD). Inside a linked
                     worktree it is resolved there, so HEAD is the commit
                     that worktree is on. With a worktree, a REF that
                     names no commit is refused
  --worktree         create a git worktree
  --no-worktree      do not create a worktree
  --reap             end the prompt with pane-reaper's instruction, so the
                     agent marks its own pane ready to close when finished
                     (default: off, or [reaper] mark_ready)
  --no-reap          do not, even if [reaper] mark_ready is on
  --placement WHERE  new-space | tab-here | split-here | tab-in; where a
                     session without a worktree lands. tab-in is a tab in
                     the workspace already holding the project's checkout,
                     and is the default whenever one is open. A worktree
                     session runs in the worktree's own space, so with a
                     worktree only new-space is accepted
  --workspace ID     the workspace tab-in opens its tab in, instead of the
                     one holding the project (implies --placement tab-in;
                     needs --no-worktree)
  --agent KIND       agent kind to start, e.g. claude
  --model NAME       the agent's model: an alias (fable, opus, sonnet,
                     haiku) or a full id such as claude-opus-5[1m]
  --effort LEVEL     low | medium | high | xhigh | max
  --permission-mode MODE
                     manual | plan | acceptEdits | auto
                     These three are claude's session options. Unset, each
                     comes from config.toml's [agents.options.<kind>], else
                     whatever [agents.extra_args] passes, else the agent's
                     own settings. "inherit" sends no flag of its own, even
                     when config.toml sets one, though [agents.extra_args]
                     can still pass one; --json's launch_options says what
                     the agent runs with. A kind that does not declare an
                     option refuses its flag
  --account NAME     clauth account to pin (claude only); "auto" asks the
                     configured [clauth] picker to choose
  --issue ID         seed title, branch and prompt from a Linear issue
  --json             print one JSON object instead of a human line
  --dry-run          resolve and check everything a create would, report
                     what it would create, and stop: nothing is created
                     or remembered, and --account auto asks the picker
                     without spending a pick
  --on-failure WHAT  keep | clean -- what to do with a session that failed
                     partway (default: keep)

exit codes:
  0  created, or with --dry-run, would create
  1  the plan started and failed, and part of the session may exist
     (--on-failure applied)
  2  bad usage, or a request that cannot be resolved
  3  herdr is unreachable, found before the plan starts
  4  the plan started, and its first step failed before making anything:
     nothing exists
  5  a question the command asked did not answer in time -- a stalled
     filesystem, or Linear; nothing was created and nothing in the
     command would change it

tab-here and split-here need herdr's own pane environment
(HERDR_WORKSPACE_ID / HERDR_TAB_ID / HERDR_PANE_ID), which herdr sets for
every pane. new-space and tab-in need none of it, and neither does a
worktree session, which always runs in its own space.
`

// registerFlags builds create's flag set, binding every flag to the
// caller's own storage. Split out of parseArgs so that the set of flags
// this command accepts is a value a test can ENUMERATE rather than a list
// it restates: `fs.VisitAll` over this is the real answer to "what does
// create accept", where createUsage's prose and create_test.go's flagNames
// were each a hand-maintained copy a new flag could be added without.
//
// That gap used to be recorded as deliberate beside flagNames, on the
// grounds that splitting the construction out for a test's benefit was not
// worth it. What changed is what depends on the answer: the spawn skill's
// document is held to this set from another package
// (spawnskill_contract_test.go), and a flag the document never names is a
// flag an agent never uses -- so "what does create accept" stopped being a
// question only this package asks itself.
func registerFlags(fs *flag.FlagSet, req *request, worktreeOn, worktreeOff *bool) {
	fs.StringVar(&req.project, "project", "", "")
	fs.StringVar(&req.title, "title", "", "")
	fs.StringVar(&req.prompt, "prompt", "", "")
	fs.StringVar(&req.branch, "branch", "", "")
	fs.StringVar(&req.base, "base", "", "")
	fs.BoolVar(worktreeOn, "worktree", false, "")
	fs.BoolVar(worktreeOff, "no-worktree", false, "")
	fs.BoolVar(&req.reapOn, "reap", false, "")
	fs.BoolVar(&req.reapOff, "no-reap", false, "")
	fs.StringVar(&req.placement, "placement", "", "")
	fs.StringVar(&req.workspace, "workspace", "", "")
	fs.StringVar(&req.agent, "agent", "", "")
	// One flag per declared session option, registered FROM the
	// declaration (agent-options spec §8.1), so an option some kind gains
	// is a flag here by construction -- and a flag createUsage and the
	// spawn skill must then name, which their tests enforce.
	req.options = map[string]*string{}
	for _, name := range agentopts.Names() {
		v := new(string)
		req.options[name] = v
		fs.StringVar(v, agentopts.FlagName(name), "", "")
	}
	fs.StringVar(&req.account, "account", "", "")
	fs.StringVar(&req.issue, "issue", "", "")
	fs.BoolVar(&req.json, "json", false, "")
	fs.BoolVar(&req.dryRun, "dry-run", false, "")
	fs.StringVar(&req.onFailure, "on-failure", onFailureKeep, "")
}

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

	registerFlags(fs, &req, &worktreeOn, &worktreeOff)

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

	if req.set["reap"] && req.set["no-reap"] {
		return request{}, errors.New("--reap and --no-reap contradict each other")
	}
	switch {
	case req.set["reap"]:
		v := req.reapOn
		req.reap = &v
	case req.set["no-reap"]:
		// Inverted for --no-worktree's reason: each flag is the other's
		// mirror, so --no-reap=false asks for the reap.
		v := !req.reapOff
		req.reap = &v
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
			return fmt.Errorf("unknown --placement %q: expected new-space, tab-here, split-here or tab-in", r.placement)
		}
	}
	if r.set["workspace"] {
		if strings.TrimSpace(r.workspace) == "" {
			return fmt.Errorf("--workspace needs a workspace id (herdr workspace list)")
		}
		if r.set["placement"] && r.placement != "tab-in" {
			return fmt.Errorf("--workspace names where --placement tab-in opens its tab; it does not apply to --placement %s", r.placement)
		}
	}
	switch r.onFailure {
	case onFailureKeep, onFailureClean:
	default:
		return fmt.Errorf("unknown --on-failure %q: expected keep or clean", r.onFailure)
	}
	// A blank option is a mistake rather than a request: "inherit" is how
	// to ask for no flag, and a script that interpolated an empty variable
	// should hear about it rather than silently inherit. Whether the value
	// itself is valid depends on the agent kind, which is resolve.go's.
	for _, name := range agentopts.Names() {
		flag := agentopts.FlagName(name)
		if r.set[flag] && strings.TrimSpace(*r.options[name]) == "" {
			return fmt.Errorf("--%s needs a value, or %q to send none", flag, agentopts.Inherit)
		}
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
	// part of the prompt, and a CRLF file ends in one too: trimming only
	// the LF would leave a CR that plan.SanitizePrompt then turns back into
	// a line break (#183). Everything inside is promptText's to clean.
	return strings.TrimRight(string(b), "\r\n"), true, nil
}
