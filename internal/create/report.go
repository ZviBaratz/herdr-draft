// report.go writes what happened: spec §13's "progress to stderr, one
// line per step; result to stdout, or a single object with --json".
package create

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/ZviBaratz/herdr-draft/internal/agentopts"
	"github.com/ZviBaratz/herdr-draft/internal/defaults"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

// report is one finished create, successful or not, in the form both
// output modes read from.
type report struct {
	input      plan.Input
	provenance map[string]string
	result     plan.ExecResult

	// failedLabel/err are the failing op's own label and error, captured
	// from plan.Execute's progress callback: ExecResult carries the failed
	// INDEX but not the error, and the error is the only part a caller can
	// act on.
	failedLabel string
	err         error

	onFailure    string
	cleaned      bool
	cleanRefused string
	// outcome is what the clean did with the worktree's branch (#173).
	// Meaningful only when cleaned.
	outcome plan.CleanOutcome

	json bool
	// dryRun marks a --dry-run's report (#209): input and provenance are
	// set, and nothing ran, so result is the zero value and means nothing.
	dryRun bool
}

// write emits the result: one JSON object on stdout under --json, a human
// line otherwise. A failure's detail goes to stderr in human mode, where
// the progress lines it belongs beside already are -- stdout stays the
// channel that carries a created session and nothing else, so `$(...)`
// around a create is either a session or empty.
func (r report) write(stdout, stderr io.Writer) {
	if r.json {
		r.writeJSON(stdout)
		return
	}
	if r.dryRun {
		fmt.Fprintln(stdout, r.dryRunLine())
		return
	}
	if r.ok() {
		fmt.Fprintln(stdout, r.humanLine())
		r.writeUnsentPrompt(stderr)
		return
	}
	fmt.Fprintf(stderr, "herdr-draft create: %s\n", r.failureLine())
	r.writeUnsentPrompt(stderr)
}

// ok reports whether every op ran -- or, for a dry run, that the real run
// would have gone ahead, which is all a dry run that got this far can say.
func (r report) ok() bool { return r.dryRun || r.result.FailedIndex == -1 }

// humanLine is the one line a successful create prints: key=value, so it
// is greppable by a shell that did not ask for --json but still wants the
// pane id.
//
// The three ids name where the AGENT is (ExecResult.AgentAt), not the
// space (#99). A caller greps `pane=` in order to send the next keystroke
// somewhere, and when herdr reused a workspace already open for the
// checkout, the space's pane belongs to another session; the agent is in a
// tab claimed beside it (placement spec §5.2). The space's ids stay
// available under --json's space_* keys; this line stays three ids long.
func (r report) humanLine() string {
	parts := []string{"created"}
	if a := r.result.AgentAt; a != nil {
		parts = append(parts,
			"workspace="+a.WorkspaceID,
			"tab="+a.TabID,
			"pane="+a.PaneID,
		)
	}
	// The checkout belongs to the space, which is what made it, and is
	// the same path either way.
	if c := r.result.Created; c != nil && c.CheckoutPath != "" {
		parts = append(parts, "checkout="+c.CheckoutPath)
	}
	parts = append(parts, "agent="+r.input.AgentKind)
	if r.input.UseWorktree {
		parts = append(parts, "branch="+r.input.Branch)
	}
	return strings.Join(parts, " ")
}

// dryRunLine is the one line a --dry-run prints without --json: what the
// run would create, in humanLine's key=value shape, naming what a command
// line usually leaves to the defaults -- the branch and base or the
// placement, the account, and the session options the agent would launch
// with. The title is quoted, being the one value with spaces in it.
func (r report) dryRunLine() string {
	in := r.input
	parts := []string{"would create", fmt.Sprintf("title=%q", in.Title), "agent=" + in.AgentKind}
	if in.UseWorktree {
		parts = append(parts, "branch="+in.Branch, "base="+cmp.Or(in.BaseRef, plan.WorktreeBase(in), "HEAD"))
	} else {
		parts = append(parts, "placement="+defaults.PlacementValue(in.Placement))
	}
	if in.AccountPin != "" {
		parts = append(parts, "account="+in.AccountPin)
	}
	launch := launchOptions(in)
	for _, o := range agentopts.For(in.AgentKind) {
		if v := launch[o.Name]; v != "" {
			parts = append(parts, o.Name+"="+v)
		}
	}
	if in.MarkReady && plan.ReapApplies(in.Prompt) {
		parts = append(parts, "mark_ready=true")
	}
	return strings.Join(parts, " ")
}

