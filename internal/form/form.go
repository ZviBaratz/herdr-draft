// Package form is herdr-draft's form root: spec §4's "form is a dumb
// view" -- fields, focus ring, and rendering only, no I/O, every verdict
// and candidate list pushed in from the app layer via setters a concrete
// field Section exposes on its own concrete type (not through this
// package's own Section interface; see Section's doc comment).
//
// form.go itself is not a port of any single Atrium file -- Atrium's
// TextInputOverlay owns its own concrete, named widget fields directly
// (titleInput, directoryPicker, textarea, ...); herdr-draft's Section
// interface is the abstraction that stands between this package and
// Tasks 17-18's concrete field widgets, and Atrium has no equivalent of
// it. What IS ported (focus.go, sizes.go) and what's a from-scratch
// reimplementation (footer.go) are documented in their own file headers;
// this file's own borrowing -- herdr's action-button convention -- is
// documented at createButton below.
package form

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// Task 21's zone-ID scheme (github.com/lrstanley/bubblezone/v2, see
// widgets/zones.go's own package doc for why Zones is a package-level,
// non-global Manager): every stack row registers a "field:<id>" zone in
// compose (below), the whole detail region registers one "panel" zone,
// the Create section registers "button:create" (renderFooter) since its
// click semantics differ from every other section (submit, not focus),
// the cancel button beside it registers "button:cancel" (v2 spec §7's
// zone list names both) even though no Section owns it -- it is esc's
// own affordance, not a focus stop -- and a concrete field's own
// chip/picker rows register
// "chip:<sectionID>:<chipID>"/"row:<sectionID>:<n>" zones themselves via
// widgets.ChipRow.MarkedView/widgets.Picker.MarkedView (see e.g.
// field_placement.go's PlacementField.Panel/Update). createSectionID
// names the one Section ID compose/handleMouseClick both special-case
// (New's own internal Create section, always last -- see New's doc
// comment); zoneCreateButton/zoneFieldPrefix/zonePanel are these constant
// name fragments, shared so a typo in one can't silently desync a Mark
// call from the Get call that is supposed to find it.
//
// The per-row zone is "field:<id>" rather than v1's "section:<id>"
// deliberately: "row" now means a line of the stack, and "row:<id>:<n>"
// already means a picker row INSIDE a panel, so a zone called
// "section:<id>" read like the wrong one of the two.
//
// The "panel" zone is the one click branch with no v1 equivalent
// (handleMouseClick): a click inside it forwards to the FOCUSED section
// without changing focus, which is what lets the chip and picker-row
// zones nested inside a panel resolve at all -- the panel belongs to the
// focused field by construction, so there is nothing for such a click to
// re-focus.
const (
	createSectionID  = "create"
	zoneCreateButton = "button:create"
	zoneCancelButton = "button:cancel"
	zoneFieldPrefix  = "field:"
	zonePanel        = "panel"
)

// Section is one field of the form: EXACTLY ONE LINE in the row stack,
// plus the detail panel it shows while it holds focus (v2 spec §5). The
// form's eight fields plus this package's own internal Create button, all
// deliberately opaque beyond this interface -- the focus ring, the
// composition and rowlayout.go's frame arithmetic work identically over
// every Section, whether it is one of the real fields or a test double,
// without knowing which.
//
// Setter methods a concrete field needs (SetItems, SetChips, ...) are NOT
// part of this interface: the app layer holds each concrete Section by
// its own concrete type to drive it, and this package only ever holds the
// slice as []Section.
//
// ID() convention: a concrete field Section's ID() should return the
// lowercase zone name v2 spec §6's field order uses -- "issue", "title",
// "prompt", "dir", "worktree", "placement", "agent", "account" (this
// package's own internal Create section uses "create") -- so zoneFor
// (below) can map the focused Section back to the ZoneKind keys.go's
// MapKey grammar needs. An ID outside that set (e.g. a test's stub
// section) is treated as a "plain" zone: MapKey advances/backs on
// Tab/Shift+Tab exactly like every non-picker, non-Title, non-Prompt,
// non-Create zone the grammar defines -- the correct behavior for a field
// kind this package doesn't know about yet.
//
// Focus() returns tea.Cmd, unlike the more obvious void signature, and it
// is not optional: widgets.PromptArea.Focus() returns the cursor's blink
// tea.Cmd, which must be folded into this package's Update batching or
// the cursor never blinks. A Section wrapping PromptArea has no other
// channel to hand that Cmd back to the focus ring -- Update(tea.Msg) only
// fires on an incoming message, never on a focus change, and Blur() has
// nowhere to put a Cmd it never receives. focus.go's `set` is where the
// Cmd is collected and handed back to form.go's Update.
type Section interface {
	// ID identifies the section (see the ID() convention above).
	ID() string
	// Enabled reports whether the section can currently take focus.
	// focus.go's ring skips disabled sections, with wrap-around, but the
	// stack still draws their row (present-but-inert, v2 spec §6),
	// carrying the reason.
	Enabled() bool
	// Focus gives the section input focus, returning whatever tea.Cmd
	// that requires (e.g. a cursor blink loop) -- see the note above.
	Focus() tea.Cmd
	// Blur removes input focus.
	Blur()
	// Update forwards a message the form grammar (keys.go's MapKey)
	// classified as ActionNone -- "not part of the grammar, forward it"
	// -- or any message class MapKey never intercepts (e.g. tea.PasteMsg
	// content, or a cursor-blink tick) to the section's own editing
	// behavior.
	Update(tea.Msg) tea.Cmd

	// Label is this field's row label ("issue", "worktree", "account"):
	// plain lowercase words, no colon, no padding. LEADING SPACES INDENT
	// a child row. The FORM renders it into a fixed labelColWidth column,
	// never the section, which is what makes the column aligned by
	// construction rather than by convention (v2 spec §5).
	Label() string

	// Row renders this field's VALUE CELL: exactly ONE physical line,
	// exactly w cells wide, where w is the value column's width
	// (rowlayout.go's labelCol applied to contentBox's inner). It is
	// called for EVERY section on every render, focused or not; a field
	// whose precondition is currently false (Enabled() == false) renders
	// its reason here, dim (v2 spec §6.1).
	//
	// Row takes no height and must not consult the window's height in
	// any way. That is precisely what makes "row i is always at row i"
	// hold, and it is a contract worth testing directly rather than
	// documenting: render the same section at two window heights and
	// compare the bytes (field_rows_test.go does).
	Row(w int) string

	// Panel renders this field's chooser or editor -- the region under
	// the second rule -- into exactly h physical lines, w cells wide,
	// including any verdict or note line of its own. It is called ONLY
	// for the section that currently holds focus, so unlike Row it may
	// render focus-only affordances. w is the full content box INCLUDING
	// the two-cell gutter (so a picker's own cursor glyph lands in the
	// same column the row stack indents past, matching v2 spec §4's
	// mockups); h is min(PanelRows(), the frame's Region) and is never
	// < 1, because the form does not call Panel at all when the layout
	// kept no region.
	Panel(w, h int) string

	// PanelRows is the greatest number of rows this field can put to
	// GOOD USE, 0 meaning it has no panel. The form hands Panel
	// min(PanelRows(), Region) and blank-fills the rest of the region
	// itself, so a field deriving this from its own item count never
	// reserves rows it cannot fill -- and, just as importantly, a small
	// panel never pulls the footer up (rowlayout.go's layoutFrame).
	PanelRows() int
}

