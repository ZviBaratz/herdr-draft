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
func (r report) humanLine() string {
	parts := []string{"created"}
	if c := r.result.Created; c != nil {
		parts = append(parts,
			"workspace="+c.WorkspaceID,
			"tab="+c.TabID,
			"pane="+c.PaneID,
		)
		if c.CheckoutPath != "" {
			parts = append(parts, "checkout="+c.CheckoutPath)
		}
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
	fmt.Fprintf(w, "the prompt was not sent -- reproduced here so it is not lost:\n%s\n", r.result.PromptText)
}

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

	WorkspaceID  string `json:"workspace_id,omitempty"`
	TabID        string `json:"tab_id,omitempty"`
	PaneID       string `json:"pane_id,omitempty"`
	CheckoutPath string `json:"checkout_path,omitempty"`

	// PromptSent is absent when there was no prompt at all, so "false"
	// always means "there was one and it did not land".
	PromptSent   *bool  `json:"prompt_sent,omitempty"`
	UnsentPrompt string `json:"unsent_prompt,omitempty"`

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
		out.WorkspaceID, out.TabID, out.PaneID, out.CheckoutPath = c.WorkspaceID, c.TabID, c.PaneID, c.CheckoutPath
	}
	if r.input.Prompt != "" {
		sent := r.result.PromptText == ""
		out.PromptSent = &sent
		out.UnsentPrompt = r.result.PromptText
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
