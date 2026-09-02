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
	"time"

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
	// accountActiveLegend is how the two read on the panel's status line,
	// which is where the explanation lives from v3 on -- see panelStatus.
	// Two spaces and no dash, the same join unavailableReasonSep makes
	// for the row stack, spelled out here because this is a legend rather
	// than a state and its reason.
	accountActiveLegend = accountActiveLabel + "  " + accountActiveHint

	// accountLiveGlyph marks clauth's own ActiveProfile on the panel (v3
	// spec §10.2). It rides in the NAME cell rather than the two-cell mark
	// column because that column is already spoken for twice over -- `!`
	// for a warned profile and `✓` for the pinned one -- and because `●`
	// and `✓` name two different rows in general, which is the whole point
	// of showing both.
	accountLiveGlyph = "●"
	// accountLiveLegend and accountLiveLegendShort are the panel's status
	// line once a `●` is on screen, widest first (v3 spec §10.2's own
	// legend, and a fallback for a panel too narrow for it). panelStatus
	// picks the first that fits, the same first-fit ladder footer.go's
	// fitFooter uses on the line below.
	accountLiveLegend      = accountLiveGlyph + " live in clauth · the pinned profile is what this session launches under"
	accountLiveLegendShort = accountLiveGlyph + " live in clauth"

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

	// accountWarnThreshold is the utilization percentage at or past which
	// a window counts as rate-limited. 95, not v2's 100 (v3 spec §10.2):
	// it is clauth's own default auto-switch trip point, and at 100 a
	// profile sitting at 98% -- a real, observed live value -- warned
	// nowhere, in either surface.
	accountWarnThreshold = 95.0

	// accountResetPrefix and accountResetDue open the panel's reset cell:
	// `in 2h11m` while the window still has time on it, `due` once it does
	// not. Relative rather than absolute (v3 spec §10.2) -- see
	// AccountField.now for how a package that performs no I/O gets a clock.
	accountResetPrefix = "in "
	accountResetDue    = "due"
)

// accountWindowLabels is the ORDERED list of clauth usage windows this
// field renders, most immediate first (v3 spec §10.1: "read windows by
// label from an ordered list rather than hard-coding 5h, so the third
// window is dropped by an explicit decision rather than by a constant
// nobody remembers").
//
// clauth reports THREE windows per profile -- "5h", "7d" and "7d fable".
// The third is left out on purpose: it measures a model this form does not
// launch, and a third gauge would cost the panel ten cells it does not
// have. v2 rendered only the first, not by decision but because a single
// constant named it and nothing else was ever read.
//
// A var rather than a const array because the row, the panel's cells and
// the panel's own COLUMN declaration all iterate it -- adding a window is
// meant to be one edit here, not four in step.
var accountWindowLabels = []string{"5h", "7d"}

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
	// accountCountOne / accountCountMany are v3 spec §8.5's readout in
	// this field's own words. Nothing filters this list, so it is always
	// the plain count -- see filterCount for the field.
	accountCountOne  = "profile"
	accountCountMany = "profiles"
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

	// now is the wall clock the panel's reset times are measured against
	// (v3 spec §10.2: "reset times are relative"). internal/form performs
	// no I/O, so it cannot read a clock of its own -- the app layer
	// supplies one alongside the status it came from, and clauth's own
	// reload on account focus (async.go's reloadClauthCmd) is what bounds
	// how stale it gets. The zero Time means "no clock supplied": the
	// reset column renders empty rather than counting down from year one.
	now time.Time

	// pinned is the profile this field would actually launch under -- ""
	// for the "active" sentinel, i.e. no pin at all. It is NOT the picker
	// cursor, and that distinction is the whole of v3 spec §10.3: before
	// it, Pin() returned wherever the cursor happened to be resting, so
	// tabbing onto this row and pressing Down once permanently pinned an
	// account and the session really did launch under it. Only commitPin
	// (Enter, or a click on a row) and SetPin write here.
	//
	// The shape is AgentField's lastConfirmed -- a stored value beside the
	// cursor, re-fed to the picker as PickerItem.Current so the panel says
	// which row it is (refreshItems). What differs is when it moves:
	// AgentField confirms on every cursor step because its two halves
	// (chips and list) are both selectors, where here a cursor step is
	// browsing and nothing more.
	pinned string

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
	f.picker.SetColumns(accountColumns()...)
	f.refreshItems()
	return f
}