// Optional capability interfaces a concrete Section may implement beyond
// the base Section contract, to opt into grammar behavior keys.go's
// MapKey documents but this package cannot invoke generically (Section
// itself has no Value/Complete/InsertNewline method -- adding one to
// every Section for the sake of the one or two zones that need it would
// force every future, simpler Section to implement a method it has
// nothing to say about). form.go's handleKey checks for these via a type
// assertion on the currently focused Section, the same "small optional
// interface, checked where needed" shape Go's own standard library uses
// (io.ReaderFrom, fmt.Stringer next to fmt.GoStringer, ...).
//
// None of Task 16's own Sections (its test stub doubles, and this
// package's internal createSection) implement any of these three -- they
// exist now because MapKey's grammar ("the grammar you dispatch on," per
// this task's own context) already requires them to make full sense of
// Tab/Enter/⌃J in the Title/picker/Prompt zones, and Tasks 17-18's
// concrete Title/Picker/Prompt Sections are expected to implement them
// once they land. Flagged in task-16-report.md as a disclosed, additive
// extension of the brief's Section-interface story, not a silent
// redesign of Section itself.
type (
	// titleValuer lets a Title-zone Section report whether it is
	// currently empty -- FocusZone.TitleEmpty, consulted by MapKey only
	// when zoneFor's Kind == ZoneTitle. A Title Section that does not
	// implement this is treated as always-empty (Enter advances rather
	// than submitting), the conservative default.
	titleValuer interface{ Value() string }

	// completer lets a picker-zone Section (ZoneKind.isPicker():
	// Issue/Dir/Base) try MapKey's ActionComplete itself (e.g. a
	// directory picker's shell-style path completion) before this
	// package falls back to a plain advance -- MapKey's own doc: "a zone
	// whose widget has nothing to complete should treat this the same
	// as ActionAdvance."
	completer interface{ Complete() bool }

	// newliner lets the Prompt-zone Section insert a literal newline on
	// MapKey's ActionNewline (⌃J/⇧↵/⌥↵ in the prompt), mirroring
	// widgets.PromptArea.InsertNewline's own "bypasses Update
	// deliberately" contract.
	newliner interface{ InsertNewline() }

	// footerHinter supplies the focused section's OWN key rungs, widest
	// first; the form appends the constant tail and picks a rung that
	// fits (footer.go's footerRungsFor/fitFooter). v2 spec §5 adds this
	// as the fourth optional capability interface, and v2 spec §3 rule 4
	// is why it exists: "the footer teaches the focused field, then
	// states the constants" -- v1's per-field hint ROWS disappear, so a
	// field that had something to say needs somewhere to say it. A
	// section that does not implement it falls back to footer.go's own
	// per-zone table.
	footerHinter interface{ FooterRungs() []string }
)

// zoneKindByID maps a Section's canonical ID() (see Section's own doc
// comment) to the ZoneKind MapKey's grammar needs. Worktree/Placement/
// Agent share one ZoneKind assignment here purely as a stand-in for "no
// special case" -- MapKey's behavior for all three is identical (plain
// advance/back, no Tab-complete, no newline, Enter advances) -- since
// ZoneKind has no dedicated "plain" member of its own.
//
// This is a CLOSED set: every ID internal/app can put in the ring appears
// here, and zoneFor's fallback for anything else (ZonePlacement) exists
// for test stubs, not for production fields. The v2 collapse of the
// worktree trio into one Section removed the "branch" and "base" entries
// -- no Section carries those IDs any more, and the WorktreeField that
// absorbed them keeps ID() == "worktree", so nothing fell through the
// fallback when they went. ZoneBranch/ZoneBase themselves survive in
// keys.go: they are still part of the grammar's vocabulary, simply with
// no section currently mapped onto them.
var zoneKindByID = map[string]ZoneKind{
	"issue":     ZoneIssue,
	"dir":       ZoneDir,
	"title":     ZoneTitle,
	"worktree":  ZoneWorktree,
	"placement": ZonePlacement,
	"agent":     ZoneAgent,
	"account":   ZoneAccount,
	"prompt":    ZonePrompt,
	"create":    ZoneCreate,
}

// zoneFor builds the FocusZone MapKey needs for the currently focused
// Section s. An ID() outside zoneKindByID (a test stub, or any future
// field kind this package doesn't know about yet) defaults to
// ZonePlacement -- an arbitrary, deliberately "plain" zone (see
// zoneKindByID's own doc: MapKey treats Worktree/Placement/Agent
// identically, so any of the three would serve equally well here). s ==
// nil (an empty ring) also defaults to ZonePlacement.
func zoneFor(s Section) FocusZone {
	if s == nil {
		return FocusZone{Kind: ZonePlacement}
	}
	kind, ok := zoneKindByID[s.ID()]
	if !ok {
		kind = ZonePlacement
	}
	zone := FocusZone{Kind: kind}
	if kind == ZoneTitle {
		zone.TitleEmpty = true
		if v, ok := s.(titleValuer); ok {
			zone.TitleEmpty = strings.TrimSpace(v.Value()) == ""
		}
	}
	return zone
}

