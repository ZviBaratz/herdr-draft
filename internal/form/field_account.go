// field_account.go is written fresh for this task, built entirely on
// widgets.Picker (Task 14, ported from Atrium's own clean-listed
// ui/overlay/picker.go mixin) against internal/clauth's real Status/
// Profile/Window types -- NOT a port of Atrium's own accountPicker.go/
// accountSelection.go. Those two files are on the audited clean list
// (spec §14), but per this task's own explicit instruction they are
// deliberately never opened for this component: Atrium's account model is
// built around a POOL of interchangeable accounts with round-robin
// rotation across concurrent sessions, a shape clauth's own real
// Status/Profile feed (a small, named, user-picked set of auth profiles,
// no rotation concept at all -- internal/clauth/status.go) does not
// match. Building from the brief and clauth's actual types directly,
// rather than adapting a pool/rotation UI to a feed that has no pool to
// rotate, avoids importing a mismatched interaction model wholesale.
package form

import (
	"fmt"
	"math"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// accountPickerRows is the account picker's fixed candidate-row count
// (spec §6 field 7's own constant-height contract), matching
// field_dir.go's dirPickerRows/field_worktree.go's basePickerRows -- a
// clauth profile list is short (spec's own example config lists a
// handful), so this doesn't need field_issue.go's larger issuePickerRows.
const accountPickerRows = 4

const accountLabel = "Account: "

// accountActiveID is the "active" row's internal widgets.PickerItem.ID --
// a leading-NUL sentinel that can never collide with a real clauth
// profile name (clauth profile names are plain user-chosen identifiers,
// e.g. "alpha", "quantivly-2"; none begin with a NUL byte), matching
// field_issue.go's issueNoneID and field_worktree.go's baseHeadID
// sentinel discipline: widgets.Picker requires unique, non-empty IDs
// (Task 14's own carried fact), and Pin() translates this sentinel back
// to "" at the getter boundary, so the internal ID never leaks through
// the public getter -- mirroring baseHeadID's identical "" == default
// contract.
const accountActiveID = "\x00active"

const (
	accountActiveLabel = "Active"
	accountActiveHint  = "use whatever profile is live"

	// accountInertPlaceholder is SetAgentIsClaude(false)'s own
	// explanatory placeholder -- spec §6 field 7: "inert unless the
	// selected agent kind is claude (dynamic)". clauth only wraps Claude
	// launches (spec §11): pinning an account has nothing to apply to for
	// any other agent kind.
	accountInertPlaceholder = "account pinning only applies to claude"

	// accountDegradedHint is shown on the always-reserved hint row when
	// the most recent SetProfiles carried a Degraded clauth.Status --
	// spec §11: "schema != 1 -> degrade to name-only entries, never
	// crash."
	accountDegradedHint = "clauth status degraded — showing names only"

	accountWarnAuthFailed  = "auth failed"
	accountWarnRateLimited = "rate limited"

	// accountWindowLabel is the usage window row() looks for -- spec §6
	// field 7's own row shape ("name · tier · auth_status · 5h/7d window
	// utilization") and the brief's own literal row format ("name · tier
	// · auth · 5h N%") both single out the 5-hour window specifically.
	accountWindowLabel = "5h"
)

// AccountField is the form's clauth account Section (spec §6 field 7): a
// picker over clauth.Status.Profiles, "active" pinned as row 0 (spec:
// "don't pin — use whatever profile is live"). Per the brief, this
// component ships v1's inline-marking-only posture: spec §16 non-goal 9
// defers the exhausted-confirm modal Atrium's own accountPicker gates on
// to future work -- AccountField only marks a rate-limited/auth-failed
// profile's row, it never blocks selecting one.
//
// Rendered only when the app layer constructs it at all (spec §6 field 7:
// "rendered only when clauth is configured and ≥2 profiles exist
// (static)" -- the same static-precondition-is-the-app-layer's-job
// posture field_issue.go's IssueField.Enabled documents for Linear).
// Enabled() instead reports the DYNAMIC half of that same spec sentence
// ("inert unless the selected agent kind is claude"), driven by
// SetAgentIsClaude.
//
// AccountField renders at a CONSTANT 2+accountPickerRows physical lines
// regardless of focus, agent-kind inertness, profile set, or degraded
// status (this task's own "verified fact": Section.Height must be
// hint-independent) -- a header row (label + current pin display), an
// always-reserved hint row, then accountPickerRows candidate rows (always
// shown, matching field_worktree.go's base-picker convention: a short
// profile list is small enough that hiding it while unfocused buys
// nothing).
type AccountField struct {
	palette theme.Palette
	picker  *widgets.Picker
	focused bool

	agentIsClaude bool
	degraded      bool
}

// NewAccountField returns an AccountField with only the "active" sentinel
// row (no profiles yet -- see SetProfiles), inert by default (agent kind
// unknown -- see SetAgentIsClaude's own doc comment for why this
// conservative default matches field_worktree.go's WorktreeField.Enabled
// convention), styled from palette.
func NewAccountField(palette theme.Palette) *AccountField {
	f := &AccountField{
		palette: palette,
		picker:  widgets.NewPicker(palette),
	}
	f.refreshItems()
	return f
}

// ID identifies this Section for form.go's zoneFor.
func (f *AccountField) ID() string { return "account" }

// Enabled reports the dynamic half of spec §6 field 7's precondition:
// present-but-inert (form.go's Section doc comment) whenever the
// currently selected agent kind is not claude -- see SetAgentIsClaude.
func (f *AccountField) Enabled() bool { return f.agentIsClaude }

// Focus gives the field input focus. widgets.Picker has no Focus/Blur of
// its own (see its package doc); focused is tracked only for
// Section-interface completeness, matching field_worktree.go's
// worktreeBaseSection.
func (f *AccountField) Focus() tea.Cmd {
	f.focused = true
	return nil
}

// Blur removes input focus.
func (f *AccountField) Blur() { f.focused = false }

// Update moves the picker's cursor on Up/Down -- matching
// field_worktree.go's worktreeBaseSection.Update; every other message is
// ignored (no filter text, no chip navigation -- a clauth profile list is
// short enough that Up/Down alone is sufficient, per the type doc
// comment).
func (f *AccountField) Update(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch km.String() {
	case "up":
		f.picker.CursorPrev()
	case "down":
		f.picker.CursorNext()
	}
	return nil
}

// SetAgentIsClaude records whether the form's currently selected agent
// kind is "claude" (spec §6 field 7's dynamic inert condition -- the app
// layer is expected to call this whenever AgentField.Value() changes, the
// same live-reactive wiring WorktreeField.SetGitTarget documents for a
// changing project directory). A freshly constructed AccountField defaults
// to inert (agentIsClaude == false) until the first call -- the same
// conservative "not usable until told otherwise" default
// field_worktree.go's WorktreeField.Enabled documents ("nothing here
// performs its own I/O to find out"), applied here to "which agent kind
// is currently selected" instead of "is the target a git repo."
func (f *AccountField) SetAgentIsClaude(isClaude bool) { f.agentIsClaude = isClaude }

// SetProfiles replaces the account picker's rows from status. Unlike
// field_dir.go's SetCandidates/field_worktree.go's SetBaseItems/
// field_issue.go's SetIssues, this setter's own signature (per the
// brief: `SetProfiles(clauth.Status)`, no caller-supplied version) carries
// no explicit staleness gate -- a deliberate difference from those three,
// not an oversight: this field's own internal widgets.Picker version is
// held FIXED across every call (see refreshItems), so every SetProfiles
// after the first is treated as a same-version refresh and preserves the
// current pin by profile name (widgets.Picker.SetItems' own same-version
// contract), the behavior spec §8's "clauth profiles ... form-open + on
// account focus" polling needs (a re-poll must not silently bounce a
// user's pin back to "active").
func (f *AccountField) SetProfiles(status clauth.Status) {
	f.degraded = status.Degraded
	f.picker.SetItems(0, buildAccountItems(status.Profiles, status.Degraded))
}

// refreshItems seeds the picker with just the "active" sentinel row (no
// profiles yet) -- called once at construction so the field is never left
// with an empty picker before the first SetProfiles call (zero-value
// safety: Selected()/Pin() always have a row to answer from).
func (f *AccountField) refreshItems() {
	f.picker.SetItems(0, buildAccountItems(nil, false))
}

// buildAccountItems builds the account picker's full item list: the
// "active" sentinel row first, then one row per profile (name-only when
// degraded is true -- spec §11: "schema != 1 -> degrade to name-only
// entries"). A profile with an empty Name, or a Name already seen, is
// skipped entirely -- clauth.Profile.Name is unvalidated external JSON
// (internal/clauth/status.go's ParseStatus enforces neither
// non-emptiness nor uniqueness), and widgets.PickerItem.ID is built
// directly from it (see accountRow), so an empty or duplicate name would
// otherwise either collide with the "active" sentinel's own "" ==
// no-pin contract (Pin() below) or make widgets.Picker's first-match-wins
// ID lookup pick an arbitrary one of the duplicates -- the same bug class
// field_worktree.go's refreshBaseItems guards against for base refs via
// an identical seen map, and field_issue.go's refreshItems guards against
// for issues with an empty Identifier.
func buildAccountItems(profiles []clauth.Profile, degraded bool) []widgets.PickerItem {
	items := make([]widgets.PickerItem, 0, len(profiles)+1)
	items = append(items, widgets.PickerItem{ID: accountActiveID, Label: accountActiveLabel, Hint: accountActiveHint})
	seen := map[string]bool{accountActiveID: true}
	for _, p := range profiles {
		if p.Name == "" || seen[p.Name] {
			continue
		}
		seen[p.Name] = true
		items = append(items, accountRow(p, degraded))
	}
	return items
}

// accountRow builds one profile's picker row: "name · tier · auth · 5h
// N%" (the brief's own literal row format) when degraded is false, or
// name-only when it is true (see buildAccountItems' own doc comment). A
// rate-limited (any usage window at or past 100%) or auth-failed
// (AuthStatus set and not "ok") profile carries a warning Marker plus a
// matching note appended to its Hint -- spec §6 field 7: "Rate-limited or
// auth-failed profiles are selectable but visibly marked", and spec §16
// non-goal 9: v1 stops at this inline marker, no blocking modal.
func accountRow(p clauth.Profile, degraded bool) widgets.PickerItem {
	if degraded {
		return widgets.PickerItem{ID: p.Name, Label: p.Name}
	}

	warning := accountWarning(p)
	marker := ""
	if warning != "" {
		marker = "!"
	}

	hint := accountWindowHint(p)
	if warning != "" {
		hint = strings.TrimSpace(hint + "  " + warning)
	}

	return widgets.PickerItem{
		ID:     p.Name,
		Label:  fmt.Sprintf("%s · %s · %s", p.Name, p.Tier, p.AuthStatus),
		Hint:   hint,
		Marker: marker,
	}
}

// accountWarning reports the profile's own warning text -- "" when
// neither condition applies.
func accountWarning(p clauth.Profile) string {
	if p.AuthStatus != "" && p.AuthStatus != "ok" {
		return accountWarnAuthFailed
	}
	for _, w := range p.Windows {
		if w.UtilizationPct >= 100 {
			return accountWarnRateLimited
		}
	}
	return ""
}

// accountWindowHint renders the profile's 5h window utilization, or a
// placeholder when clauth reported no "5h"-labeled window at all (an
// empty Windows slice is a real, observed clauth shape -- see
// internal/clauth/status_test.go's stale-status-file fixture).
func accountWindowHint(p clauth.Profile) string {
	for _, w := range p.Windows {
		if w.Label == accountWindowLabel {
			return fmt.Sprintf("%s %d%%", accountWindowLabel, int(math.Round(w.UtilizationPct)))
		}
	}
	return accountWindowLabel + " —"
}

// SetPin moves the account picker's cursor directly to the profile row
// whose ID (Name) matches pin -- e.g. to apply spec §12's config.toml
// `[clauth] default` value at form construction. "" and "active" (the
// config's own documented sentinel for "don't pin -- use whatever
// profile is live") are both no-ops: the picker already starts on the
// "active" row by construction (refreshItems/SetProfiles), matching
// Pin()'s own "" == active contract, so there is nothing to move for
// either. A pin naming a profile not present in the current item set
// (e.g. a stale or typo'd config value) is also a no-op --
// widgets.Picker.SelectID leaves the cursor wherever it already was
// rather than guessing.
//
// Added in Task 20b (the app layer) alongside PlacementField.SetValue --
// see field_title.go's SetTitle doc comment for the fuller writeup of
// this class of gap: a config-derived default value with no way to
// pre-select the field it configures.
func (f *AccountField) SetPin(pin string) {
	if pin == "" || pin == "active" {
		return
	}
	f.picker.SelectID(pin)
}

// Pin returns the currently selected profile name, or "" when the
// selection is the "active" sentinel row (spec: "don't pin — use
// whatever profile is live") -- the getter-boundary translation
// field_worktree.go's Base() documents for the identical baseHeadID
// sentinel shape.
func (f *AccountField) Pin() string {
	sel, ok := f.picker.Selected()
	if !ok || sel.ID == accountActiveID {
		return ""
	}
	return sel.ID
}

// Height reports AccountField's constant footprint -- independent of
// winH, focus, agent-kind inertness, profile set, or degraded status (see
// the type doc comment).
func (f *AccountField) Height(int) int { return 2 + accountPickerRows }

// View renders the field at exactly Height's own physical line count.
func (f *AccountField) View(inner int) string {
	if inner < 1 {
		inner = 1
	}
	labelStyled := lipgloss.NewStyle().Foreground(f.palette.Text).Render(accountLabel)
	budget := inner - lipgloss.Width(accountLabel)
	if budget < 1 {
		budget = 1
	}

	if !f.agentIsClaude {
		header := fitLine(labelStyled+fitLine(dimHint(f.palette).Render(accountInertPlaceholder), budget), inner)
		blanks := make([]string, accountPickerRows)
		for i := range blanks {
			blanks[i] = fitLine("", inner)
		}
		return header + "\n" + fitLine("", inner) + "\n" + strings.Join(blanks, "\n")
	}

	display := accountActiveLabel
	if pin := f.Pin(); pin != "" {
		display = pin
	}
	body := fitLine(lipgloss.NewStyle().Foreground(f.palette.Text).Render(display), budget)
	header := fitLine(labelStyled+body, inner)

	hintLine := fitLine("", inner)
	if f.degraded {
		hintLine = fitLine(dimHint(f.palette).Render(accountDegradedHint), inner)
	}

	rows := f.picker.View(inner, accountPickerRows)
	return header + "\n" + hintLine + "\n" + rows
}