// failureLine says what failed and what became of what had already been
// created -- the two facts a caller needs to decide whether to retry.
func (r report) failureLine() string {
	var b strings.Builder
	b.WriteString("failed")
	if r.failedLabel != "" {
		b.WriteString(" while " + r.failedLabel)
	}
	if r.err != nil {
		b.WriteString(": " + r.err.Error())
	}
	switch {
	case r.result.NothingCreated:
		b.WriteString("\nnothing was created")
	case r.result.Created == nil:
		// No space was reported, and nothing shows herdr refused before it
		// acted (#192): its worktree create can fail after git has made the
		// branch, the checkout, and even a workspace on it. So this says
		// neither thing.
		b.WriteString("\nherdr may have made part of it before failing")
		if r.input.UseWorktree {
			b.WriteString(" -- any of the branch " + r.input.Branch + ", its checkout and a workspace for it")
		}
		b.WriteString("; look before retrying")
		if r.cleanRefused != "" {
			b.WriteString("\n--on-failure clean: " + r.cleanRefused)
		}
	case r.cleaned:
		b.WriteString("\nthe session it had created was removed (--on-failure clean)")
		switch o := r.outcome; {
		case o.DeletedBranch != "":
			b.WriteString(", with its branch " + o.DeletedBranch)
			if o.DeletedTip != "" {
				// The short form git's own "Deleted branch" line uses: enough
				// for `git branch <name> <tip>` to find it.
				b.WriteString(" (was " + shortCommit(o.DeletedTip) + ")")
			}
		case o.KeptBranch != "":
			b.WriteString(", but not its branch " + o.KeptBranch + ": " + o.KeptReason)
		}
	case r.cleanRefused != "":
		b.WriteString("\nkept the session it had created: " + r.cleanRefused)
	default:
		b.WriteString(fmt.Sprintf("\nkept the session it had created (workspace %s, pane %s)",
			r.result.Created.WorkspaceID, r.result.Created.PaneID))
	}
	return b.String()
}

// writeUnsentPrompt reproduces a prompt that never reached the agent.
// plan.Execute populates PromptText only when the prompt op itself failed
// -- including the case internal/plan/dialog.go's guard causes, where the
// agent was showing a blocking dialog and the text was deliberately NOT
// typed into it. Either way it is the one piece of the caller's own work
// this failure can destroy, and a headless caller has no pane to scroll
// back through, so it is written out verbatim.
func (r report) writeUnsentPrompt(w io.Writer) {
	if r.result.PromptText == "" {
		return
	}
	if r.result.PromptUnconfirmed {
		// Deliberately not "was not sent". This is the wording that caused
		// #108's double-paste: the old line asserted a delivery failure
		// for a prompt the agent was already several tool calls into, and
		// the documented thing to do with the text below is paste it.
		//
		// Worded for every unconfirmed shape, not just #108's: after a
		// send herdr failed (#228) no agent is working on the prompt, but
		// its input box may hold part of it.
		fmt.Fprintf(w, "delivery of the prompt could not be confirmed -- read the pane before "+
			"resending it, since the agent may already have some or all of it:\n%s\n", r.result.PromptText)
		return
	}
	fmt.Fprintf(w, "the prompt was not sent -- reproduced here so it is not lost:\n%s\n", r.result.PromptText)
}

// The three values of jsonReport.PromptStatus. Constants because two of
// them are written in one place and read in another, and a typo in a
// snake_case string literal is not a compile error.
const (
	promptStatusSent        = "sent"
	promptStatusUnsent      = "unsent"
	promptStatusUnconfirmed = "unconfirmed"
)