// accountColumns declares the panel table's columns (v3 spec §8.1/§10.2):
// the profile name, the plan, then a gauge and a labelled percentage per
// window in accountWindowLabels, then the reset time.
//
// NOTHING flexes, which is a decision rather than an omission: the name is
// the only column a reader picks a row BY, so it must be the last to give
// up width, and the picker's fallback shrink order (right to left among
// the inflexible) is exactly that -- the reset time goes first, then the
// longer window, and the name survives longest. Flexing the name -- tried,
// and it looks wrong at 40 cells -- elides every profile name to "…" while
// "Max 20x" keeps every cell it asked for.
//
// The percentage cells carry their own window LABEL ("5h 12%") rather than
// relying on a heading row, because widgets.Picker has no heading row to
// rely on: v3 spec §10.2's mockup draws one, and the widget's public API
// offers no way to add it without measuring the columns from outside the
// picker. Self-labelling each cell is what keeps two anonymous bars from
// sitting side by side.
func accountColumns() []widgets.PickerColumn {
	cols := make([]widgets.PickerColumn, 0, 3+2*len(accountWindowLabels))
	cols = append(cols,
		widgets.PickerColumn{},                        // profile name, plus ● when clauth calls it active
		widgets.PickerColumn{Tone: widgets.ToneMuted}, // plan/tier
	)
	for range accountWindowLabels {
		cols = append(cols,
			widgets.PickerColumn{Tone: widgets.ToneMuted}, // the gauge: texture, never the loudest thing on the row
			widgets.PickerColumn{},                        // `5h 12%` -- the number is what is read
		)
	}
	// The reset time is the one column here that is genuinely optional,
	// and the only one that declares a DropBelow. At 80 cells the table
	// does not fit and the shrink loop floored this column at one, so
	// every row ended in a lone `…` -- three cells spent saying nothing,
	// which is exactly what v3 spec §8.1 refuses for the badge. Below
	// accountResetMinCells it is dropped outright instead.
	//
	// The gauges and their percentages deliberately do NOT declare one,
	// even though they are the columns a narrow panel squeezes next: a
	// bar with no number beside it states a fraction of nothing, and the
	// picker's drop pass has no way to say "these two go together". Below
	// about seventy cells they still elide. That is a real limitation and
	// it is recorded here rather than papered over.
	return append(cols, widgets.PickerColumn{Tone: widgets.ToneDim, DropBelow: accountResetMinCells})
}