// Setup configures a new form Model.
type Setup struct {
	// Palette is the herdr-derived color palette (internal/theme) every
	// style in this package and its Sections is built from.
	Palette theme.Palette
	// Sections are the caller's own form fields, in v2 spec §6's row
	// order. New appends its own internal Create section after these,
	// always last -- callers must not include their own "create" ID.
	Sections []Section
	// Name is the header line's left-hand text ("new session", v2 spec
	// §4). "" renders no name; the header row itself is still part of
	// the frame whenever the window affords it, because the layout is a
	// function of (height, section count) alone -- see rowlayout.go's
	// layoutFrame. v1's path ignores this field entirely (it draws no
	// header at all).
	Name string
	// InitialFocusID is the ID() of the section focus opens on -- v2
	// spec §8's "focus opens on title, not on the first enabled
	// section". "" falls back to the first ENABLED section, v1's
	// behavior. An ID no section carries is likewise ignored. Like
	// FocusByID (and unlike Tab navigation) a named section is focused
	// even when it is currently disabled: the caller naming it is
	// asserting an intent the ring's skip-disabled walk has no way to
	// express.
	InitialFocusID string
}

// Model is the form root: a tea.Model over Setup's Sections plus this
// package's own Create button, wiring focus.go's ring, rowlayout.go's
// content box and frame arithmetic, footer.go's key ladder, and keys.go's
// MapKey grammar together, painted in Setup.Palette's herdr skin.
//
// Construction via New is required. A zero-value Model (e.g. `var m
// form.Model`, or any Model obtained some other way than a call to New)
// has a nil focus ring and no Sections -- Init/Update/View/ViewAt each
// guard against that nil ring explicitly (see their own doc comments) and
// degrade to a no-op Cmd / an unchanged Model / an empty render rather
// than dereferencing it, per this project's "no panics in production
// code" rule, but a zero-value Model still can't do anything useful: the
// guard exists so an uninitialized Model fails safely, not so leaving one
// uninitialized is a supported way to use this package.
type Model struct {
	palette    theme.Palette
	ring       *focusRing
	width      int
	height     int
	clearArmed bool
	name       string
	context    string
}

// New returns a form Model over cfg.Sections plus this package's own
// always-enabled Create section (spec §6 field 9), with the focus ring
// on the first enabled section. It performs no I/O and calls no
// Section's Focus()/Blur() (a pure constructor, matching this package's
// widgets' own "no side effects before Init/a real call" convention) --
// Init is where the initial focus (and whatever tea.Cmd that produces)
// is actually asserted.
func New(cfg Setup) Model {
	sections := make([]Section, 0, len(cfg.Sections)+1)
	sections = append(sections, cfg.Sections...)
	sections = append(sections, newCreateSection(cfg.Palette))
	ring := newFocusRing(sections)
	if cfg.InitialFocusID != "" {
		// Move the CURSOR only, no Focus()/Blur() fan-out: New is a pure
		// constructor (see its doc comment above) and Init is what
		// asserts the initial focus for real. See Setup.InitialFocusID
		// for why a disabled section is a legitimate target here.
		if i := ring.indexOf(cfg.InitialFocusID); i >= 0 {
			ring.index = i
		}
	}
	return Model{
		palette: cfg.Palette,
		ring:    ring,
		name:    cfg.Name,
	}
}

// SetContext sets the header line's right-hand text -- v2 spec §4's
// "live context for the SELECTED project: repository name and its
// current branch, not the invoking workspace" ("herdr-draft · main").
// "" renders no context. Ignored by v1's path, which draws no header.
//
// POINTER receiver, deliberately, and this is the single easiest thing
// in this file to get wrong: every OTHER Model mutator gets away with a
// value receiver only because it writes through *focusRing, a pointer
// the copy shares with the original. A plain string field written
// through a value receiver would be set on the copy and silently
// dropped.
func (m *Model) SetContext(s string) {
	m.context = s
}

// SubmitMsg is emitted (as a tea.Cmd's result) when the grammar's
// ActionSubmit fires (Enter from Create or a filled Title, or ⌃S from
// any zone). The app layer's own Update, wrapping this Model, is
// expected to listen for it and drive the submit pipeline (spec §9);
// this package stays a dumb view and never calls any pipeline itself
// (spec §4).
type SubmitMsg struct{}

// CancelMsg is emitted when ActionCancel fires (Esc/⌃C).
type CancelMsg struct{}

// ClearRequestedMsg is emitted when the ⌃R ⌃R double-tap fires
// (ActionClear). Per keys.go's own doc comment, rebuilding the form to
// its default state is the app layer's job, not this package's -- it has
// no config/profile data to do that with.
type ClearRequestedMsg struct{}

func submitCmd() tea.Msg { return SubmitMsg{} }
func cancelCmd() tea.Msg { return CancelMsg{} }
func clearCmd() tea.Msg  { return ClearRequestedMsg{} }

// Init focuses the ring's current (first-enabled) section and returns
// whatever tea.Cmd that produces. A zero-value Model (nil ring; see
// Model's own doc comment) is a no-op, returning nil rather than
// dereferencing it.
func (m Model) Init() tea.Cmd {
	if m.ring == nil {
		return nil
	}
	return m.ring.set(m.ring.index)
}

// FocusedID returns the ID() of the section currently holding focus (see
// Section's own doc comment on the ID() convention), or "" for a zero-value
// Model (nil ring) or an empty ring. This is a minimal read accessor for
// the app layer, added in Task 20: unlike form.go's setter-oriented
// surface (Setup.Sections, each field's own SetXxx methods), nothing in
// this package previously let the app layer observe WHICH section is
// currently focused at all -- needed for spec §11's "clauth: load ... on
// account focus", which the app layer can only react to by polling this
// after every Update and diffing against what it last saw (the same
// before/after-comparison discipline every Section's own Update already
// uses for its own value, applied here one level up).
func (m Model) FocusedID() string {
	if m.ring == nil {
		return ""
	}
	if s := m.ring.current(); s != nil {
		return s.ID()
	}
	return ""
}

