// report.go writes what happened: spec §13's "progress to stderr, one
// line per step; result to stdout, or a single object with --json".
package create

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

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

	json bool
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
	if r.ok() {
		fmt.Fprintln(stdout, r.humanLine())
		r.writeUnsentPrompt(stderr)
		return
	}
	fmt.Fprintf(stderr, "herdr-draft create: %s\n", r.failureLine())
	r.writeUnsentPrompt(stderr)
}

// ok reports whether every op ran.
func (r report) ok() bool { return r.result.FailedIndex == -1 }

// humanLine is the one line a successful create prints: key=value, so it
// is greppable by a shell that did not ask for --json but still wants the
// pane id.
//
// The three ids name where the AGENT is (ExecResult.AgentAt), not the
// space (#99). A caller greps `pane=` in order to send the next keystroke
// somewhere, and with a worktree plus a `here` placement the space's pane
// is the worktree's own idle shell -- or, when the workspace was reused,
// a pane belonging to another session. The space's ids stay available
// under --json's space_* keys; this line stays three ids long.
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
	case r.result.Created == nil:
		b.WriteString("\nnothing was created")
	case r.cleaned:
		b.WriteString("\nthe session it had created was removed (--on-failure clean)")
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
		fmt.Fprintf(w, "delivery of the prompt could not be confirmed -- read the pane before "+
			"resending it, since the agent may already be working on it:\n%s\n", r.result.PromptText)
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
	OK         bool   `json:"ok"`
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
	// is a different workspace, tab AND pane whenever a worktree is
	// combined with a `here` placement, and a different tab and pane
	// whenever the worktree's workspace was already open (#99).
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

	OnFailure    string `json:"on_failure,omitempty"`
	Cleaned      bool   `json:"cleaned,omitempty"`
	CleanRefused string `json:"clean_refused,omitempty"`

	// Provenance is spec §10's tier attribution, one entry per resolved
	// value: which file supplied it, or "flag" when the caller did.
	Provenance map[string]string `json:"provenance"`
}

func (r report) writeJSON(w io.Writer) {
	out := jsonReport{
		OK:         r.ok(),
		Title:      r.input.Title,
		ProjectDir: r.input.ProjectDir,
		AgentKind:  r.input.AgentKind,
		Account:    r.input.AccountPin,
		Placement:  defaults.PlacementValue(r.input.Placement),
		Worktree:   r.input.UseWorktree,
		Provenance: r.provenance,
	}
	if r.input.UseWorktree {
		out.Branch = r.input.Branch
		out.Base = r.input.BaseRef
	}
	if c := r.result.Created; c != nil {
		out.SpaceWorkspaceID, out.SpaceTabID, out.SpacePaneID = c.WorkspaceID, c.TabID, c.PaneID
		out.CheckoutPath = c.CheckoutPath
	}
	if a := r.result.AgentAt; a != nil {
		out.WorkspaceID, out.TabID, out.PaneID = a.WorkspaceID, a.TabID, a.PaneID
	}
	if r.input.Prompt != "" {
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
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// The only way this fails is an unencodable value, and every field
	// above is a string, bool or map[string]string.
	_ = enc.Encode(out)
}