// jsonReport is --json's single object. Field names are snake_case and
// stable; anything absent (no worktree, no prompt, no failure) is omitted
// rather than emitted empty, so a consumer can test for presence.
type jsonReport struct {
	OK bool `json:"ok"`
	// DryRun is true for a --dry-run's report (#209): everything below was
	// resolved and checked, and nothing was created, so there are no ids
	// and no prompt fate to report. OK then means the real run would go
	// ahead.
	DryRun     bool   `json:"dry_run,omitempty"`
	Error      string `json:"error,omitempty"`
	FailedStep string `json:"failed_step,omitempty"`

	Title      string `json:"title"`
	ProjectDir string `json:"project_dir"`
	AgentKind  string `json:"agent_kind"`
	Account    string `json:"account,omitempty"`
	Placement  string `json:"placement"`
	Worktree   bool   `json:"worktree"`
	Branch     string `json:"branch,omitempty"`
	Base       string `json:"base,omitempty"`

	// WorkspaceID/TabID/PaneID name where the AGENT ended up
	// (ExecResult.AgentAt) -- the ids a caller asks a create for, since
	// they are what it addresses next. Space* name the space the plan
	// created and --on-failure clean acts on (ExecResult.Created), which
	// is a different tab and pane whenever the worktree's workspace was
	// already open (#99). Before placement spec §14 a worktree combined with
	// a `here` placement separated all three; that combination is refused
	// now.
	//
	// Both triples are emitted whenever they are known, equal or not: a
	// consumer comparing them learns whether the agent sits inside the
	// space, and one that only wants an id does not have to discover that
	// a key it relies on disappears in the common case. A failure before
	// any pane was claimed leaves the agent triple absent and the space's
	// present, which is the true statement about that run.
	WorkspaceID  string `json:"workspace_id,omitempty"`
	TabID        string `json:"tab_id,omitempty"`
	PaneID       string `json:"pane_id,omitempty"`
	CheckoutPath string `json:"checkout_path,omitempty"`

	SpaceWorkspaceID string `json:"space_workspace_id,omitempty"`
	SpaceTabID       string `json:"space_tab_id,omitempty"`
	SpacePaneID      string `json:"space_pane_id,omitempty"`

	// PromptSent is absent when there was no prompt at all, and also when
	// there WAS one whose fate is unknown -- a prompt-wait timeout (#108),
	// where neither true nor false is a statement this command can make.
	// It stays a *bool so a consumer already branching on true/false is
	// unaffected; PromptStatus is where the third state lives.
	//
	// PromptStatus names the outcome in words -- "sent", "unsent" or
	// "unconfirmed" -- and is present whenever the plan carried a prompt.
	// The successful case is named rather than inferred from an absence,
	// because "field missing" already means two different things above.
	//
	// The text comes back under exactly one of the two remaining keys, and
	// which one is the point. UnsentPrompt means "this failure destroyed
	// your work, here it is back", which is why a caller pastes it into
	// the pane; on an unconfirmed delivery that is how a working agent
	// gets its instructions twice, so that case uses UnconfirmedPrompt and
	// the accompanying message says to read the pane first.
	PromptSent        *bool  `json:"prompt_sent,omitempty"`
	PromptStatus      string `json:"prompt_status,omitempty"`
	UnsentPrompt      string `json:"unsent_prompt,omitempty"`
	UnconfirmedPrompt string `json:"unconfirmed_prompt,omitempty"`

	// MarkReady is true when the prompt carried pane-reaper's instruction,
	// which is what a consumer means by "will this pane close itself?". The
	// toggle's position alone does not answer that, and stays visible
	// through provenance (reap spec §7.4).
	MarkReady bool `json:"mark_ready,omitempty"`

	// AgentOptions is the session options the agent was launched with,
	// option name to value (agent-options spec §8.2) -- the chosen ones, not
	// what [agents.extra_args] passes on its own. Absent when every option
	// inherits; provenance's option.<name> entries say where each came from.
	AgentOptions agentopts.Values `json:"agent_options,omitempty"`
	// LaunchOptions is what the agent's command line actually carries for
	// each declared option (#209): a chosen value, or the one
	// [agents.extra_args] passes for an option left on inherit. The #208
	// session ran on a model and an effort extra_args set, with
	// agent_options absent. Absent here means no flag at all, so the
	// agent's own settings decide; provenance's option.<name> says which
	// source supplied each, "extra_args" included.
	//
	// It reads the declared flags and nothing else, which is why AgentArgs
	// exists beside it.
	LaunchOptions agentopts.Values `json:"launch_options,omitempty"`
	// AgentArgs is the agent's whole argument list (#219), plan.AgentArgs:
	// the list both launch paths hand the agent's command, so the three
	// option flags and their values are in it, and so is every other
	// argument [agents.extra_args] passes. Before it, such an argument --
	// one that skips the agent's permission prompts, say -- reached the
	// agent unreported, and launch_options could say plan mode for a
	// session that asked nobody anything. An array, not a shell string:
	// both paths do end up typed into the pane's shell, but each is quoted
	// there by the layer that types it -- herdr for `agent start`, PaneRun
	// for a launcher -- and quoting depends on the shell, so a string here
	// would be one shell's spelling of the list. Its elements are what a
	// consumer compares. Absent when the agent starts with no arguments.
	//
	// It is what this command passes, after the agent's own command. The
	// pane's shell resolves that command, so an alias or function named
	// after the agent stands in between, and a pinned account's
	// `[clauth] launcher` does too. A launcher can add arguments of its own,
	// and one of the form `sh -c "..."` drops every argument after it
	// (agent-options spec §5.1). Nothing in this report can see either.
	AgentArgs []string `json:"agent_args,omitempty"`

	OnFailure    string `json:"on_failure,omitempty"`
	Cleaned      bool   `json:"cleaned,omitempty"`
	CleanRefused string `json:"clean_refused,omitempty"`

	// DeletedBranch is the worktree branch the clean deleted -- the one
	// this run made (#173) -- and DeletedBranchTip the commit it pointed
	// at, so `git branch <deleted_branch> <deleted_branch_tip>` puts it
	// back. KeptBranch is a worktree branch the clean left in place, and
	// KeptBranchReason why: nothing showed this run made it, it held
	// commits nothing else did, or git would not delete it. A kept branch
	// still refuses a retry that derives the same name.
	DeletedBranch    string `json:"deleted_branch,omitempty"`
	DeletedBranchTip string `json:"deleted_branch_tip,omitempty"`
	KeptBranch       string `json:"kept_branch,omitempty"`
	KeptBranchReason string `json:"kept_branch_reason,omitempty"`

	// Provenance is spec §10's tier attribution, one entry per resolved
	// value: which file supplied it, or "flag" when the caller did,
	// "worktree" for the placement a worktree decides, "checkout" for the
	// commit a linked checkout supplies as an unset base, and "extra_args"
	// for a session option left on inherit that [agents.extra_args] passes
	// anyway (see provenanceFlag and its siblings).
	Provenance map[string]string `json:"provenance"`
}