// SectionIDs returns every section's own ID(), in construction order --
// added alongside FocusedID (Task 20 fix round 1) for the same reason:
// external verification of New's own assembled order (e.g. "the three
// worktree zones must read as ONE visual group," a Task 20 carried
// requirement) needs a way to see the FULL list, including a
// present-but-inert section. Tab-driven navigation alone can't answer
// this: focus.go's ring skips disabled sections entirely (nextEnabled),
// so a section that's inert in whatever state a test put the form into
// (e.g. Placement, always inert while Worktree is on) would silently and
// misleadingly drop out of a Tab-walk, even though it's still really
// there, in its real construction position. Returns nil for a zero-value
// Model (nil ring).
func (m Model) SectionIDs() []string {
	if m.ring == nil {
		return nil
	}
	ids := make([]string, len(m.ring.sections))
	for i, s := range m.ring.sections {
		ids[i] = s.ID()
	}
	return ids
}

// FocusByID moves the focus ring directly to the section whose ID() ==
// id, regardless of that section's own Enabled() state, syncing every
// section's Focus()/Blur() the same way Tab navigation does (focus.go's
// own set), and returns whatever tea.Cmd the newly focused section's own
// Focus() produces. A no-op (nil Cmd) for a zero-value Model (nil ring)
// or an id with no matching section.
//
// This is a minimal public entry point over focusRing's own
// already-existing, already-documented focusByID (focus.go) -- added in
// Task 20b since nothing in this package previously exposed it beyond
// this file. The rule it serves is "a failing submit re-focuses Title",
// generalized by spec §9's submit-time validation ("inline verdicts,
// submit blocked, focus moved") to whichever field actually blocked; the
// app layer's own checkSubmitValidation is that rule's real caller.
func (m Model) FocusByID(id string) tea.Cmd {
	if m.ring == nil {
		return nil
	}
	return m.ring.focusByID(id)
}

// Update dispatches an incoming message: tea.WindowSizeMsg is stored for
// View/ViewAt; tea.KeyPressMsg is run through MapKey (keys.go, "the
// grammar you dispatch on") and acted on by handleKey; tea.PasteMsg
// disarms the ⌃R ⌃R gesture (HandlePaste) and is otherwise forwarded to
// the focused section, matching the same routing keys.go's own doc
// requires (paste content must never re-enter the key grammar); anything
// else (e.g. a cursor-blink tick) is forwarded to the focused section
// only -- an unfocused section is Blurred and has no live cursor to tick.
//
// A zero-value Model (nil ring; see Model's own doc comment) still
// records a tea.WindowSizeMsg (harmless -- width/height are plain fields,
// not ring-derived), but every other message is a no-op: there is no
// focused section, and no zone, to run MapKey or forwardToFocused
// against.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wsm, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = wsm.Width, wsm.Height
		return m, nil
	}
	if m.ring == nil {
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.PasteMsg:
		m.clearArmed = HandlePaste()
		return m, m.forwardToFocused(msg)
	case tea.MouseClickMsg:
		return m.handleMouseClick(msg)
	case tea.MouseWheelMsg:
		return m.handleMouseWheel(msg)
	default:
		return m, m.forwardToFocused(msg)
	}
}

// handleMouseClick implements task 21's click grammar (spec §7: herdr is
// mouse-first): a left-button click on the Create button's own
// "button:create" zone (see renderFooter) submits, exactly like Enter
// from Create; a click on the cancel button's "button:cancel" zone
// beside it cancels, exactly like Esc; a left-button click inside the
// "panel" zone is forwarded
// to the FOCUSED section WITHOUT changing focus; and a left-button click
// on any stack row's own "field:<id>" zone (see compose) moves the ring
// directly to that section -- the same ring.set plumbing Tab navigation
// and FocusByID already use, so it works even on a currently-disabled/
// present-but-inert section, matching FocusByID's own documented contract
// -- and then forwards the raw click to the now-focused section's own
// Update, so a concrete field (PlacementField, WorktreeField, AgentField,
// IssueField/DirField/AccountField) can decide for itself whether the
// SAME click also landed on one of ITS OWN finer-grained
// chip:<sectionID>:<chipID>/row:<sectionID>:<n> zones -- it alone knows
// which chip IDs or picker rows it owns; form.go does not (see e.g.
// field_placement.go's PlacementField.Update). A click matching no zone
// at all (the popup's own outer chrome, a stale click after a resize, or
// any non-left button -- spec §7 only asks for click-to-activate, not a
// context-menu) is a no-op.
//
// The panel branch is the one with no v1 equivalent, and it must come
// FIRST: the panel already belongs to the focused field by construction,
// so there is nothing to re-focus -- and re-running the focus fan-out
// would blur and re-Focus the field mid-click, which is what would stop
// the chip:<id>:<chipID> and row:<id>:<n> zones nested inside the panel
// from resolving.
//
// A zero-value Model (nil ring; see Model's own doc comment) is a no-op.
func (m Model) handleMouseClick(msg tea.MouseClickMsg) (Model, tea.Cmd) {
	if m.ring == nil {
		return m, nil
	}
	if msg.Button != tea.MouseLeft {
		return m, nil
	}
	if widgets.Zones.Get(zoneCreateButton).InBounds(msg) {
		return m, submitCmd
	}
	if widgets.Zones.Get(zoneCancelButton).InBounds(msg) {
		return m, cancelCmd
	}

	if widgets.Zones.Get(zonePanel).InBounds(msg) {
		return m, m.forwardToFocused(msg)
	}

	for i, s := range m.ring.sections {
		if s.ID() == createSectionID {
			continue
		}
		if !widgets.Zones.Get(zoneFieldPrefix + s.ID()).InBounds(msg) {
			continue
		}
		focusCmd := m.ring.set(i)
		clickCmd := s.Update(msg)
		return m, tea.Batch(focusCmd, clickCmd)
	}
	return m, nil
}

