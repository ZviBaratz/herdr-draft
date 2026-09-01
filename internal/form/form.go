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
// documented at renderCreateButton below.
package form

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// Section is one field/region of the form: a Linear issue picker, the
// project directory picker, the Title field, ... spec §6's field-order
// list items 1-8, plus this package's own internal Create button (item
// 9), which New always appends after every caller-supplied Section. It
// is deliberately opaque beyond this interface -- form.go's focus ring,
// composition, and degradation ladder all work identically over every
// Section, whether it's one of Tasks 17-18's real fields or this task's
// own test doubles, without knowing which.
//
// Setter methods a concrete field needs (SetItems, SetChips, ...) are NOT
// part of this interface -- per the brief, "Setter methods used by the
// app layer land with each field task": the app layer is expected to
// hold each concrete Section by its own concrete type (or a small
// additional interface) to drive it; this package only ever holds the
// slice as []Section.
//
// ID() convention: a concrete field Section's ID() should return the
// lowercase zone name spec §6's field order uses -- "issue", "dir",
// "title", "worktree", "branch", "base", "placement", "agent", "account",
// "prompt" (this package's own internal Create section uses "create") --
// so zoneFor (below) can map the focused Section back to the ZoneKind
// keys.go's MapKey grammar needs. An ID outside that set (e.g. a test's
// stub section) is treated as a "plain" zone: MapKey advances/backs on
// Tab/Shift+Tab exactly like every non-picker, non-Title, non-Prompt,
// non-Create zone spec §6 defines -- the correct behavior for a field
// kind this package doesn't know about yet.
//
// Deliberate deviation from the brief's literal interface, flagged for
// controller review (see task-16-report.md's "Section interface as
// built" section for the full writeup): Focus() here returns tea.Cmd,
// where the brief's Interfaces line lists it as `Focus()` with no return.
// This widens, rather than breaks, the given shape -- every method the
// brief lists is still present with the same name and receiver shape,
// only Focus()'s result type changed from nothing to tea.Cmd -- and it
// is not optional: the task's own "verified facts" state that
// widgets.PromptArea.Focus() returns the cursor's blink tea.Cmd, which
// "must be folded into your Update batching or the cursor never
// blinks." A Section wrapping PromptArea (a future Prompt field, Tasks
// 17-18) has no other channel to hand that Cmd back to this package's
// focus ring: Update(tea.Msg) only fires on an incoming message, never on
// a focus change, and Blur() (unchanged, still void, matching
// PromptArea.Blur()) has nowhere to put a Cmd it never receives. A void
// Focus() would silently defeat exactly the bug the brief's own
// "hard-won fact" warns about. focus.go's `set` is where this Cmd is
// collected and handed back to form.go's Update.
type Section interface {
	// ID identifies the section (see the ID() convention above).
	ID() string
	// Enabled reports whether the section can currently take focus.
	// focus.go's ring skips disabled sections, with wrap-around, but
	// still renders them (present-but-inert, spec §6) at their normal
	// height.
	Enabled() bool
	// Focus gives the section input focus, returning whatever tea.Cmd
	// that requires (e.g. a cursor blink loop) -- see the deviation note
	// above.
	Focus() tea.Cmd
	// Blur removes input focus.
	Blur()
	// Update forwards a message the form grammar (keys.go's MapKey)
	// classified as ActionNone -- "not part of the grammar, forward it"
	// -- or any message class MapKey never intercepts (e.g. tea.PasteMsg
	// content, or a cursor-blink tick) to the section's own editing
	// behavior.
	Update(tea.Msg) tea.Cmd
	// View renders the section into exactly Height(winH) physical lines
	// (for the inner width the caller currently has, spec §6's
	// constant-height contract) at the given inner content width.
	View(inner int) string
	// Height reports how many physical lines View(inner) renders at,
	// for a popup winH rows tall. It must depend only on winH (and the
	// section's own already-set internal state, e.g. a future concrete
	// field's own SetRows-style setter) -- NEVER on whether the section
	// currently has focus, per the task's own verified fact: "a hint
	// line must always be reserved... Section.Height() must be
	// hint-independent." This is what keeps the composed form from
	// jumping as focus moves between sections at a fixed window size.
	Height(winH int) int
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
)

