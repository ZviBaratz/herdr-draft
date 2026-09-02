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
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// accountActiveID is the "active" row's internal widgets.PickerItem.ID --
// a leading-NUL sentinel that can never collide with a real clauth
// profile name (clauth profile names are plain user-chosen identifiers,
// e.g. "alpha", "work-2"; none begin with a NUL byte), matching
// field_issue.go's issueNoneID and field_worktree.go's baseHeadID
// sentinel discipline: widgets.Picker requires unique, non-empty IDs
// (Task 14's own carried fact), and Pin() translates this sentinel back
// to "" at the getter boundary, so the internal ID never leaks through
// the public getter -- mirroring baseHeadID's identical "" == default
// contract.
const accountActiveID = "\x00active"

const (
	// accountActiveLabel is the picker's own first row. Lowercase, and
	// therefore the same word accountRowActive puts in the row: the two
	// name one thing, and v1's capital was a leftover from a chip row that
	// read as a heading (v2 spec §3 rule 5).
	accountActiveLabel = "active"
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

const (
	// accountRowLabel is v2's row label (v2 spec §6).
	accountRowLabel = "account"
	// accountRowActive is what the unpinned row calls itself: not a
	// profile name but the standing instruction "use whatever profile is
	// live" (v2 spec §6's `active · max · 12%`).
	accountRowActive = "active"
	// accountRowSep separates the row's three parts.
	accountRowSep = " · "
	// accountRowAuthFailed is v2 spec §6's danger-colored state word,
	// replacing v1's bare "!" marker: "an auth failure reads `beta · max ·
	// sign in again` in the danger color".
	accountRowAuthFailed = "sign in again"
	// accountPanelMaxRows caps PanelRows -- a clauth profile set is small
	// and the panel should not claim more of the form than it can fill.
	accountPanelMaxRows = 8
	// accountPanelEmpty speaks in the field's own terms when clauth
	// reported no profiles at all (v2 spec §6.1's "nothing to choose").
	accountPanelEmpty = "no clauth profiles"
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
type AccountField struct {
	palette theme.Palette
	picker  *widgets.Picker
	focused bool

	agentIsClaude bool
	degraded      bool

	// profiles and activeProfile are the parts of the most recent
	// SetProfiles payload the ROW needs: with nothing pinned it reads the
	// LIVE profile's tier and utilization, which needs both the profile
	// records themselves and clauth's own answer to "which one is
	// active". v1 discarded both.
	profiles      []clauth.Profile
	activeProfile string

	// pickerRowsShown is how many profile rows the last Panel render
	// drew. widgets.Picker.SelectAt needs the SAME height MarkedView was
	// called with to map a click back to an item, and v2's panel height
	// varies with the window -- so the fixed v1 constant this used to
	// pass (accountPickerRows, 4, deleted with this field) picked the
	// wrong profile on any list the panel had scrolled.
	pickerRowsShown int

	// verdictKey/verdictText are SetVerdict's own staleness guard, the
	// same "clears the moment the underlying value changes" discipline
	// field_title.go's TitleField.verdictKey/field_dir.go's
	// DirField.validityPath document: panelStatus only shows
	// verdictText when verdictKey still equals the CURRENT Pin(), so a
	// verdict computed for a pin the user has since moved away from (Up/
	// Down, or a later SetPin) stops rendering on its own, with no
	// separate Clear call needed.
	verdictKey  string
	verdictText string
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
	if click, ok := msg.(tea.MouseClickMsg); ok {
		if f.pickerRowsShown > 0 {
			f.picker.SelectAt(click, f.pickerRowsShown, "row:"+f.ID()+":")
		}
		return nil
	}
	if wheel, ok := msg.(tea.MouseWheelMsg); ok {
		switch wheelDelta(wheel) {
		case -1:
			f.picker.CursorPrev()
		case 1:
			f.picker.CursorNext()
		}
		return nil
	}
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
//
// status.ActiveProfile and status.Profiles are retained (see their own
// doc comment on the struct) rather than discarded as v1 discarded them:
// v2's unpinned row reads `active · max · 12%` off the LIVE profile, which
// neither the picker's item set nor Pin() can answer.
func (f *AccountField) SetProfiles(status clauth.Status) {
	f.degraded = status.Degraded
	f.profiles = append([]clauth.Profile(nil), status.Profiles...)
	f.activeProfile = status.ActiveProfile
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

// SetVerdict records the app layer's own live-validation message for the
// pin (key, a profile name or "" for the "active" sentinel -- the same
// shape Pin() returns) that was current when it was computed: a short
// note shown on the always-reserved hint row, taking priority there over
// the degraded-status hint (View's own doc comment) -- e.g. spec §9's "a
// pinned account whose auth_status != ok blocks with an account verdict."
// A later call whose key no longer matches Pin() (the user has since
// moved to a different profile) is stored but never rendered -- see
// verdictKey's own doc comment; there is no separate Clear method,
// matching TitleField.SetVerdict's identical staleness-by-comparison
// design.
//
// Added in fix round 1 (Task 20b review): the auth-blocked case
// previously surfaced no NEW message at all -- only the picker row's own
// pre-existing "! auth failed" marker (Task 18), which was already
// visible before the blocked submit attempt, making a blocked Create
// press look like it silently did nothing.
func (f *AccountField) SetVerdict(key, text string) {
	f.verdictKey = key
	f.verdictText = text
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

// --- the row and its panel ------------------------------------------------

// Label is v2's row label (v2 spec §6's field table).
func (f *AccountField) Label() string { return accountRowLabel }

// Row is v2 spec §6's `personal · max · ok` when a profile is pinned and
// `active · max · 12%` when none is, with the third part becoming a
// COLORED STATE WORD where the profile needs attention: the utilization
// in Warning at or past 100% ("gamma · team · 100%"), and "sign in again"
// in Danger on an auth failure ("beta · max · sign in again"). That
// colored word is what replaces v1's bare "!" marker.
//
// Inert (the selected agent kind is not claude) states why, dim. A
// degraded clauth status (schema != 1) collapses the row to the name
// alone -- spec §11's "degrade to name-only entries" applies here exactly
// as it does to the picker's own rows, since tier, auth status and
// windows are all fields the degraded parse marks unreliable.
func (f *AccountField) Row(w int) string {
	if w < 1 {
		w = 1
	}
	text := lipgloss.NewStyle().Foreground(f.palette.Text)
	if !f.agentIsClaude {
		return fitLine(dimHint(f.palette).Render(keepHead(accountInertPlaceholder, w)), w)
	}

	name := accountRowActive
	lookup := f.activeProfile
	if pin := f.Pin(); pin != "" {
		name, lookup = pin, pin
	}

	profile, known := f.profileByName(lookup)
	if !known || f.degraded {
		return fitLine(text.Render(keepHead(name, w)), w)
	}

	state, style := f.accountRowState(profile, name != accountRowActive)
	suffix := accountRowSep + profile.Tier
	if state != "" {
		// A profile for which clauth reported neither an auth_status nor
		// a 5h window has no third part at all; its separator goes with
		// it rather than dangling off the tier.
		suffix += accountRowSep
	}
	// The state word and the tier are short and fixed; the profile NAME is
	// the part that can run long, so it is the part that gives up cells.
	budget := w - lipgloss.Width(suffix) - lipgloss.Width(state)
	if budget < 1 {
		return fitLine(text.Render(keepHead(name+suffix+state, w)), w)
	}
	return fitLine(text.Render(keepHead(name, budget)+suffix)+style.Render(state), w)
}

// accountRowState picks the row's third part and the color it is drawn
// in. An auth failure outranks a rate limit (you cannot use the profile
// at all), a rate limit outranks the resting state, and the resting state
// itself differs by row: a PINNED row names the auth status it was pinned
// on ("ok"), an unpinned one names the live profile's 5h utilization,
// which is the fact that would make you want to pin something else.
func (f *AccountField) accountRowState(p clauth.Profile, pinned bool) (string, lipgloss.Style) {
	if p.AuthStatus != "" && p.AuthStatus != "ok" {
		return accountRowAuthFailed, lipgloss.NewStyle().Foreground(f.palette.Danger)
	}
	if pct, ok := accountUtilization(p); ok && pct >= 100 {
		return accountPercent(pct), lipgloss.NewStyle().Foreground(f.palette.Warning)
	}
	plain := lipgloss.NewStyle().Foreground(f.palette.Text)
	if pinned {
		return p.AuthStatus, plain
	}
	if pct, ok := accountUtilization(p); ok {
		return accountPercent(pct), plain
	}
	return p.AuthStatus, plain
}

// profileByName finds the retained clauth.Profile called name. An empty
// name, or one clauth has never reported, is not known -- Row falls back
// to printing the name alone rather than inventing a tier for it.
func (f *AccountField) profileByName(name string) (clauth.Profile, bool) {
	if name == "" {
		return clauth.Profile{}, false
	}
	for _, p := range f.profiles {
		if p.Name == name {
			return p, true
		}
	}
	return clauth.Profile{}, false
}

// accountUtilization returns the profile's 5h window utilization, or
// (0, false) when clauth reported no such window (an empty Windows slice
// is a real, observed shape -- see accountWindowHint).
func accountUtilization(p clauth.Profile) (float64, bool) {
	for _, w := range p.Windows {
		if w.Label == accountWindowLabel {
			return w.UtilizationPct, true
		}
	}
	return 0, false
}

// accountPercent renders a utilization as v2 spec §6's row writes it:
// a whole number and a percent sign, no window label.
func accountPercent(pct float64) string {
	return strconv.Itoa(int(math.Round(pct))) + "%"
}

// Panel is the profile picker plus one status line, which carries -- in
// the same priority order v1 used -- a live verdict,
// then the degraded-status hint, then nothing.
func (f *AccountField) Panel(w, h int) string {
	if h < 1 {
		h = 1
	}
	lines := make([]string, 0, h)
	f.pickerRowsShown = 0
	if h > 1 {
		f.pickerRowsShown = h - 1
		lines = append(lines, panelPickerLines(f.picker, w, h-1, "row:"+f.ID()+":", f.palette)...)
	}
	lines = append(lines, panelText(f.panelStatus(), w))
	return panelBlock(w, h, lines...)
}

// panelStatus renders the panel's last line: SetVerdict's own live
// message (Danger) when it still applies to the current pin, the
// degraded-status hint (dim) when clauth's schema was unrecognized, the
// field's own empty-list sentence when there are no profiles at all, and
// otherwise nothing.
func (f *AccountField) panelStatus() string {
	switch {
	case f.verdictKey == f.Pin() && f.verdictText != "":
		return lipgloss.NewStyle().Foreground(f.palette.Danger).Render(f.verdictText)
	case f.degraded:
		return dimHint(f.palette).Render(accountDegradedHint)
	case len(f.profiles) == 0:
		return dimHint(f.palette).Render(accountPanelEmpty)
	default:
		return ""
	}
}

// PanelRows is the "active" row, one row per profile, and the status
// line, capped at accountPanelMaxRows.
func (f *AccountField) PanelRows() int {
	return capRows(2+len(f.profiles), accountPanelMaxRows)
}