// handleMouseWheel forwards the raw tea.MouseWheelMsg to the CURRENTLY
// FOCUSED section's own Update only. v2 spec §7 states the rule as "the
// wheel scrolls the panel unconditionally -- there is now exactly one
// scrollable region, so routing by focus is no longer meaningful", and
// forwarding to the focused section is how that is spelled here: the
// panel IS the focused section's panel, so "the panel" and "the focused
// section" name the same region. (v1's own rule, task 21's, was spec
// §7's "scroll the focused picker or the prompt" -- deliberately not
// "whatever section sits under the pointer" -- and the code is
// unchanged; v2 removed the ambiguity that wording guarded against, not
// the behavior.) Forwarding lets a
// concrete field decide for itself whether it has anything to scroll
// (IssueField/DirField/WorktreeField's base picker/AccountField/
// AgentField's expanded list move their own picker cursor; PromptField
// scrolls its own textarea; every other field ignores it, the same
// "forward and let the widget decide" posture forwardToFocused already
// uses for every other unclassified message).
//
// A zero-value Model (nil ring; see Model's own doc comment) is a no-op.
func (m Model) handleMouseWheel(msg tea.MouseWheelMsg) (Model, tea.Cmd) {
	if m.ring == nil {
		return m, nil
	}
	return m, m.forwardToFocused(msg)
}

// wheelDelta translates a tea.MouseWheelMsg into -1 (wheel up: move
// back/prev, mirroring Up) or +1 (wheel down: move forward/next,
// mirroring Down), or 0 for a wheel axis this package has no vertical-
// scroll meaning for (left/right -- v2 spec §7 asks only for a wheel
// that "scrolls the panel", a vertical region, and v1 spec §7 asked only
// for up/down before it). Shared by every field_*.go Update that handles
// tea.MouseWheelMsg, so each one's own up/down cursor-move branches read
// the same shape as their existing Up/Down KeyPressMsg branches.
func wheelDelta(msg tea.MouseWheelMsg) int {
	switch msg.Button {
	case tea.MouseWheelUp:
		return -1
	case tea.MouseWheelDown:
		return 1
	default:
		return 0
	}
}

// forwardToFocused forwards msg to the ring's current section's own
// Update, or is a no-op if the ring is empty.
func (m Model) forwardToFocused(msg tea.Msg) tea.Cmd {
	if s := m.ring.current(); s != nil {
		return s.Update(msg)
	}
	return nil
}

// handleKey runs msg through MapKey for the currently focused zone and
// acts on the resulting KeyAction: ring navigation (Advance/Back) is
// handled directly by focus.go; ActionComplete/ActionNewline delegate to
// the focused section's own optional completer/newliner capability (see
// their doc comments) when it has one, falling back to a plain advance
// (Complete) or a no-op (Newline, since nothing to insert into); Submit/
// Cancel/Clear are surfaced to the app layer as messages (see
// SubmitMsg/CancelMsg/ClearRequestedMsg); ArmClear needs no action beyond
// what MapKey already returned (the armed state, threaded through
// m.clearArmed, is what the footer's own hint ladder reads); ActionNone
// forwards the raw key to the focused section, exactly like every other
// unclassified message.
func (m Model) handleKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	zone := zoneFor(m.ring.current())
	action, armed := MapKey(msg, zone, m.clearArmed)
	m.clearArmed = armed

	switch action {
	case ActionAdvance:
		return m, m.ring.move(1)
	case ActionBack:
		return m, m.ring.move(-1)
	case ActionComplete:
		if c, ok := m.ring.current().(completer); ok && c.Complete() {
			return m, nil
		}
		return m, m.ring.move(1)
	case ActionNewline:
		if nl, ok := m.ring.current().(newliner); ok {
			nl.InsertNewline()
		}
		return m, nil
	case ActionSubmit:
		return m, submitCmd
	case ActionCancel:
		return m, cancelCmd
	case ActionArmClear:
		return m, nil
	case ActionClear:
		return m, clearCmd
	default: // ActionNone
		return m, m.forwardToFocused(msg)
	}
}

// View satisfies tea.Model, rendering at the last tea.WindowSizeMsg this
// Model saw (0x0 before the first one arrives -- ViewAt degrades to an
// empty string in that case, same as every degenerate-dimension contract
// this package's widgets already follow). A zero-value Model (nil ring;
// see Model's own doc comment) also renders "" -- see ViewAt/compose.
func (m Model) View() tea.View {
	return tea.NewView(m.ViewAt(m.width, m.height))
}

// ViewAt is the deterministic render entry point golden-frame tests call
// directly (and the one View() itself calls): it renders the form at an
// explicit w x h rather than whatever tea.WindowSizeMsg last reported,
// and pins lipgloss's output to a fixed color profile (TrueColor) via
// charmbracelet/colorprofile.Writer's own documented pass-through mode,
// so golden frames are byte-identical across machines regardless of the
// local terminal's own detected capabilities.
//
// Why pin at all, given lipgloss v2's Style.Render never actually
// consults the environment (verified directly against the vendored
// source: color.go/style.go/writer.go have no os.Getenv or
// colorprofile.Detect call anywhere in the Style.Render path -- v2
// removed v1's global SetColorProfile entirely; downsampling is now an
// io.Writer-level concern applied only by lipgloss.Println/Sprint/the
// package's own `Writer` var, none of which this package calls. This
// matches Atrium's own nocolour_test.go comment on the same v1->v2 cut:
// "SetColorProfile is exactly what v2 removes... Bubble Tea hands the
// profile to ultraviolet, whose [writer] applies conversion.") Pinning
// explicitly here, rather than relying on "nothing in this package
// currently calls anything environment-sensitive," is the belt-and-
// suspenders posture the brief asks for -- and colorprofile.Writer{
// Profile: TrueColor} is a documented passthrough (verified against
// github.com/charmbracelet/colorprofile@v0.4.3 writer.go: `case
// w.Profile == TrueColor:` writes bytes unmodified), so it costs nothing
// even though it is not, today, load-bearing.
//
// A zero-value Model (nil ring; see Model's own doc comment) renders ""
// -- compose (below) guards the nil ring directly rather than this method
// duplicating the check.
//
// ViewAt is task 21's own zone.Scan boundary: compose (below) and every
// Section it composes register bubblezone/v2 zones by wrapping their own
// rendered content in invisible marker sequences (widgets.Zones.Mark),
// and THIS is the one place in the whole call graph -- the deterministic
// render entry point every golden-frame test and the real tea.Program
// alike ultimately go through (View() above calls this too) -- where
// that marked content is handed to widgets.Zones.Scan before it ever
// leaves this package: Scan both records each zone's on-screen bounds
// for later mouse-hit lookups (handleMouseClick/handleMouseWheel, and
// each field's own click handling) AND strips every marker sequence back
// out of the returned string. Marker sequences are zero-width for every
// lipgloss Width/MaxWidth measurement (verified directly: a private CSI
// sequence ending in 'z', not a color/style code, so lipgloss's
// ansi-aware width calculator skips it exactly like it skips a real SGR
// code) and Scan removes them cleanly regardless of which composed lines
// actually reached the output -- composeRows' paint loop clamps to h and
// stackWindow scrolls rows out of the frame entirely, and an unpaired
// marker left behind by a line that never made it is still stripped, its
// zone simply isn't reported this scan (see widgets/zones.go's own doc
// comment) -- so this package's committed golden frames stay
// byte-identical: Scan is called BEFORE the colorprofile.Writer pass
// below, so that pass (already a documented TrueColor passthrough
// regardless) never sees a marker byte at all.
func (m Model) ViewAt(w, h int) string {
	content := widgets.Zones.Scan(m.compose(w, h))
	var buf strings.Builder
	cw := &colorprofile.Writer{Forward: &buf, Profile: colorprofile.TrueColor}
	_, _ = cw.WriteString(content)
	return buf.String()
}