// zoneKindByID maps a Section's canonical ID() (see Section's own doc
// comment) to the ZoneKind MapKey's grammar needs. Worktree/Placement/
// Agent share one ZoneKind assignment here purely as a stand-in for "no
// special case" -- MapKey's behavior for all three is identical (plain
// advance/back, no Tab-complete, no newline, Enter advances) -- since
// ZoneKind has no dedicated "plain" member of its own.
var zoneKindByID = map[string]ZoneKind{
	"issue":     ZoneIssue,
	"dir":       ZoneDir,
	"title":     ZoneTitle,
	"worktree":  ZoneWorktree,
	"branch":    ZoneBranch,
	"base":      ZoneBase,
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
	// Sections are the caller's own form fields, in spec §6's field
	// order. New appends its own internal Create section after these,
	// always last -- callers must not include their own "create" ID.
	Sections []Section
}

// Model is the form root: a tea.Model over Setup's Sections plus this
// package's own Create button, wiring focus.go's ring, sizes.go's
// constant-height budget/degradation ladder, footer.go's key ladder, and
// keys.go's MapKey grammar together, painted in Setup.Palette's herdr
// skin.
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
	return Model{
		palette: cfg.Palette,
		ring:    newFocusRing(sections),
	}
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
	default:
		return m, m.forwardToFocused(msg)
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
func (m Model) ViewAt(w, h int) string {
	content := m.compose(w, h)
	var buf strings.Builder
	cw := &colorprofile.Writer{Forward: &buf, Profile: colorprofile.TrueColor}
	_, _ = cw.WriteString(content)
	return buf.String()
}

// compose assembles the form's content at exactly w x h: one blank
// padding row top and bottom, each section's own View(inner) lines
// (decorated with the focus-marker gutter, see decorateFocus) followed by
// a divider, the footer's key ladder, then the trailing Create section --
// always last, by construction (New always appends it). sizes.go's
// fitToHeight then applies spec §6's degradation ladder if the composed
// content is taller than h, and every line is finally painted with
// Setup.Palette.PanelBG across the full w columns (spec §7: "panel
// background painted explicitly across the full popup area").
//
// A zero-value Model (nil ring; see Model's own doc comment) renders as
// "" -- there are no Sections to compose -- rather than dereferencing the
// nil ring's own sections slice.
func (m Model) compose(w, h int) string {
	if h <= 0 || m.ring == nil {
		return ""
	}

	inner := innerWidth(w)
	sections := m.ring.sections

	divider := decorateFocus(dividerLine(inner, m.palette), false, m.palette)
	blank := decorateFocus("", false, m.palette)

	lines := make([]string, 0, h+len(sections)*2)
	for i := 0; i < verticalPadding; i++ {
		lines = append(lines, blank)
	}

	body, last := sections[:len(sections)-1], sections[len(sections)-1]
	for i, s := range body {
		focused := i == m.ring.index
		for _, l := range strings.Split(s.View(inner), "\n") {
			lines = append(lines, decorateFocus(l, focused, m.palette))
		}
		lines = append(lines, divider)
	}

	lines = append(lines, decorateFocus(fitFooter(footerRungs(m.clearArmed), inner), false, m.palette))

	lastFocused := m.ring.index == len(sections)-1
	for _, l := range strings.Split(last.View(inner), "\n") {
		lines = append(lines, decorateFocus(l, lastFocused, m.palette))
	}

	// Deliberately no trailing bottom padding here: sizes.go's clipTail
	// (via fitToHeight) preserves whatever line is structurally LAST,
	// mirroring Atrium's own fitOverlay ("it is the submit button in
	// both compose() branches") -- so the Create section's own rendered
	// line(s) must actually BE the last line(s) this function appends.
	// An earlier version of this method appended a blank padding line
	// after Create and broke that invariant outright (caught by
	// TestDegradation_CreateNeverClippedAt80x20: the last row came back
	// blank instead of containing "Create").

	lines = fitToHeight(lines, h, divider, -1)

	painted := make([]string, 0, h)
	for _, l := range lines {
		if len(painted) == h {
			break
		}
		painted = append(painted, paintLine(l, w, m.palette.PanelBG))
	}
	for len(painted) < h {
		painted = append(painted, paintLine("", w, m.palette.PanelBG))
	}

	return strings.Join(painted, "\n")
}

