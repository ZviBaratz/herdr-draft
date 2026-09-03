// field_title.go is written fresh for this task, per the task-17 brief's
// own provenance note ("TitleField and PlacementField are written fresh")
// -- it is NOT derived from atrium (github.com/ZviBaratz/atrium).
// Atrium's own title field lives in ui/overlay/textInput.go, which is not
// on the audited clean-file list and was never opened for this task; only
// this package's own lineInput (lineinput.go, itself written directly
// against bubbles/v2's textinput, not ported from Atrium) is reused here.
package form

import (
	"strconv"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

const (
	// titleCharLimit is spec §6 field 3's "32-rune cap".
	titleCharLimit = 32
	// titleRowLabel is v2's row label (v2 spec §6): lowercase, no colon,
	// no padding -- the form pads it into the label column
	// (rowlayout.go's labelColWidth).
	titleRowLabel = "title"
	// titleRowUnset is v2 spec §6's Unset cell for this row. It matches
	// the v1 placeholder the wrapped lineInput already carries, so the
	// empty field reads the same whether the row is showing the editor or
	// the resting value.
	titleRowUnset = "untitled"

	// titleSessionsHeading opens v3 spec §9's list. It names WHEN the
	// list was read, not what it is, and §9 requires exactly that: the
	// data is fetched once at Bootstrap and never refreshed, so a session
	// created elsewhere while this form is open is missing from it. A
	// heading reading "sessions that already exist" would state something
	// the panel cannot know.
	titleSessionsHeading = "sessions open when this form did"
	// titleSessionOne / titleSessionMany are the heading's own count (v3
	// spec §8.5's readout, on the heading rather than a status line --
	// this panel has no status line, and §4's mockup puts it here).
	//
	// "session", not "workspace", although a herdr WORKSPACE is what these
	// rows are and what §4's mockup counts. The row two lines up says
	// `new session` and everything this form creates it calls a session;
	// two words for one thing on adjacent lines is exactly what v2 §3
	// rule 5's copy pass exists to remove.
	titleSessionOne  = "session"
	titleSessionMany = "sessions"
	// titleSessionsMaxRows caps how many session rows PanelRows will ask
	// for. Fifteen is panelCapRows -- the region cannot show more than
	// that anyway (v3 spec §7.2), so a larger number would only make
	// PanelRows lie about a height it can never be given.
	titleSessionsMaxRows = panelCapRows
)

// Session is one row of v3 spec §9's list: a herdr workspace that existed
// when this form opened.
//
// It is a FORM-LOCAL value type, not herdrc.WorkspaceInfo, and that is
// CLAUDE.md's rule rather than a preference: "internal/form ... No I/O, no
// knowledge of herdr/git/Linear." The app maps its own herdrc types down
// to this on the way in, the same direction every other value crosses that
// boundary.
type Session struct {
	// Label is the workspace's name -- what a colliding title would
	// collide with (async.go's workspaceLabelTaken compares exactly this).
	Label string
	// Status is herdr's own agent-status word ("idle", "working",
	// "blocked", ""), passed through rather than re-spelled: it is what
	// herdr's own sidebar shows for the same workspace.
	Status string
	// Panes is how many panes the workspace holds.
	Panes int
	// Repo is the repository the workspace is open on, or "" for one
	// herdr reports no worktree context for.
	//
	// v3 spec §9's mockup draws a BRANCH here (`zvi/v3-palette`). There
	// is no branch to draw: `herdr workspace list` returns a `worktree`
	// object of {repo_key, repo_name, repo_root, checkout_path,
	// is_linked_worktree} and no branch name anywhere, verified against
	// live output. A linked worktree's checkout_path basename is derived
	// from the branch but slash-mangled -- `zvi-clear-stty-size` for
	// `zvi/clear-stty-size` -- so drawing it as a branch would be a small
	// lie, and §9 forbids the one thing that would get the real name
	// ("no new I/O, no new subprocess").
	//
	// The repository is the honest column and the more useful one here
	// anyway: what a reader about to create a session needs from this
	// list is whether one is already open on the project they are
	// pointing at.
	Repo string
}

// TitleField is the form's Title Section (spec §6 field 3): a single-line,
// 32-rune-capped text field whose typed value doubles as the session's
// branch/title (spec §6's "quick-create" contract, wired through
// form.go's own titleValuer capability so an Enter from a non-empty Title
// submits the form -- see form.go's Section doc comment).
//
// The row is the editor (a title's editing surface is one line, so it
// needs no panel to type in) and the panel is the verdict.
type TitleField struct {
	palette theme.Palette
	input   *lineInput
	focused bool
	touched bool

	// verdictKey/verdictText are SetVerdict's own staleness guard, the
	// same "clears the moment the underlying value changes" discipline
	// field_dir.go's DirField.validityPath uses for its inline marker: a
	// verdict computed for a title the user has since edited away from
	// must stop rendering, without SetVerdict's caller needing a separate
	// Clear call to make that happen -- verdictLine (below) only shows
	// verdictText when verdictKey still equals the CURRENT Value().
	verdictKey  string
	verdictText string

	// sessions is v3 spec §9's list: the workspaces that existed when the
	// form opened, pushed in by the app (SetSessions) from the
	// WorkspaceList call Bootstrap already makes and already retains. No
	// new I/O, no new state file, nothing new collected.
	sessions []Session
	// list renders those sessions. It is a widgets.Picker because §9 says
	// "it is a list in a panel, so it inherits §8 wholesale. It is not a
	// new widget" -- and it is SetCursorless, because nothing here can be
	// chosen and a cursor row that says otherwise is a promise the form
	// cannot keep.
	list *widgets.Picker
}

// NewTitleField returns an empty, blurred TitleField styled from palette.
func NewTitleField(palette theme.Palette) *TitleField {
	// The ground is ActiveRowBG, not PanelBG: Row renders this input
	// only while the field is focused, and a focused stack row is filled
	// ActiveRowBG end to end (form.go's compose loop).
	input := newLineInput(palette, titleCharLimit, palette.ActiveRowBG)
	input.SetPlaceholder("untitled")
	f := &TitleField{palette: palette, input: input, list: widgets.NewPicker(palette)}
	f.list.SetCursorless(true)
	// Label, status, panes, branch. Nothing flexes, and the shrink order
	// (right to left among the inflexible) is already the right one here:
	// the branch goes first and the label -- the only column a colliding
	// title is compared against -- survives longest.
	f.list.SetColumns(
		widgets.PickerColumn{},
		widgets.PickerColumn{Tone: widgets.ToneMuted},
		widgets.PickerColumn{Tone: widgets.ToneMuted, DropBelow: titleSessionPanesMinCells},
		widgets.PickerColumn{Tone: widgets.ToneMuted, DropBelow: titleSessionRepoMinCells},
	)
	return f
}

// titleSessionPanesMinCells and titleSessionRepoMinCells are the
// narrowest those columns are worth drawing in ("1 pane"; a repository
// name short enough to still name something). Below them the column is
// dropped outright rather than elided to a lone `…` -- see
// PickerColumn.DropBelow. The label and the status never declare one: the
// label is what a collision is about, and the status word is the shortest
// column there is.
const (
	titleSessionPanesMinCells = 6
	titleSessionRepoMinCells  = 8
)

// ID identifies this Section for form.go's zoneFor (see form.go's Section
// doc comment's ID() convention).
func (f *TitleField) ID() string { return "title" }

// Enabled reports that Title is always present -- spec §6 field 3 has no
// precondition that could ever make it unavailable.
func (f *TitleField) Enabled() bool { return true }

// Focus gives the field input focus, returning the wrapped lineInput's own
// blink tea.Cmd.
func (f *TitleField) Focus() tea.Cmd {
	f.focused = true
	return f.input.Focus()
}

// Blur removes input focus.
func (f *TitleField) Blur() {
	f.focused = false
	f.input.Blur()
}

// Update forwards msg to the wrapped lineInput -- see keys.go's own
// grammar boundary: MapKey already intercepts Tab/Enter/Esc/Ctrl+S/Ctrl+R
// for ZoneTitle before this is ever called, so only genuine text-editing
// messages (typed runes, backspace, arrow/word movement, paste) reach
// here. touched is set to true only when this call actually CHANGES the
// value -- comparing Value() before and after, rather than assuming every
// Update call is an edit -- so a message that moves the cursor without
// changing content (Left/Right/Home/End), or a non-edit message forwarded
// here for some other reason (e.g. a cursor-blink tick), does not
// spuriously flip Touched() -- see field_dir.go's DirField.Update for the
// same discipline applied to picker cursor-reset avoidance.
func (f *TitleField) Update(msg tea.Msg) tea.Cmd {
	before := f.input.Value()
	cmd := f.input.Update(msg)
	if f.input.Value() != before {
		f.touched = true
		// The collision mark is a function of the typed text, so it is
		// recomputed on the same before/after comparison that already
		// decides `touched` rather than on a fresh signal -- the pattern
		// every other field's Update uses internally, and the one
		// app.go's reactToChanges follows one layer up.
		f.refreshSessions()
	}
	return cmd
}

// Value returns the field's current typed text -- also TitleField's
// titleValuer implementation (form.go's optional capability interface),
// consulted by zoneFor for FocusZone.TitleEmpty.
func (f *TitleField) Value() string { return f.input.Value() }

// Touched reports whether the user has typed into this field since
// construction (never reset once true -- there is no untouch operation;
// a caller that rebuilds the form for spec §6's Ctrl+R Ctrl+R clear
// gesture is expected to construct a fresh TitleField instead).
func (f *TitleField) Touched() bool { return f.touched }

// SetTitle sets the input's value, honoring the same touched-vs-preselected
// rule field_worktree.go's WorktreeField.SetBranch documents: when seeded
// is true, this is a SUGGESTION (e.g. a chosen Linear issue's own title)
// applied only if the user has not yet typed into the field themselves
// (Touched() == false) -- once touched, every further seeded call is
// silently ignored, so a later re-suggestion never clobbers the user's own
// edit. seeded == false is a hard, authoritative set that always applies
// and clears touched, so a subsequent seed can apply again.
//
// Added in Task 20 (the app layer) alongside WorktreeField.SetBranch and
// PromptField.SetValue -- IssueChosenMsg's own doc comment already
// documents the app layer calling "TitleField/WorktreeField/PromptField's
// own setters", but Title had no programmatic setter at all until this one;
// see task-20-report.md for the full write-up of this gap and why the fix
// mirrors SetBranch's existing, already-reviewed discipline rather than
// inventing a new one.
func (f *TitleField) SetTitle(v string, seeded bool) {
	if seeded && f.touched {
		return
	}
	f.input.SetValue(v)
	if !seeded {
		f.touched = false
	}
}

// --- the row and its panel ------------------------------------------------
//
// Label is v2's row label (v2 spec §6's field table).
func (f *TitleField) Label() string { return titleRowLabel }

// Row renders the title's value cell: the live editor while focused (v2
// spec §5 -- "the row is the EDITOR for a field whose editing surface is
// a single line"), the typed value otherwise, and a dim "untitled" when
// there is nothing to show. A too-long title elides at its TAIL, keeping
// the head: a title is read left to right and its first words are what
// identify it.
//
// No verdict is appended here, deliberately: v2 spec §6 puts verdicts in
// the panel precisely so a recomputing verdict cannot shift text under
// the cursor.
func (f *TitleField) Row(w int) string {
	if w < 1 {
		w = 1
	}
	if f.focused {
		return f.input.View(w)
	}
	if v := f.Value(); v != "" {
		return fitLine(lipgloss.NewStyle().Foreground(f.palette.Text).Render(keepHead(v, w)), w)
	}
	return fitLine(dimText(f.palette).Render(keepHead(titleRowUnset, w)), w)
}

// Panel renders the verdict line at FULL width -- the whole point of
// moving it off the row. v1 clamped it to 21 cells (titleVerdictMaxCells,
// now deleted) so it could not collide with the typed text sharing its
// section; a full-width line of its own has nothing to collide with, and
// "branch: zvi/fix-login-redirect-loop" survives whole.
//
// A verdict computed for a title the user has since edited away from is
// not rendered at all, the same staleness-by-comparison guard
// verdictKey's own doc comment describes for v1.
//
// Beneath it, v3 spec §9's session list, and the reason this Section
// grew a panel worth the name. PanelRows() used to be 1 against
// issuePanelMaxRows' 24, and because the region is fixed so the footer
// does not move as focus travels, NO popup height served both: sized for
// the picker, the opening screen -- the first thing every user sees --
// was a fifteen-row hole. #22 bounded that hole; only content fills it.
//
// A blank line separates the two halves because they answer different
// questions: the verdict is about the title being typed, the list is
// about the world it is being typed into.
func (f *TitleField) Panel(w, h int) string {
	if h < 1 {
		h = 1
	}
	text := ""
	if f.verdictKey == f.Value() {
		text = f.verdictText
	}
	lines := []string{panelText(dimHint(f.palette).Render(text), w)}
	// The list is what a short window gives up, not the verdict: the
	// verdict is about the keystroke the user just made.
	if h >= 3 && len(f.sessions) > 0 {
		lines = append(lines, panelText("", w), f.sessionsHeading(w))
		rows := h - len(lines)
		if rows > 0 {
			// No zone prefix: the rows are informational, so they claim
			// no mouse zones. A click that resolves to a row nothing can
			// select is the same false promise the cursor would be.
			lines = append(lines, panelPickerLines(f.list, w, rows, "", f.palette)...)
		}
	}
	return panelBlock(w, h, lines...)
}

// sessionsHeading is the list's own heading line, with v3 spec §8.5's
// count right-flushed onto it. There is no status line to put the count
// on here -- this panel has none -- and §4's mockup puts it on the
// heading, which is the only line that is about the list as a whole.
func (f *TitleField) sessionsHeading(w int) string {
	count := filterCount(len(f.sessions), len(f.sessions), titleSessionOne, titleSessionMany)
	return panelStatusLine(dimHint(f.palette).Render(titleSessionsHeading), count, w, f.palette)
}

// PanelRows is the verdict line, plus -- once the app has pushed a
// session list in -- a blank, the heading, and one row per session.
//
// The verdict line is reserved unconditionally even when no verdict
// applies, which is what keeps the panel's height (and therefore the
// footer's position) independent of whether the app layer has answered
// yet. With no sessions at all it is the whole panel, exactly as before
// v3 spec §9.
func (f *TitleField) PanelRows() int {
	// No other sessions open is the whole panel gone, not a heading over
	// nothing: with one workspace (the one this popup is in, which the app
	// drops) there is no landscape to show, and a line saying so would be
	// chrome explaining its own absence.
	if len(f.sessions) == 0 {
		return 1
	}
	return capRows(3+len(f.sessions), 3+titleSessionsMaxRows)
}

// SetSessions records the workspaces that existed when the form opened
// (v3 spec §9). The app layer pushes them in, from the WorkspaceList call
// Bootstrap already makes and already retains -- no new I/O, no new
// subprocess, no new state file, nothing new collected about the user.
//
// It is a setter on the CONCRETE type rather than anything on Section,
// which is how every other candidate list reaches a field (form.go's
// Section doc comment: the interface deliberately does not expose them).
//
// The data is fetched once and goes stale if a session is created
// elsewhere while this form is open. That is accepted -- the panel is
// informational and the authoritative duplicate check still runs at
// submit -- and titleSessionsHeading is worded so the panel does not
// claim otherwise.
func (f *TitleField) SetSessions(sessions []Session) {
	f.sessions = append([]Session(nil), sessions...)
	f.refreshSessions()
}

// refreshSessions rebuilds the list's rows, including the collision mark,
// which is why it runs on every keystroke and not only on SetSessions.
//
// The mark is the whole reason this list beats a bare count: the app
// already computes labelTaken from this same data (async.go's
// workspaceLabelTaken) but only surfaces it as a verdict AFTER the user
// has typed a colliding title. Marking the row shows the collision coming
// -- and shows which session it is with, which the verdict never could.
//
// The comparison is workspaceLabelTaken's own, exact equality on the
// label, so the two cannot disagree about what a collision is.
func (f *TitleField) refreshSessions() {
	title := f.Value()
	items := make([]widgets.PickerItem, 0, len(f.sessions))
	for _, s := range f.sessions {
		if s.Label == "" {
			continue // an unlabelled workspace names nothing and collides with nothing
		}
		panes := ""
		if s.Panes == 1 {
			panes = "1 pane"
		} else if s.Panes > 1 {
			panes = strconv.Itoa(s.Panes) + " panes"
		}
		marker := ""
		if title != "" && s.Label == title {
			marker = markerWarning
		}
		items = append(items, widgets.PickerItem{
			ID:     s.Label,
			Cells:  []string{s.Label, s.Status, panes, s.Repo},
			Marker: marker,
		})
	}
	// Version 0 always: this list is never filtered and never re-ranked,
	// so every call is a same-version refresh and the picker preserves by
	// ID -- which matters only for the scroll offset, since nothing here
	// has a cursor to move.
	f.list.SetItems(0, items)
}

// SetVerdict records the app layer's own live-validation message for the
// title text that was current when it was computed (key): a short note
// (e.g. the branch name a title would produce, or a "title already in
// use" warning) shown on the reserved verdict line. A later call whose key
// no longer matches Value() (the title has since changed) is stored but
// never rendered -- see verdictKey's own doc comment; there is no separate
// Clear method, matching DirField's SetValidity's identical
// staleness-by-comparison design.
func (f *TitleField) SetVerdict(key, text string) {
	f.verdictKey = key
	f.verdictText = text
}