// compose assembles the form's frame at exactly w x h, or "" for a
// degenerate height or a zero-value Model (nil ring; see Model's own doc
// comment) -- composeRows guards nothing itself.
func (m Model) compose(w, h int) string {
	if h <= 0 || m.ring == nil || len(m.ring.sections) == 0 {
		return ""
	}
	return m.composeRows(w, h)
}

// composeRows assembles v2's frame at exactly w x h (v2 spec §4/§5/§9):
// the header, a rule, the row stack, a second rule, the detail panel,
// and the footer -- six components, no blank spacer rows, in whatever
// subset rowlayout.go's layoutFrame says this height affords.
//
// Every line is emitted at its own background: PanelBG for all of them
// except the focused stack row, which is painted edge to edge in
// ActiveRowBG. That fill is ONE OF THREE focus signals (v3 spec §5.4),
// not the whole of it -- renderStackRow adds the accent bar in the gutter
// and bold on the value. v2 shipped the fill alone, on the premise that
// it replaced v1's gutter bar; rowvalues.go's focusBarGlyph records why
// that premise failed and why the bar is back. paintLine is what makes
// the fill survive the accent- and dim-styled spans inside a row: it
// reasserts the background SGR after every embedded ANSI reset, which is
// exactly the hazard a full-width row highlight hits.
//
// No degradation ladder runs here and none is needed. layoutFrame's
// components sum to exactly h by construction, and Create is on the
// footer line rather than in the stack, so v1's "never clip the last
// line" contract -- which v1 repaired after the fact, with a post-hoc
// drop-lines cascade over the composed lines (sizes.go's own package doc
// records what became of it) -- is upheld here by the arithmetic
// instead. The paint loop still clamps to h, belt and braces.
func (m Model) composeRows(w, h int) string {
	sections := m.ring.sections
	lastIdx := len(sections) - 1
	// `sections[lastIdx]` is New's own Create section, which feeds the
	// FOOTER rather than the stack, so the stack is indexed over
	// sections[:len-1].
	body := sections[:lastIdx]

	padLeft, inner := contentBox(w)
	boxWidth := gutterWidth + inner
	labelW, valueW := labelCol(inner)
	pad := strings.Repeat(" ", padLeft)

	f := layoutFrame(h, len(body))

	type composedLine struct {
		text string
		bg   theme.Color
	}
	lines := make([]composedLine, 0, h)
	add := func(text string, bg theme.Color) {
		lines = append(lines, composedLine{text: pad + text, bg: bg})
	}

	if f.Header {
		add(m.renderHeaderLine(boxWidth), m.palette.PanelBG)
	}
	if f.Rule1 {
		add(dividerLine(boxWidth, m.palette), m.palette.PanelBG)
	}

	start := stackWindow(len(body), f.Rows, m.ring.index)
	for i := start; i < start+f.Rows && i < len(body); i++ {
		s := body[i]
		focused := i == m.ring.index
		bg := m.palette.PanelBG
		if focused {
			bg = m.palette.ActiveRowBG
		}
		// field:<id> (v2): one zone per stack line -- clicking anywhere
		// on the row focuses that field and then forwards the raw click
		// to it, so whatever chip/picker zones the field nested in its
		// own Row survive and resolve (bubblezone markers nest).
		add(widgets.Zones.Mark(zoneFieldPrefix+s.ID(), renderStackRow(s, labelW, valueW, focused, m.palette)), bg)
	}

	if f.Rule2 {
		add(dividerLine(boxWidth, m.palette), m.palette.PanelBG)
	}

	if f.Region > 0 {
		// One "panel" zone over the whole region, blank rows included:
		// a click inside it goes to the focused section without moving
		// focus (handleMouseClick).
		region := widgets.Zones.Mark(zonePanel, strings.Join(m.renderPanelRegion(boxWidth, f.Region), "\n"))
		for _, l := range strings.Split(region, "\n") {
			add(l, m.palette.PanelBG)
		}
	}

	if f.Footer {
		focused := m.ring.current()
		rungs := footerRungsFor(focused, zoneFor(focused), m.clearArmed)
		add(renderFooter(boxWidth, rungs, m.palette), m.palette.PanelBG)
	}

	painted := make([]string, 0, h)
	for _, l := range lines {
		if len(painted) == h {
			break
		}
		painted = append(painted, paintLine(l.text, w, l.bg))
	}
	for len(painted) < h {
		painted = append(painted, paintLine("", w, m.palette.PanelBG))
	}
	return strings.Join(painted, "\n")
}

// renderHeaderLine renders v2 spec §4's header: the form's name on the
// left (Setup.Name, bold) and the selected project's live context on the
// right (SetContext, dim and flush to the box's right edge). Either half
// may be empty; the row itself is still drawn, because the frame's
// geometry is a function of (height, section count) alone -- see
// rowlayout.go's layoutFrame.
func (m Model) renderHeaderLine(width int) string {
	name, context := m.name, m.context
	if name != "" {
		name = lipgloss.NewStyle().Foreground(m.palette.Text).Bold(true).Render(name)
	}
	if context != "" {
		context = dimText(m.palette).Render(context)
	}
	return spreadLine(name, context, width)
}