// accountResetMinCells is the narrowest a reset cell can be and still
// say something: `in 45m`, the shortest text resetHint produces.
const accountResetMinCells = 6

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
//
// MOVING THE CURSOR DOES NOT PIN (v3 spec §10.3). Up/Down and the wheel
// browse; only Complete (Enter, routed here as MapKey's ActionComplete)
// and a CLICK on a row commit. A click is treated as a commit and a wheel
// tick is not, deliberately: a click names one row the way Enter does,
// where the wheel is the mouse's own spelling of Up/Down.
func (f *AccountField) Update(msg tea.Msg) tea.Cmd {
	if click, ok := msg.(tea.MouseClickMsg); ok {
		if f.pickerRowsShown > 0 {
			if _, ok := f.picker.SelectAt(click, f.pickerRowsShown, "row:"+f.ID()+":"); ok {
				f.commitPin()
			}
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

// Complete implements form.go's optional `completer` for the account zone
// (v3 spec §10.3): Enter COMMITS the profile under the cursor as the pin,
// and the form's own handleKey keeps focus here when it reports true.
//
// Returning false when the pin did not actually move is what makes a
// second Enter advance to the next field, which is exactly MapKey's own
// contract for the action ("a zone whose widget has nothing to complete
// should treat this the same as ActionAdvance"). So Enter pins, and Enter
// again moves on.
//
// keys.go routes Enter here rather than Tab: Tab is how a user LEAVES a
// row, and a Tab that pinned whatever the cursor was resting on would
// reintroduce the exact defect §10.3 exists to remove. ZoneAccount is
// still not a `isPicker()` zone for that reason.
func (f *AccountField) Complete() bool { return f.commitPin() }

// commitPin makes the row under the cursor the field's pin, reporting
// whether that actually changed anything. The "active" sentinel commits as
// "" -- the getter-boundary translation Pin() documents -- so committing on
// it is how a pin is REMOVED, which is the same gesture in the same place
// as setting one.
func (f *AccountField) commitPin() bool {
	sel, ok := f.picker.Selected()
	if !ok {
		return false
	}
	pin := sel.ID
	if pin == accountActiveID {
		pin = ""
	}
	if pin == f.pinned {
		return false
	}
	f.pinned = pin
	// Re-feed at the SAME version so the ✓ moves while the cursor stays
	// exactly where the user left it -- widgets.Picker.SetItems' own
	// same-version preserve-by-ID contract, and the same refresh
	// AgentField.refreshCurrent performs for the identical reason.
	f.refreshItems()
	return true
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
// the unpinned row reads the LIVE profile's tier and utilization, and the
// panel marks it `●` (v3 spec §10.2) -- neither of which the picker's item
// set nor Pin() can answer.
//
// now is the wall clock the panel's reset times are measured against (v3
// spec §10.2). It rides alongside the status rather than being read here
// because internal/form performs no I/O at all; see AccountField.now.
func (f *AccountField) SetProfiles(status clauth.Status, now time.Time) {
	f.degraded = status.Degraded
	f.profiles = append([]clauth.Profile(nil), status.Profiles...)
	f.activeProfile = status.ActiveProfile
	f.now = now
	f.refreshItems()
}

// refreshItems re-feeds the picker from the field's CURRENT state -- the
// "active" sentinel row plus one row per retained profile. Called at
// construction (so the field is never left with an empty picker before the
// first SetProfiles: Selected()/Pin() always have a row to answer from),
// by SetProfiles, and by commitPin, which needs only the ✓ to move.
//
// Always version 0: this field's picker version is held FIXED, so every
// call after the first is a same-version refresh and preserves the cursor
// by row ID (widgets.Picker.SetItems' own contract). That is what spec
// §8's "clauth profiles ... form-open + on account focus" polling needs --
// a re-poll must not bounce the user's cursor back to the top -- and what
// lets commitPin repaint the marks without moving anything.
func (f *AccountField) refreshItems() {
	f.picker.SetItems(0, f.buildItems())
}

// buildItems builds the account picker's full item list: the "active"
// sentinel row first, then one row per profile (name-only when the status
// was degraded -- spec §11: "schema != 1 -> degrade to name-only
// entries"). A profile with an empty Name, or a Name already seen, is
// skipped entirely -- clauth.Profile.Name is unvalidated external JSON
// (internal/clauth/status.go's ParseStatus enforces neither
// non-emptiness nor uniqueness), and widgets.PickerItem.ID is built
// directly from it (see profileItem), so an empty or duplicate name would
// otherwise either collide with the "active" sentinel's own "" ==
// no-pin contract (Pin() below) or make widgets.Picker's first-match-wins
// ID lookup pick an arbitrary one of the duplicates -- the same bug class
// field_worktree.go's refreshBaseItems guards against for base refs via
// an identical seen map, and field_issue.go's refreshItems guards against
// for issues with an empty Identifier.
func (f *AccountField) buildItems() []widgets.PickerItem {
	items := make([]widgets.PickerItem, 0, len(f.profiles)+1)
	// The sentinel carries only the word: v3 spec §8.1 measures every
	// column over the whole set, so a sentence in cell 1 would set the
	// PLAN column's width for every profile row beneath it and blow the
	// table apart. Its explanation moved to the panel's own status line
	// (panelStatus), which the field already reserves and which is empty
	// in exactly the state the explanation is for -- the same place v3
	// spec §10.2 puts the panel's legend.
	//
	// Current on this row is not decoration: with no pin, "use whatever
	// profile is live" IS the field's value, and the ✓ is the only thing
	// that says so once the cursor has wandered off it (v3 spec §10.3).
	items = append(items, widgets.PickerItem{
		ID:      accountActiveID,
		Cells:   []string{accountActiveLabel},
		Current: f.pinned == "",
	})
	seen := map[string]bool{accountActiveID: true}
	for _, p := range f.profiles {
		if p.Name == "" || seen[p.Name] {
			continue
		}
		seen[p.Name] = true
		items = append(items, f.profileItem(p))
	}
	return items
}

// profileItem builds one profile's picker row (v3 spec §10.2): the name --
// carrying `●` when clauth reports it as the ACTIVE profile -- the plan,
// then a gauge and a labelled percentage per window in
// accountWindowLabels, then when that window reports one, how long until
// it resets. Name-only when the status was degraded (see buildItems).
//
// A rate-limited (any usage window at or past accountWarnThreshold) or
// auth-failed (AuthStatus set and not "ok") profile carries a warning
// Marker plus the warning itself as its badge -- spec §6 field 7:
// "Rate-limited or auth-failed profiles are selectable but visibly
// marked", and spec §16 non-goal 9: v1 stops at this inline marker, no
// blocking modal. The badge is toned by which condition fired, which the
// single `!` cannot say: an auth failure is Danger (the profile cannot be
// used at all) where a rate limit is Warning (it can, later) -- the same
// split rowParts draws the stack row's own state word with.
//
// Current marks the PINNED profile and `●` marks the LIVE one, and they
// are two different rows in general -- which is the point of drawing both
// (v3 spec §10.3). Before Pin() became a deliberate commit they would have
// been the same row by construction, which is why AccountField was left
// out of §8.2's first Current pass.
func (f *AccountField) profileItem(p clauth.Profile) widgets.PickerItem {
	current := p.Name == f.pinned
	if f.degraded {
		return widgets.PickerItem{ID: p.Name, Cells: []string{p.Name}, Current: current}
	}

	warning := accountWarning(p)
	marker := ""
	tone := widgets.ToneWarning
	if warning != "" {
		marker = "!"
		if warning == accountWarnAuthFailed {
			tone = widgets.ToneDanger
		}
	}

	name := p.Name
	if p.Name == f.activeProfile {
		name += " " + accountLiveGlyph
	}

	cells := make([]string, 0, 3+2*len(accountWindowLabels))
	cells = append(cells, name, p.Tier)
	for _, label := range accountWindowLabels {
		w, ok := accountWindow(p, label)
		if !ok {
			// Two empty cells rather than a placeholder: a window NO
			// profile reports then measures zero and its columns vanish
			// entirely (widgets.rowLayout.left skips a zero-width column
			// and its gap), instead of two dead columns of "7d —".
			cells = append(cells, "", "")
			continue
		}
		cells = append(cells, gaugeBar(w.UtilizationPct/100, gaugeWidth), accountWindowPercent(label, w.UtilizationPct))
	}

	return widgets.PickerItem{
		ID:        p.Name,
		Cells:     append(cells, f.resetHint(p)),
		Badge:     warning,
		BadgeTone: tone,
		Marker:    marker,
		Current:   current,
	}
}

// accountWarning reports the profile's own warning text -- "" when
// neither condition applies.
func accountWarning(p clauth.Profile) string {
	if p.AuthStatus != "" && p.AuthStatus != "ok" {
		return accountWarnAuthFailed
	}
	for _, w := range p.Windows {
		if w.UtilizationPct >= accountWarnThreshold {
			return accountWarnRateLimited
		}
	}
	return ""
}

// accountWindow returns the profile's window carrying label, or
// (zero, false) when clauth reported no such window -- an empty Windows
// slice is a real, observed clauth shape (see
// internal/clauth/status_test.go's stale-status-file fixture), and so is a
// profile that reports some of the three but not all.
func accountWindow(p clauth.Profile, label string) (clauth.Window, bool) {
	for _, w := range p.Windows {
		if w.Label == label {
			return w, true
		}
	}
	return clauth.Window{}, false
}

// accountWindowPercent renders one window the way BOTH surfaces write it:
// the window's own label and a whole number, `5h 12%`. The label rides in
// the cell because widgets.Picker has no heading row -- see
// accountColumns.
func accountWindowPercent(label string, pct float64) string {
	return fmt.Sprintf("%s %s", label, accountPercent(pct))
}

// resetHint renders how long until this profile's most immediate reported
// window resets, `in 2h11m` (v3 spec §10.2). "" when no window in
// accountWindowLabels reports a reset time at all -- clauth reports
// `"resets_at": null` for a window with none scheduled -- or when no clock
// was supplied (see AccountField.now).
//
// The FIRST window in accountWindowLabels that names one wins, i.e. the
// 5-hour window's, falling back to the weekly one: with a single unlabelled
// column the honest thing to show is the reset a user is actually waiting
// on, and that is the short window in every case where both are running.
func (f *AccountField) resetHint(p clauth.Profile) string {
	if f.now.IsZero() {
		return ""
	}
	for _, label := range accountWindowLabels {
		w, ok := accountWindow(p, label)
		if !ok || w.ResetsAt == nil {
			continue
		}
		return accountResetText(*w.ResetsAt, f.now)
	}
	return ""
}

// accountResetText renders resetsAt relative to now: `in 4d14h`,
// `in 2h11m`, `in 45m`, or `due` for a window whose reset has already
// come round (clauth's feed can be a couple of minutes behind the clock,
// and a negative countdown says nothing a reader can use).
//
// The DAY tier is here because the live panel produced `in 110h07m` on
// the author's own account the first time this was run against real
// clauth data: accountWindowLabels' second entry is a SEVEN-DAY window,
// so a reset four days out is an ordinary reading and not a corner. Two
// significant units is the whole rule -- days and hours, or hours and
// minutes, or minutes -- because a third says nothing a reader waiting on
// a quota can act on.
//
// The lower unit is zero-padded so `2h05m` never appears beside `2h11m`
// and the readings sort by eye. It does NOT make every reading the same
// width -- `in 10h00m` is a cell wider than `in 2h47m` -- and it does not
// need to: the column is measured over the whole filtered set, so the
// widths differ per PROFILE, which is stable, rather than per keystroke.
func accountResetText(resetsAt, now time.Time) string {
	d := resetsAt.Sub(now).Round(time.Minute)
	if d <= 0 {
		return accountResetDue
	}
	if days := int(d / (24 * time.Hour)); days > 0 {
		return fmt.Sprintf("%s%dd%02dh", accountResetPrefix, days, int(d/time.Hour)%24)
	}
	if h := int(d / time.Hour); h > 0 {
		return fmt.Sprintf("%s%dh%02dm", accountResetPrefix, h, int(d/time.Minute)%60)
	}
	return fmt.Sprintf("%s%dm", accountResetPrefix, int(d/time.Minute))
}

// SetPin makes pin the field's committed pin and moves the picker's cursor
// onto it -- e.g. to apply spec §12's config.toml `[clauth] default` value
// at form construction. "" and "active" (the config's own documented
// sentinel for "don't pin -- use whatever profile is live") are both
// no-ops: an unpinned field is what a fresh one already is, so there is
// nothing to apply for either. A pin naming a profile not present in the
// current item set (e.g. a stale or typo'd config value) is also a no-op --
// never guess.
//
// It is the ONE writer of the pin besides commitPin, and deliberately so:
// this is a caller stating a value, where every user gesture that could be
// mistaken for browsing goes through commitPin instead (v3 spec §10.3).
//
// Added in Task 20b (the app layer) alongside PlacementField.SetValue --
// see field_title.go's SetTitle doc comment for the fuller writeup of
// this class of gap: a config-derived default value with no way to
// pre-select the field it configures.
func (f *AccountField) SetPin(pin string) {
	if pin == "" || pin == "active" {
		return
	}
	if !f.picker.SelectID(pin) {
		return
	}
	f.pinned = pin
	f.refreshItems()
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

// Pin returns the profile the user has DELIBERATELY committed to, or ""
// when none is (spec: "don't pin — use whatever profile is live") -- the
// getter-boundary translation field_worktree.go's Base() documents for the
// identical baseHeadID sentinel shape.
//
// It is not the picker cursor, and v3 spec §10.3 is the whole reason: this
// value reaches plan.Input.AccountPin and the session really does launch
// under `clauth <it>`, so browsing the list with ↑↓ must not write it. See
// AccountField.pinned and commitPin.
func (f *AccountField) Pin() string { return f.pinned }

// --- the row and its panel ------------------------------------------------

// Label is v2's row label (v2 spec §6's field table).
func (f *AccountField) Label() string { return accountRowLabel }

// Row is v3 spec §10.1's `personal · Max 20x · 5h 0% · 7d 14%`: the pinned
// profile's name, or `active` and the LIVE profile's numbers when nothing
// is pinned, then its plan, then one part per window in
// accountWindowLabels. A window at or past accountWarnThreshold is drawn
// in Warning.
//
// v2 wrote `personal · Max 20x · ok` instead, because it showed the
// literal AuthStatus for a pinned profile and the utilization only for an
// unpinned one. That is backwards, and §10.1 says why: **`ok` is the state
// that needs no words.** So the auth status appears only when it is NOT
// "ok", as `sign in again` in Danger, and the numbers are shown either
// way -- they are the reason anyone looks at this row.
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

	parts := f.rowParts(profile)
	var tail strings.Builder
	for _, part := range parts {
		tail.WriteString(accountRowSep)
		tail.WriteString(part.text)
	}
	// Every part after the name is short and fixed; the profile NAME is the
	// one that can run long, so it is the one that gives up cells.
	budget := w - lipgloss.Width(tail.String())
	if budget < 1 {
		return fitLine(text.Render(keepHead(name+tail.String(), w)), w)
	}
	var b strings.Builder
	b.WriteString(text.Render(keepHead(name, budget)))
	for _, part := range parts {
		b.WriteString(text.Render(accountRowSep))
		b.WriteString(part.style.Render(part.text))
	}
	return fitLine(b.String(), w)
}

// accountRowPart is one `·`-separated piece of the stack row past the
// profile name, with the color it is drawn in. A slice of these is what
// lets Row measure the whole tail before deciding how many cells the name
// may keep, without building the styled string twice.
type accountRowPart struct {
	text  string
	style lipgloss.Style
}

// rowParts builds the row's tail (v3 spec §10.1): the plan, then one part
// per reported window in accountWindowLabels, then -- only when it is not
// "ok" -- the auth status, as `sign in again` in Danger.
//
// A window at or past accountWarnThreshold is drawn in Warning, which is
// the surface the threshold change is most visible on: at v2's 100 a
// profile sitting at 98% read exactly like one sitting at 3%.
//
// Anything clauth did not report is simply absent rather than rendered as
// an em dash: v2 built this string by hand in two places with two
// different rules and left a dangling `· ` behind an empty AuthStatus
// (v3 spec §10.3's closing note). One builder, and a part that has nothing
// to say does not get a separator.
func (f *AccountField) rowParts(p clauth.Profile) []accountRowPart {
	plain := lipgloss.NewStyle().Foreground(f.palette.Text)
	parts := make([]accountRowPart, 0, 2+len(accountWindowLabels))
	if p.Tier != "" {
		parts = append(parts, accountRowPart{text: p.Tier, style: plain})
	}
	for _, label := range accountWindowLabels {
		w, ok := accountWindow(p, label)
		if !ok {
			continue
		}
		style := plain
		if w.UtilizationPct >= accountWarnThreshold {
			style = lipgloss.NewStyle().Foreground(f.palette.Warning)
		}
		parts = append(parts, accountRowPart{text: accountWindowPercent(label, w.UtilizationPct), style: style})
	}
	if p.AuthStatus != "" && p.AuthStatus != "ok" {
		parts = append(parts, accountRowPart{
			text:  accountRowAuthFailed,
			style: lipgloss.NewStyle().Foreground(f.palette.Danger),
		})
	}
	return parts
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

// accountPercent renders a utilization as both surfaces write it: a whole
// number and a percent sign, no window label.
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
	lines = append(lines, panelStatusLine(f.panelStatus(panelInner(w)), f.filterCount(), w, f.palette))
	return panelBlock(w, h, lines...)
}

// filterCount is v3 spec §8.5's readout for this field. Nothing filters
// this list, so it is always the plain count and never a ratio; it is
// here because the panel caps at accountPanelMaxRows and can therefore
// scroll, and a scrollbar with no count says how far you are through a
// list of unstated length.
//
// It counts PROFILES, not picker rows: the `active` sentinel is a choice,
// not an account, so three profiles must not read `4 profiles`.
func (f *AccountField) filterCount() string {
	return filterCount(len(f.profiles), len(f.profiles), accountCountOne, accountCountMany)
}

// panelStatus renders the panel's last line at inner cells wide:
// SetVerdict's own live message (Danger) when it still applies to the
// current pin, the degraded-status hint (dim) when clauth's schema was
// unrecognized, the field's own empty-list sentence when there are no
// profiles at all, and otherwise the panel's legend.
//
// That last case used to be nothing at all, and the sentence used to be
// the sentinel row's own hint. v3 spec §8.1 measures a picker's columns
// over the whole set, so a row carrying prose sets the width of a column
// every other row has to live in -- and this line was reserved, empty,
// directly beneath it. It is the same move v3 spec §10.2 makes for the
// panel's `●` legend.
//
// The legend itself depends on whether a `●` is actually on screen (v3
// spec §10.2 writes it for the case where one is): with no live profile
// among the rows there is no glyph to explain, and the line goes back to
// saying what the `active` row means.
func (f *AccountField) panelStatus(inner int) string {
	switch {
	case f.verdictKey == f.Pin() && f.verdictText != "":
		return lipgloss.NewStyle().Foreground(f.palette.Danger).Render(f.verdictText)
	case f.degraded:
		return dimHint(f.palette).Render(accountDegradedHint)
	case len(f.profiles) == 0:
		return dimHint(f.palette).Render(accountPanelEmpty)
	default:
		return dimHint(f.palette).Render(f.panelLegend(inner))
	}
}

// panelLegend picks the widest legend that fits inner cells, first-fit
// down a two-rung ladder and then the `active` sentence -- the same
// degradation footer.go's fitFooter runs on the line below, spelled out
// here rather than shared because that one is crossed with a constant tail
// this line has no equivalent of.
func (f *AccountField) panelLegend(inner int) string {
	if _, live := f.profileByName(f.activeProfile); live {
		for _, rung := range []string{accountLiveLegend, accountLiveLegendShort} {
			if lipgloss.Width(rung) <= inner {
				return rung
			}
		}
	}
	return accountActiveLegend
}

// PanelRows is the "active" row, one row per profile, and the status
// line, capped at accountPanelMaxRows.
func (f *AccountField) PanelRows() int {
	return capRows(2+len(f.profiles), accountPanelMaxRows)
}
