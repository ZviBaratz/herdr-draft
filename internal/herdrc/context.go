// Package herdrc talks to a running herdr instance on behalf of the
// herdr-draft plugin: it decodes the plugin invocation context herdr hands
// the process on startup, and it drives the herdr CLI (never the raw socket
// API) to create and control agent sessions.
//
// It also holds the two facts herdr-plugin.toml declares ABOUT this plugin
// -- PluginID and Version -- because those are what herdr reads to install
// and name it, and because keeping them together keeps them under one
// manifest-consistency test (manifest_test.go) instead of two.
package herdrc

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// PluginID is this plugin's own id, as declared by herdr-plugin.toml's
// `id`. It lives here, in the package that decodes herdr's invocation
// context, because that is what it names: what herdr calls us.
//
// One copy on purpose. It is the install directory, the config dir, the
// state dir and the `--plugin` argument all at once, so every place that
// prints a command a user should run needs it, and a second copy would be
// a second thing to forget. TestPluginIDMatchesManifest holds it to the
// manifest.
const PluginID = "zvibaratz.draft"

// Version is this plugin's own version, as declared by
// herdr-plugin.toml's `version`. It is what herdr shows for the installed
// plugin, so reporting anything else here would mean the plugin and the
// thing that installed it disagree about what is running.
//
// Bump this and the manifest together; TestVersionMatchesManifest fails
// otherwise. The git tag is the third member of that set and the one no
// test can reach -- see CHANGELOG/release process.
const Version = "0.1.0"

// ErrContextUnset reports that $HERDR_PLUGIN_CONTEXT_JSON was empty rather
// than malformed. The two are worth telling apart: malformed means herdr
// sent something this binary could not read, which is a bug somewhere;
// EMPTY almost always means the program was started by a person, from a
// shell, without herdr involved at all.
//
// That case used to surface as `parse plugin context: unexpected end of
// JSON input` -- technically accurate, and the first thing a curious
// person saw after installing the plugin.
var ErrContextUnset = errors.New("$HERDR_PLUGIN_CONTEXT_JSON is not set")

// ContextWorktree mirrors herdr's WorkspaceWorktreeInfo
// (https://github.com/herdrdev/herdr/blob/b1ff4582/src/api/schema/workspaces.rs#L76):
// the
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
// (PluginInvocationContext,
// https://github.com/herdrdev/herdr/blob/b1ff4582/src/api/schema/plugins.rs#L363),
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
// An empty payload returns ErrContextUnset rather than a JSON error -- see
// that variable for why the distinction is worth making. Note that "{}"
// parses fine and is a legitimate context: every field is optional, so
// absence of DATA is not absence of a CONTEXT.
func ParseContext(raw string) (Context, error) {
	if strings.TrimSpace(raw) == "" {
		return Context{}, ErrContextUnset
	}
	var ctx Context
	if err := json.Unmarshal([]byte(raw), &ctx); err != nil {
		return Context{}, fmt.Errorf("parse plugin context: %w", err)
	}
	return ctx, nil
}