// renderStackRow renders one line of the row stack: the two-cell gutter,
// the field's Label padded into the fixed label column, then its own Row
// filling the value column. The label is dim and the value is the field's
// business (v2 spec §7's "lowercase terse labels in a dim color, values
// brighter").
//
// Two of v3 spec §5.4's three focus signals are applied HERE -- the
// accent bar in the gutter and bold over the value -- and the third, the
// ActiveRowBG fill, is still the caller's. All three are the FORM's, not
// the section's: `focused` is not passed on to Row, which keeps Row
// focus- and height-independent and so keeps "row i is always at row i"
// true as focus travels (field_rows_test.go pins it).
//
// The label is deliberately left un-bolded. §5.4 says "bold on the
// value", and the whole point of the dim label column is that it is
// scenery the eye skips; bolding the row's scenery would compete with the
// signal rather than add to it.
func renderStackRow(s Section, labelW, valueW int, focused bool, p theme.Palette) string {
	label := ""
	if labelW > 0 {
		label = dimText(p).Width(labelW).MaxWidth(labelW).Inline(true).Render(s.Label())
	}
	value := fitLine(s.Row(valueW), valueW)
	if focused {
		value = boldSpan(value)
	}
	return rowGutter(focused, p) + label + value
}

// renderPanelRegion renders exactly region lines of the focused
// section's detail panel, width cells wide.
//
// The focused field is handed min(PanelRows(), region) lines and this
// function blank-fills whatever is left over. That asymmetry is
// deliberate and load-bearing: a field with little to show does NOT
// shrink the region, so the rule above the panel and the footer below it
// stay put as focus travels (see layoutFrame's "slack always lands in
// Region"). A focused section with no panel at all (PanelRows() == 0 --
// the Create section, or a field whose chooser is empty) yields an
// entirely blank region for the same reason.
func (m Model) renderPanelRegion(width, region int) []string {
	lines := make([]string, 0, region)
	if s := m.ring.current(); s != nil && region > 0 {
		if want := s.PanelRows(); want > 0 {
			if want > region {
				want = region
			}
			lines = append(lines, strings.Split(fitBlock(s.Panel(width, want), want, width), "\n")...)
		}
	}
	for len(lines) < region {
		lines = append(lines, fitLine("", width))
	}
	return lines[:region]
}

// renderFooter composes v2's footer line: the contextual key ladder on
// the left, the two action buttons flush right (v2 spec §4's mockup
// footer, `↵ create · esc cancel`, and §5's "Create stays a focus stop
// but moves onto the footer line").
//
// There are TWO buttons, not one, because v2 spec §4 puts both actions
// there and §7 adopts herdr's own two-button grammar wholesale ("action
// buttons carrying a key glyph plus a verb -- the primary filled with
// the accent color, the secondary on a surface background"). Only the
// primary is a Section and a focus stop; cancel is esc's own clickable
// affordance ("button:cancel", v2 spec §7's zone list), which is also
// what retires `Esc cancel` from the key ladder -- see footer.go's
// zoneRungs.
//
// The rungs are fitted into width - both buttons - a two-cell gap, and
// the buttons are NEVER traded away for hint text: on a window too
// narrow for all three, the key ladder is what gets clipped, then cancel,
// and create is the last thing standing -- it is the one control the
// form cannot do without.
func renderFooter(width int, rungs []string, p theme.Palette) string {
	create := widgets.Zones.Mark(zoneCreateButton, createButton(p))
	createWidth := lipgloss.Width(create)
	if createWidth >= width {
		return fitLine(create, width)
	}

	buttons, buttonsWidth := create, createWidth
	cancel := widgets.Zones.Mark(zoneCancelButton, cancelButton(p))
	if both := createWidth + footerButtonGap + lipgloss.Width(cancel); both < width {
		buttons, buttonsWidth = create+strings.Repeat(" ", footerButtonGap)+cancel, both
	}
	return spreadLine(fitFooter(rungs, width-buttonsWidth-footerButtonGap), buttons, width)
}

// footerButtonGap is the minimum blank space kept between the footer's
// key ladder and the Create button, so the two never read as one run of
// text.
const footerButtonGap = 2

// spreadLine renders left and right on one line exactly width cells
// wide, with right flush to the right edge. When the two collide the
// LEFT is what gets clipped (the right-hand half is the Create button on
// the footer and the live project context in the header -- in both cases
// the half that must survive).
func spreadLine(left, right string, width int) string {
	if width < 1 {
		width = 1
	}
	rightWidth := lipgloss.Width(right)
	if rightWidth >= width {
		return fitLine(right, width)
	}
	return fitLine(fitLine(left, width-rightWidth)+right, width)
}

// dividerLine renders a horizontal rule inner cells wide in the palette's
// Overlay0 color, matching Atrium's own divider convention
// (textInput_render.go's tiDividerStyle/`strings.Repeat("─", innerWidth)`,
// ported here as styling only -- the repeated-rune choice, not a literal
// code port, since Atrium's own construction is a one-line call this
// package could not import across packages anyway).
//
// Overlay0 and not Border (v3 spec §5.3): Border is herdr's surface_dim, a
// near-invisible *fill* -- reading it as a stroke is what drew v2's rules at
// 1.07:1 against their own panel. Overlay0 is the only palette field with a
// contrast floor that holds across all eighteen builtins (worst case 1.69:1,
// nord), which internal/theme's contrast_test.go asserts. herdr splits the
// two the same way in its scrollbar: track surface_dim, thumb overlay0.
func dividerLine(inner int, p theme.Palette) string {
	if inner < 0 {
		inner = 0
	}
	return lipgloss.NewStyle().Foreground(p.Overlay0).Render(strings.Repeat("─", inner))
}