// decorateFocus prefixes line with the focus-marker gutter: an
// accent-colored bar and a space when focused is true, two blank spaces
// otherwise -- spec §7's "accent for the focused section marker"
// convention. It never changes line's own physical line count (always
// exactly one line in, one out) and is applied uniformly to every
// composed line (section content, dividers, blank padding, the footer),
// so the gutter column stays aligned regardless of which lines are
// section content -- only per-SECTION content lines ever pass
// focused=true, everything else always passes false.
func decorateFocus(line string, focused bool, p theme.Palette) string {
	marker := "  "
	if focused {
		marker = lipgloss.NewStyle().Foreground(p.Accent).Render("▎") + " "
	}
	return marker + line
}

// dividerLine renders a horizontal rule inner cells wide in the palette's
// Border color, matching Atrium's own divider convention
// (textInput_render.go's tiDividerStyle/`strings.Repeat("─", innerWidth)`,
// ported here as styling only -- the repeated-rune choice, not a literal
// code port, since Atrium's own construction is a one-line call this
// package could not import across packages anyway).
func dividerLine(inner int, p theme.Palette) string {
	if inner < 0 {
		inner = 0
	}
	return lipgloss.NewStyle().Foreground(p.Border).Render(strings.Repeat("─", inner))
}

// createSection is form.go's own internal Section for spec §6 field 9,
// "Create": the form's last focus stop, always enabled, and (by
// construction -- New always appends exactly one of these after the
// caller's own Setup.Sections, and compose renders sections in ring
// order) always the last line of composed content, which is what makes
// sizes.go's clipTail's "never drop the last line" contract equivalent to
// spec §6's "never clipped" for this specific section.
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

func (c *createSection) Height(int) int { return 1 }

func (c *createSection) View(inner int) string {
	return renderCreateButton(inner, c.focused, c.palette)
}

// actionButtonText / panelContrastFG / renderCreateButton port herdr's
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
//   - renderCreateButton takes herdr's dialogs.rs call-site convention
//     for a PRIMARY button --
//     `Style::default().fg(panel_contrast_fg(&app.palette)).bg(app.palette.accent).add_modifier(Modifier::BOLD)`,
//     hint "↵" -- for this section's FOCUSED state.
//
// What's NOT ported, and is this task's own synthesis (flagged in
// task-16-report.md): herdr's own call sites give DIFFERENT buttons
// different FIXED colors regardless of focus (accent-filled for the
// primary action, a dimmer surface0-filled style for a cancel/clear
// button rendered alongside it), because herdr's own dialogs render more
// than one button at once and use color to tell primary from secondary.
// This form has exactly one button, and its color instead needs to
// convey FOCUS (whether the ring is currently on it) -- there is no
// second button here for color to distinguish it from. So the unfocused
// state instead mirrors Atrium's own renderEnterButton
// (textInput_render.go, on the clean list): plain text, no background
// fill, no hint glyph, until the ring actually reaches it.
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

func renderCreateButton(inner int, focused bool, p theme.Palette) string {
	text := actionButtonText("", "Create")
	style := lipgloss.NewStyle().Foreground(p.Text)
	if focused {
		text = actionButtonText("↵", "Create")
		style = lipgloss.NewStyle().
			Foreground(panelContrastFG(p)).
			Background(p.Accent).
			Bold(true)
	}
	if inner < 1 {
		inner = 1
	}
	return style.Width(inner).MaxWidth(inner).Inline(true).AlignHorizontal(lipgloss.Center).Render(text)
}