func (r report) writeJSON(w io.Writer) {
	out := jsonReport{
		OK:         r.ok(),
		DryRun:     r.dryRun,
		Title:      r.input.Title,
		ProjectDir: r.input.ProjectDir,
		AgentKind:  r.input.AgentKind,
		Account:    r.input.AccountPin,
		Placement:  defaults.PlacementValue(r.input.Placement),
		Worktree:   r.input.UseWorktree,
		Provenance: r.provenance,
		MarkReady:  r.input.MarkReady && plan.ReapApplies(r.input.Prompt),

		AgentOptions:  r.input.AgentOptions,
		LaunchOptions: launchOptions(r.input),
		AgentArgs:     plan.AgentArgs(r.input),
	}
	if r.input.UseWorktree {
		out.Branch = r.input.Branch
		// A chosen base as chosen, whatever it resolved to; an unset one
		// from a linked checkout as the commit it turned out to be (#171).
		out.Base = cmp.Or(r.input.BaseRef, plan.WorktreeBase(r.input))
	}
	if c := r.result.Created; c != nil {
		out.SpaceWorkspaceID, out.SpaceTabID, out.SpacePaneID = c.WorkspaceID, c.TabID, c.PaneID
		out.CheckoutPath = c.CheckoutPath
	}
	if a := r.result.AgentAt; a != nil {
		out.WorkspaceID, out.TabID, out.PaneID = a.WorkspaceID, a.TabID, a.PaneID
	}
	// A dry run sent nothing, so the prompt has no fate to report -- and
	// "unsent" would read as one that needs sending again.
	if r.input.Prompt != "" && !r.dryRun {
		if r.result.PromptUnconfirmed {
			// PromptSent deliberately left nil: see its doc comment.
			out.PromptStatus = promptStatusUnconfirmed
			out.UnconfirmedPrompt = r.result.PromptText
		} else {
			sent := r.result.PromptText == ""
			out.PromptSent = &sent
			out.PromptStatus = promptStatusUnsent
			if sent {
				out.PromptStatus = promptStatusSent
			}
			out.UnsentPrompt = r.result.PromptText
		}
	}
	if !out.OK {
		out.FailedStep = r.failedLabel
		if r.err != nil {
			out.Error = r.err.Error()
		}
		out.OnFailure = r.onFailure
		out.Cleaned = r.cleaned
		out.CleanRefused = r.cleanRefused
		if r.cleaned {
			out.DeletedBranch, out.DeletedBranchTip = r.outcome.DeletedBranch, r.outcome.DeletedTip
			out.KeptBranch, out.KeptBranchReason = r.outcome.KeptBranch, r.outcome.KeptReason
		}
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// The only way this fails is an unencodable value, and every field
	// above is a string, a bool, a slice or map of strings, or a pointer to
	// a bool.
	_ = enc.Encode(out)
}

// launchOptions is jsonReport.LaunchOptions: the declared flags in the
// argument list both launch paths start the agent with, read back the same
// way the form reads what extra_args pins, so the report and the launch
// cannot disagree about what displaced what.
func launchOptions(in plan.Input) agentopts.Values {
	return agentopts.Pinned(in.AgentKind, plan.AgentArgs(in))
}

// shortCommit is the first seven characters of a full commit id, git's own
// default abbreviation; an id already shorter is returned as it is.
func shortCommit(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
}