// createSection is form.go's own internal Section for spec §6 field 9,
// "Create": the form's last focus stop, always enabled, and (by
// construction -- New always appends exactly one of these after the
// caller's own Setup.Sections) the section composeRows renders on the
// FOOTER line rather than in the stack. v2 spec §5 keeps it a real focus
// stop for a reason: the ring keeps a guaranteed-enabled member, the
// mouse zone is unchanged, and "never clip the last line" now protects
// the key ladder and the button with one rule.
type createSection struct {
	palette theme.Palette
	focused bool
}

func newCreateSection(palette theme.Palette) *createSection {
	return &createSection{palette: palette}
}

func (c *createSection) ID() string    { return "create" }
func (c *createSection) Enabled() bool { return true }

func (c *createSection) Focus() tea.Cmd {
	c.focused = true
	return nil
}

func (c *createSection) Blur() { c.focused = false }

func (c *createSection) Update(tea.Msg) tea.Cmd { return nil }

// Label/Row/Panel/PanelRows are all trivial by design: Create occupies no
// stack row and no panel, it renders as the button on the footer line
// (composeRows/renderFooter), so nothing ever calls them. They exist to
// state that, not to render anything.
func (c *createSection) Label() string         { return "" }
func (c *createSection) Row(int) string        { return "" }
func (c *createSection) Panel(int, int) string { return "" }
func (c *createSection) PanelRows() int        { return 0 }

// actionButtonText / panelContrastFG / createButton port herdr's
// own action-button convention (READ-ONLY reference per the task brief,
// Apache-2.0, attributed generally in this repository's NOTICE per spec
// §14: /home/zvi/Projects/herdr/src/ui/widgets.rs lines 151-210 --
// action_button_text, render_action_button, panel_contrast_fg, and the
// specific fg/bg/bold combination herdr's own dialogs.rs call sites use
// for a primary action button), imitating its SHAPE rather than
// translating it line-by-line per the brief -- ratatui's Frame/Rect/
// Style/Paragraph model has no lipgloss equivalent, so the port is
// conceptual:
//
//   - actionButtonText ports herdr's own action_button_text near-
//     verbatim (it's pure string formatting -- `" {hint} {label} "` /
//     `" {label} "` -- no ratatui type touches it, so there's nothing
//     about it that needed reshaping).
//   - panelContrastFG ports herdr's own panel_contrast_fg near-verbatim:
//     the text color for content drawn on an accent-filled background is
//     the panel's OWN background color (a "knocked out" look consistent
//     with the active theme, rather than a fixed black/white), falling
//     back to a dimmer color when panel_bg is the "inherit the
//     terminal's own" sentinel (herdr's Color::Reset; herdr-draft's
//     lipgloss.NoColor{}, see internal/theme's own doc). herdr falls
//     back to its own `surface_dim` field; this package falls back to
//     palette.Border, the field internal/theme's own package doc maps
//     surface_dim onto ("very dim surface for separators").
//   - createButtonFace takes herdr's dialogs.rs call-site convention
//     for a PRIMARY button --
//     `Style::default().fg(panel_contrast_fg(&app.palette)).bg(app.palette.accent).add_modifier(Modifier::BOLD)`,
//     hint "↵" -- and cancelButton takes the SECONDARY convention beside
//     it (v2 spec §7: "the primary filled with the accent color, the
//     secondary on a surface background").
//
// An earlier round rendered the unfocused primary as Atrium's plain
// renderEnterButton -- no fill, no key glyph -- on the reasoning that
// "this form has exactly one button, and its color instead needs to
// convey FOCUS ... there is no second button here for color to
// distinguish it from." v2 spec §4's footer has two, so that premise is
// gone: color tells primary from secondary exactly as herdr's own dialogs
// use it. Both states keep the key glyph, because §7 makes the glyph part
// of what a button IS, and because the footer's key ladder no longer
// repeats ↵ anywhere (footer.go's zoneRungs).
func actionButtonText(hint, label string) string {
	if hint == "" {
		return " " + label + " "
	}
	return " " + hint + " " + label + " "
}

func panelContrastFG(p theme.Palette) theme.Color {
	if _, inherit := p.PanelBG.(lipgloss.NoColor); inherit {
		return p.Border
	}
	return p.PanelBG
}

// createButtonFace returns the primary button's text and style -- the
// herdr port described above, with no sizing of its own. The label is
// lowercase because every other word on this screen is (v2 spec §3 rule
// 5, "copy is plain, lowercase and active"), and it carries its key glyph
// (v2 spec §7).
//
// ONE face, not two: v3 spec §5.5 makes `↵ create` filled, full stop.
// v2 spent the fill on focus instead -- accent as the fill when focused,
// accent as the TEXT on a Surface fill when not -- which meant the button
// a user actually sees, the resting one, was the weaker half of herdr's
// recipe on the busiest line of the screen. herdr fills its own primary
// unconditionally (`dialogs.rs:324-343`); a dialog's default action reads
// as the default action whether or not the cursor happens to be parked on
// it.
//
// Create is still a focus stop, and its focus is still visible: landing
// on it swaps the footer's whole key ladder to `⇧⇥ back to the form`
// (footer.go's zoneRungs, ZoneCreate) -- the same line the button sits
// on, so the two are read together.
func createButtonFace(p theme.Palette) (string, lipgloss.Style) {
	return actionButtonText("↵", "create"), lipgloss.NewStyle().
		Foreground(panelContrastFG(p)).
		Background(p.Accent).
		Bold(true)
}

// createButton renders the primary button at its INTRINSIC width, for
// the footer line (v2 spec §5): renderFooter places it flush right and
// fits the key ladder into what is left.
func createButton(p theme.Palette) string {
	text, style := createButtonFace(p)
	return style.Inline(true).Render(text)
}

// cancelButton renders the SECONDARY footer button, `esc cancel` -- the
// second half of v2 spec §4's footer, on herdr's own surface-filled
// secondary face (v2 spec §7).
//
// It has no focused state because it is not a focus stop: esc works from
// every field, and the ring never lands here. Rendering it beside Create
// is what lets footer.go's key ladder stop repeating `Esc cancel` on
// every zone -- the button says it once, in the place the eye already
// goes for an action.
func cancelButton(p theme.Palette) string {
	return lipgloss.NewStyle().
		Foreground(p.Text).
		Background(p.Surface).
		Inline(true).
		Render(actionButtonText("esc", "cancel"))
}
