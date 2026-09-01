// Package herdrc talks to a running herdr instance on behalf of the
// herdr-draft plugin: it decodes the plugin invocation context herdr hands
// the process on startup, and it drives the herdr CLI (never the raw socket
// API) to create and control agent sessions.
package herdrc

import (
	"encoding/json"
	"fmt"
)

// ContextWorktree mirrors herdr's WorkspaceWorktreeInfo
// (/home/zvi/Projects/herdr/src/api/schema/workspaces.rs, ~line 76): the
// worktree summary embedded in a workspace when that workspace is backed by
// a Git worktree checkout.
type ContextWorktree struct {
	RepoKey          string `json:"repo_key"`
	RepoName         string `json:"repo_name"`
	RepoRoot         string `json:"repo_root"`
	CheckoutPath     string `json:"checkout_path"`
	IsLinkedWorktree bool   `json:"is_linked_worktree"`
}

// Context is herdr's plugin invocation context
// (PluginInvocationContext, /home/zvi/Projects/herdr/src/api/schema/plugins.rs:363),
// delivered to the plugin process via $HERDR_PLUGIN_CONTEXT_JSON. Every
// field is Optional on the Rust side, so callers must tolerate any field
// being absent from the payload -- a missing key simply leaves the
// corresponding Go field at its zero value (or nil, for Worktree).
type Context struct {
	WorkspaceID      string           `json:"workspace_id"`
	WorkspaceLabel   string           `json:"workspace_label"`
	WorkspaceCwd     string           `json:"workspace_cwd"`
	Worktree         *ContextWorktree `json:"worktree"`
	TabID            string           `json:"tab_id"`
	FocusedPaneID    string           `json:"focused_pane_id"`
	FocusedPaneCwd   string           `json:"focused_pane_cwd"`
	FocusedPaneAgent string           `json:"focused_pane_agent"`
}

// ParseContext decodes raw -- the verbatim $HERDR_PLUGIN_CONTEXT_JSON
// payload -- into a Context. Fields PluginInvocationContext marks Optional
// but that this Context does not model (e.g. tab_label, selected_text,
// invocation_source) are simply ignored by the decoder; every field this
// Context does model tolerates absence by falling back to its zero value.
func ParseContext(raw string) (Context, error) {
	var ctx Context
	if err := json.Unmarshal([]byte(raw), &ctx); err != nil {
		return Context{}, fmt.Errorf("parse plugin context: %w", err)
	}
	return ctx, nil
}
