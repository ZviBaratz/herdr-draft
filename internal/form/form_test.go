package form

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// update regenerates the golden frames under testdata/frames instead of
// comparing against them -- the brief's own harness contract ("run with
// -update to regenerate").
var update = flag.Bool("update", false, "regenerate golden frames")

// assertFrame is the brief's own golden-frame harness (task-16-brief.md
// step 2), adapted to this package's own "internal test package, not an
// external _test package" convention (every existing test file in
// internal/form and internal/form/widgets -- keys_test.go, picker_test.go,
// chiprow_test.go, textarea_test.go -- is `package form`/`package
// widgets`, never `..._test`; the brief's own snippet writes `m
// form.Model` because it's illustrating the shape from outside the
// package, which this file, being inside it, spells as plain `Model`).
func assertFrame(t *testing.T, name string, m Model, w, h int) {
	t.Helper()
	got := m.ViewAt(w, h)
	path := filepath.Join("testdata", "frames", name+".txt")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil || string(want) != got {
		t.Errorf("frame %s mismatch (run with -update to regenerate)\n%s", name, got)
	}
}

// stubSection is a minimal, fully configurable Section test double. It
// also spies on Focus/Blur/Update call counts so focus-ring and
// grammar-wiring tests can assert on behavior, not just rendered output.
type stubSection struct {
	id      string
	enabled bool

	label      string
	value      string
	panelLines []string

	focused     bool
	focusCalls  int
	blurCalls   int
	updateCalls int
	updateMsgs  []tea.Msg
}

func newStub(id string) *stubSection {
	return &stubSection{
		id:      id,
		enabled: true,
		label:   id,
		value:   id + " value",
	}
}

// withPanel gives the stub a panel n lines tall, each line naming its
// own index so a test can tell real panel content from the blank fill
// the form adds under it.
func (s *stubSection) withPanel(n int) *stubSection {
	s.panelLines = make([]string, n)
	for i := range s.panelLines {
		s.panelLines[i] = fmt.Sprintf("%s panel %d", s.id, i)
	}
	return s
}

func (s *stubSection) ID() string    { return s.id }
func (s *stubSection) Enabled() bool { return s.enabled }
func (s *stubSection) Label() string { return s.label }

// Row deliberately consults nothing but the width it is handed -- not
// the window height, not its own focus state. That is the contract
// TestRowStack_RowsRenderIdenticallyAtEveryHeight enforces from the
// outside.
func (s *stubSection) Row(w int) string { return fitLine(s.value, w) }

func (s *stubSection) Panel(w, h int) string { return sectionLines(h, w, s.panelLines...) }

func (s *stubSection) PanelRows() int { return len(s.panelLines) }

func (s *stubSection) Focus() tea.Cmd {
	s.focused = true
	s.focusCalls++
	return nil
}

func (s *stubSection) Blur() {
	s.focused = false
	s.blurCalls++
}

func (s *stubSection) Update(msg tea.Msg) tea.Cmd {
	s.updateCalls++
	s.updateMsgs = append(s.updateMsgs, msg)
	return nil
}

// titledStub layers titleValuer onto a stubSection, for zoneFor's
// TitleEmpty wiring.
type titledStub struct {
	stubSection
	value string
}

func (t *titledStub) Value() string { return t.value }

// hintingStub layers footerHinter (form.go) onto a stubSection, for the
// contextual footer.
type hintingStub struct {
	stubSection
	rungs []string
}

func (s *hintingStub) FooterRungs() []string { return s.rungs }

// --- focus ring: wrap + skip disabled -----------------------------------

// TestModel_FocusRingSkipsDisabledAndWraps is task-16 brief step 3's
// literal requirement: "focus ring wraps and skips disabled." Three
// caller sections (a enabled, b DISABLED, c enabled) plus New's own
// always-enabled internal "create" section give a four-stop ring
// [a, b, c, create]. Advancing from a must land on c (skipping disabled
// b); advancing again reaches create; advancing a third time must wrap
// all the way back to a (skipping b again, from the other direction).
// Shift+Tab (Back) is checked to wrap and skip symmetrically.
func TestModel_FocusRingSkipsDisabledAndWraps(t *testing.T) {
	a := newStub("a")
	b := newStub("b")
	b.enabled = false
	c := newStub("c")

	m := New(Setup{Palette: theme.Default(), Sections: []Section{a, b, c}})
	if _, cmd := m.Update(nil); cmd != nil {
		t.Fatalf("Update(nil) returned a non-nil cmd: %v", cmd)
	}
	if cmd := m.Init(); cmd != nil {
		// createSection.Focus and stubSection.Focus both return nil, so
		// Init should too, but don't assume -- fail loudly if that ever
		// changes silently.
		t.Fatalf("Init() returned a non-nil cmd: %v", cmd)
	}

	current := func() string { return m.ring.current().ID() }

	if got := current(); got != "a" {
		t.Fatalf("initial focus = %q, want %q", got, "a")
	}
	if !a.focused {
		t.Fatalf("a.focused = false after Init")
	}

	step := func(msg tea.KeyPressMsg) {
		t.Helper()
		next, _ := m.Update(msg)
		m = next.(Model)
	}

	step(keyTab)
	if got := current(); got != "c" {
		t.Fatalf("after 1 Tab from a: focus = %q, want %q (b is disabled)", got, "c")
	}
	if b.focusCalls != 0 {
		t.Fatalf("disabled section b was Focus()ed %d times, want 0", b.focusCalls)
	}
	if !c.focused || a.focused {
		t.Fatalf("focus state after 1 Tab: a.focused=%v c.focused=%v, want a=false c=true", a.focused, c.focused)
	}

	step(keyTab)
	if got := current(); got != "create" {
		t.Fatalf("after 2 Tabs from a: focus = %q, want %q", got, "create")
	}

	step(keyTab)
	if got := current(); got != "a" {
		t.Fatalf("after 3 Tabs from a: focus = %q, want %q (wraps, skipping disabled b)", got, "a")
	}
	if b.focusCalls != 0 {
		t.Fatalf("disabled section b was Focus()ed %d times across a full wrap, want 0", b.focusCalls)
	}

	// Shift+Tab (Back) from a must wrap the OTHER way, straight to
	// create, skipping disabled b in the backward direction too.
	step(keyShiftTab)
	if got := current(); got != "create" {
		t.Fatalf("after Shift+Tab from a: focus = %q, want %q (wraps backward, skipping disabled b)", got, "create")
	}
}

// TestModel_FocusedID pins FocusedID (added in Task 20 for the app layer's
// own "clauth: load ... on account focus" reaction, see its own doc
// comment): it must track the ring's own current section across Tab moves,
// and degrade to "" for an unconstructed zero-value Model.
func TestModel_FocusedID(t *testing.T) {
	var zero Model
	if got := zero.FocusedID(); got != "" {
		t.Fatalf("zero-value Model.FocusedID() = %q, want \"\"", got)
	}

	a := newStub("a")
	c := newStub("c")
	m := New(Setup{Palette: theme.Default(), Sections: []Section{a, c}})
	m.Init()

	if got := m.FocusedID(); got != "a" {
		t.Fatalf("FocusedID() after Init = %q, want %q", got, "a")
	}

	next, _ := m.Update(keyTab)
	m = next.(Model)
	if got := m.FocusedID(); got != "c" {
		t.Fatalf("FocusedID() after 1 Tab = %q, want %q", got, "c")
	}

	next, _ = m.Update(keyTab)
	m = next.(Model)
	if got := m.FocusedID(); got != "create" {
		t.Fatalf("FocusedID() after 2 Tabs = %q, want %q", got, "create")
	}
}

// TestModel_SectionIDs pins SectionIDs' own reason for existing (Task 20
// fix round 1): unlike Tab-driven navigation, it must include a DISABLED
// section too, at its real construction position -- a plain Tab-walk would
// silently skip it (focus.go's nextEnabled), which is exactly the gap that
// let a real construction-order regression slip past an earlier version of
// this test suite undetected.
func TestModel_SectionIDs(t *testing.T) {
	var zero Model
	if got := zero.SectionIDs(); got != nil {
		t.Fatalf("zero-value Model.SectionIDs() = %v, want nil", got)
	}

	a := newStub("a")
	b := newStub("b")
	b.enabled = false
	c := newStub("c")
	m := New(Setup{Palette: theme.Default(), Sections: []Section{a, b, c}})

	got := m.SectionIDs()
	want := []string{"a", "b", "c", "create"}
	if len(got) != len(want) {
		t.Fatalf("SectionIDs() = %v, want %v", got, want)
	}
	for i, id := range want {
		if got[i] != id {
			t.Fatalf("SectionIDs()[%d] = %q, want %q (full: %v)", i, got[i], id, got)
		}
	}
}

// TestModel_FocusByID pins FocusByID (added in Task 20b for the app
// layer's own submit-time validation, spec §9: "a failing submit
// re-focuses Title" -- see focus.go's own already-existing focusByID doc
// comment, which named exactly this use case before this public entry
// point existed): it must move the ring directly to the named section
// (skipping the wrap/skip-disabled walk Tab uses), work even when that
// section is currently DISABLED (focusByID's own documented contract,
// unlike Tab navigation), and degrade to a nil Cmd for both an unknown id
// and a zero-value Model.
func TestModel_FocusByID(t *testing.T) {
	var zero Model
	if cmd := zero.FocusByID("a"); cmd != nil {
		t.Fatalf("zero-value Model.FocusByID(\"a\") returned a non-nil Cmd, want nil")
	}

	a := newStub("a")
	b := newStub("b")
	b.enabled = false
	c := newStub("c")
	m := New(Setup{Palette: theme.Default(), Sections: []Section{a, b, c}})
	m.Init()

	if got := m.FocusedID(); got != "a" {
		t.Fatalf("FocusedID() after Init = %q, want %q", got, "a")
	}

	m.FocusByID("c")
	if got := m.FocusedID(); got != "c" {
		t.Fatalf("FocusedID() after FocusByID(\"c\") = %q, want %q", got, "c")
	}

	// Unlike Tab, FocusByID reaches a DISABLED section directly.
	m.FocusByID("b")
	if got := m.FocusedID(); got != "b" {
		t.Fatalf("FocusedID() after FocusByID(\"b\") (disabled) = %q, want %q", got, "b")
	}

	if cmd := m.FocusByID("no-such-id"); cmd != nil {
		t.Fatalf("FocusByID(\"no-such-id\") returned a non-nil Cmd, want nil")
	}
	if got := m.FocusedID(); got != "b" {
		t.Fatalf("FocusedID() after an unknown FocusByID = %q, want unchanged %q", got, "b")
	}
}

// --- MapKey wiring: Submit/Cancel/Clear/ActionNone forwarding ----------

// TestModel_CtrlSSubmitsFromAnyZone checks handleKey's ActionSubmit wiring:
// Ctrl+S must produce a tea.Cmd whose invocation yields SubmitMsg{},
// regardless of which section (including a non-title, non-create one) is
// currently focused -- keys.go's MapKey documents Ctrl+S as
// submit-from-anywhere.
func TestModel_CtrlSSubmitsFromAnyZone(t *testing.T) {
	a := newStub("a")
	m := New(Setup{Palette: theme.Default(), Sections: []Section{a}})
	m.Init()

	_, cmd := m.Update(keyCtrlS)
	if cmd == nil {
		t.Fatalf("Ctrl+S produced a nil cmd")
	}
	if _, ok := cmd().(SubmitMsg); !ok {
		t.Fatalf("Ctrl+S cmd's message = %#v, want SubmitMsg{}", cmd())
	}
}

// TestModel_EnterSubmitsFromTheTitleAndThePrompt pins v2 spec §8's
// fast path end to end, through the real Sections rather than through
// MapKey alone: a non-empty title and the prompt both CREATE on Enter,
// and the prompt's newline keys still insert newlines instead.
//
// The prompt half is the change §8 makes ("↵ from the prompt submits
// rather than advancing. Nothing used it for a newline; ⌃J, ⇧↵ and ⌥↵
// keep that job") and the half the companion view plan's footer table
// got wrong; the title half is unchanged and here to keep the pair
// asserted together, since "open, type a title, Enter" and "type the
// prompt, Enter" are one contract from the user's side.
func TestModel_EnterSubmitsFromTheTitleAndThePrompt(t *testing.T) {
	palette := theme.Default()
	title := NewTitleField(palette)
	title.SetTitle("fix login redirect loop", false)
	prompt := NewPromptField(palette)

	m := New(Setup{Palette: palette, Sections: []Section{title, prompt}, InitialFocusID: "title"})
	m.Init()

	_, cmd := m.Update(keyEnter)
	if cmd == nil {
		t.Fatalf("Enter on a non-empty title produced a nil cmd")
	}
	if _, ok := cmd().(SubmitMsg); !ok {
		t.Fatalf("Enter on a non-empty title = %#v, want SubmitMsg{}", cmd())
	}

	if c := m.FocusByID("prompt"); c != nil {
		c()
	}
	_, cmd = m.Update(keyEnter)
	if cmd == nil {
		t.Fatalf("Enter in the prompt produced a nil cmd")
	}
	if _, ok := cmd().(SubmitMsg); !ok {
		t.Fatalf("Enter in the prompt = %#v, want SubmitMsg{}", cmd())
	}
	if got := prompt.Value(); got != "" {
		t.Fatalf("prompt value after Enter = %q, want Enter to submit rather than type", got)
	}

	// ...and the three newline keys still are newline keys.
	for _, k := range []tea.KeyPressMsg{keyCtrlJ, keyShiftEnter, keyAltEnter} {
		before := prompt.Value()
		_, cmd = m.Update(k)
		if cmd != nil {
			if _, ok := cmd().(SubmitMsg); ok {
				t.Fatalf("%s in the prompt submitted; it must insert a newline", k.String())
			}
		}
		if prompt.Value() == before {
			t.Fatalf("%s in the prompt left the value at %q, want a newline inserted", k.String(), before)
		}
	}
}

// TestModel_EscCancels checks handleKey's ActionCancel wiring.
func TestModel_EscCancels(t *testing.T) {
	m := New(Setup{Palette: theme.Default(), Sections: []Section{newStub("a")}})
	m.Init()

	_, cmd := m.Update(keyEsc)
	if cmd == nil {
		t.Fatalf("Esc produced a nil cmd")
	}
	if _, ok := cmd().(CancelMsg); !ok {
		t.Fatalf("Esc cmd's message = %#v, want CancelMsg{}", cmd())
	}
}

// TestModel_CtrlRCtrlRClears checks the double-tap clear gesture end to
// end: the first Ctrl+R only arms it (no ClearRequestedMsg yet, and the
// footer's rendered hint switches from "⌃R clear" to "⌃R again" --
// asserted directly on the unexported clearArmed field, same
// same-package white-box convention keys_test.go itself uses), the
// second, consecutive Ctrl+R fires it.
func TestModel_CtrlRCtrlRClears(t *testing.T) {
	m := New(Setup{Palette: theme.Default(), Sections: []Section{newStub("a")}})
	m.Init()

	next, cmd := m.Update(keyCtrlR)
	m = next.(Model)
	if cmd != nil {
		t.Fatalf("first Ctrl+R produced a non-nil cmd (should only arm): %v", cmd())
	}
	if !m.clearArmed {
		t.Fatalf("m.clearArmed = false after first Ctrl+R, want true")
	}

	next, cmd = m.Update(keyCtrlR)
	m = next.(Model)
	if cmd == nil {
		t.Fatalf("second consecutive Ctrl+R produced a nil cmd")
	}
	if _, ok := cmd().(ClearRequestedMsg); !ok {
		t.Fatalf("second Ctrl+R cmd's message = %#v, want ClearRequestedMsg{}", cmd())
	}
	if m.clearArmed {
		t.Fatalf("m.clearArmed = true after the clear fired, want false (MapKey disarms on every keypress but a first Ctrl+R)")
	}
}

// TestModel_UnclassifiedKeyForwardsToFocusedSection checks ActionNone's
// forwarding path: a keypress MapKey's grammar doesn't classify (a plain
// rune) must reach the focused section's own Update, and only that one.
func TestModel_UnclassifiedKeyForwardsToFocusedSection(t *testing.T) {
	a, b := newStub("a"), newStub("b")
	m := New(Setup{Palette: theme.Default(), Sections: []Section{a, b}})
	m.Init()

	m.Update(keyX)

	if a.updateCalls != 1 {
		t.Fatalf("focused section a.updateCalls = %d, want 1", a.updateCalls)
	}
	if b.updateCalls != 0 {
		t.Fatalf("unfocused section b.updateCalls = %d, want 0", b.updateCalls)
	}
}

// --- zoneFor -------------------------------------------------------------

func TestZoneFor(t *testing.T) {
	cases := []struct {
		name string
		s    Section
		want ZoneKind
	}{
		{"nil section", nil, ZonePlacement},
		{"canonical dir", newStub("dir"), ZoneDir},
		{"canonical prompt", newStub("prompt"), ZonePrompt},
		{"canonical create", newStub("create"), ZoneCreate},
		{"unknown ID falls back to a plain zone", newStub("widget-x"), ZonePlacement},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := zoneFor(c.s).Kind; got != c.want {
				t.Errorf("zoneFor(%v).Kind = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

// TestZoneFor_EveryRealFieldHasItsOwnZone guards the hole zoneKindByID's
// fallback leaves open: an ID it does not know silently becomes
// ZonePlacement, which is the right answer for a test stub and a wrong,
// SILENT one for a real field. The worktree collapse removed two entries
// from that map, so this is the assertion that says nothing fell through
// with them.
func TestZoneFor_EveryRealFieldHasItsOwnZone(t *testing.T) {
	palette := theme.Default()
	want := map[string]ZoneKind{
		"issue":     ZoneIssue,
		"title":     ZoneTitle,
		"prompt":    ZonePrompt,
		"dir":       ZoneDir,
		"worktree":  ZoneWorktree,
		"placement": ZonePlacement,
		"agent":     ZoneAgent,
		"account":   ZoneAccount,
	}
	fields := []Section{
		NewIssueField(palette), NewTitleField(palette), NewPromptField(palette),
		NewDirField(palette), NewWorktreeField(palette), NewPlacementField(palette),
		NewAgentField(palette), NewAccountField(palette),
	}
	for _, f := range fields {
		expected, known := want[f.ID()]
		if !known {
			t.Fatalf("field %q is not in this test's own table -- add it when adding a field", f.ID())
		}
		if got := zoneFor(f).Kind; got != expected {
			t.Errorf("zoneFor(%q).Kind = %v, want %v", f.ID(), got, expected)
		}
	}
}

func TestZoneFor_TitleEmpty(t *testing.T) {
	empty := &titledStub{stubSection: *newStub("title"), value: "  "}
	filled := &titledStub{stubSection: *newStub("title"), value: "hello"}
	noValuer := newStub("title") // ID "title" but no Value() method.

	if !zoneFor(empty).TitleEmpty {
		t.Errorf("zoneFor(blank Value()).TitleEmpty = false, want true")
	}
	if zoneFor(filled).TitleEmpty {
		t.Errorf("zoneFor(non-blank Value()).TitleEmpty = true, want false")
	}
	if !zoneFor(noValuer).TitleEmpty {
		t.Errorf("zoneFor(no titleValuer).TitleEmpty = false, want true (conservative default)")
	}
}

// --- constant-height invariant -------------------------------------------

// TestModel_ConstantHeightAcrossFocusMoves pins what survives of spec §6's
// "constant-height sections for a given window size": at a FIXED (w, h),
// moving focus never changes the composed render's own line count -- it is
// always exactly h, so nothing above or below the form shifts as the user
// Tabs.
//
// TestRowStack_RowsNeverMoveAcrossFocus is the sharper claim this one
// could not make in v1, where a shared height budget refilled the focused
// section and reflowed the whole form.
func TestModel_ConstantHeightAcrossFocusMoves(t *testing.T) {
	m := New(Setup{Palette: theme.Default(), Sections: []Section{newStub("a"), newStub("b")}})
	m.Init()

	before := strings.Count(m.ViewAt(80, 24), "\n")

	next, _ := m.Update(keyTab)
	m = next.(Model)
	next, _ = m.Update(keyTab)
	m = next.(Model)

	after := strings.Count(m.ViewAt(80, 24), "\n")

	if before != after {
		t.Fatalf("composed line count changed as focus moved: before=%d after=%d", before, after)
	}
}

// --- zero-value Model safety ----------------------------------------------

// TestZeroValueModel_DoesNotPanic pins this project's "no panics in
// production code" rule against a zero-value Model (`var m Model`, or any
// Model obtained some way other than New): Model's own doc comment says
// construction via New is required, but a caller that gets this wrong
// must still fail safely, not crash. Every entry point
// (Init/Update/View/ViewAt) is exercised, including four different
// message kinds through Update -- a window resize (the one message a
// zero-value Model must still record, since width/height are plain
// fields, not ring-derived), a key press (would otherwise reach
// handleKey's zoneFor/MapKey/ring dispatch), a paste, and a message class
// Update's switch never classifies at all -- inside a single deferred
// recover, so a regression's failure names which call panicked rather
// than just "test crashed."
func TestZeroValueModel_DoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("zero-value Model panicked: %v", r)
		}
	}()

	var m Model

	if cmd := m.Init(); cmd != nil {
		t.Errorf("Init() on a zero-value Model returned a non-nil cmd")
	}

	next, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	if cmd != nil {
		t.Errorf("Update(WindowSizeMsg) on a zero-value Model returned a non-nil cmd")
	}
	if m.width != 80 || m.height != 24 {
		t.Errorf("Update(WindowSizeMsg) on a zero-value Model left width=%d height=%d, want 80x24 (width/height are plain fields, not ring-derived)", m.width, m.height)
	}

	next, cmd = m.Update(keyTab)
	m = next.(Model)
	if cmd != nil {
		t.Errorf("Update(a KeyPressMsg) on a zero-value Model returned a non-nil cmd")
	}

	next, cmd = m.Update(tea.PasteMsg{Content: "pasted text"})
	m = next.(Model)
	if cmd != nil {
		t.Errorf("Update(PasteMsg) on a zero-value Model returned a non-nil cmd")
	}

	next, cmd = m.Update(struct{ unclassified bool }{})
	m = next.(Model)
	if cmd != nil {
		t.Errorf("Update(an unclassified msg) on a zero-value Model returned a non-nil cmd")
	}

	if v := m.View(); v.Content != "" {
		t.Errorf("View() on a zero-value Model = %q, want empty content", v.Content)
	}

	if got := m.ViewAt(80, 24); got != "" {
		t.Errorf("ViewAt(80, 24) on a zero-value Model = %q, want \"\"", got)
	}
}

// --- golden frames ---------------------------------------------------------

// pickerSection and chipRowSection wrap this package's own real widgets
// (internal/form/widgets, Task 14) rather than fabricated placeholder
// content, so the golden frames below exercise -- and the paintLine
// background-painting fix in sizes.go is stress-tested against -- actual
// multi-span styled output (Picker's cursor-row accent highlighting and
// its "✓" Marker convention, ChipRow's cursor highlighting), not just
// plain unstyled stub text.
//
// Both render a one-line value row and put their widget in the panel,
// exactly as a real field does.
type pickerSection struct {
	id      string
	picker  *widgets.Picker
	enabled bool
	rows    int
}

func (s *pickerSection) ID() string             { return s.id }
func (s *pickerSection) Enabled() bool          { return s.enabled }
func (s *pickerSection) Focus() tea.Cmd         { return nil }
func (s *pickerSection) Blur()                  {}
func (s *pickerSection) Update(tea.Msg) tea.Cmd { return nil }
func (s *pickerSection) Height(int) int         { return s.rows }
func (s *pickerSection) MinHeight() int         { return 1 }
func (s *pickerSection) View(inner, h int) string {
	return s.picker.View(inner, h)
}
func (s *pickerSection) Label() string { return s.id }
func (s *pickerSection) Row(w int) string {
	if sel, ok := s.picker.Selected(); ok && len(sel.Cells) > 0 {
		return fitLine(sel.Cells[0], w)
	}
	return fitLine("none", w)
}
func (s *pickerSection) Panel(w, h int) string {
	return panelBlock(w, h, panelPickerLines(s.picker, w, h, "row:"+s.id+":", theme.Default())...)
}
func (s *pickerSection) PanelRows() int { return capRows(s.picker.FilteredLen(), s.rows) }

// chipRowSection always reserves the hint line regardless of whether the
// currently selected chip carries one -- the "field wrappers must always
// reserve the hint line" rule this task's own brief calls out
// (widgets.ChipRow.View's line count is otherwise hint-dependent; see
// chiprow.go's own doc comment), so Height stays a true constant.
type chipRowSection struct {
	id      string
	row     *widgets.ChipRow
	enabled bool
}

func (s *chipRowSection) ID() string             { return s.id }
func (s *chipRowSection) Enabled() bool          { return s.enabled }
func (s *chipRowSection) Focus() tea.Cmd         { return nil }
func (s *chipRowSection) Blur()                  {}
func (s *chipRowSection) Update(tea.Msg) tea.Cmd { return nil }
func (s *chipRowSection) Height(int) int         { return 2 }
func (s *chipRowSection) MinHeight() int         { return 1 }
func (s *chipRowSection) View(inner, h int) string {
	v := s.row.View(inner)
	if !strings.Contains(v, "\n") {
		pad := inner
		if pad < 0 {
			pad = 0
		}
		v += "\n" + strings.Repeat(" ", pad)
	}
	return fitBlock(v, h, inner)
}
func (s *chipRowSection) Label() string    { return s.id }
func (s *chipRowSection) Row(w int) string { return fitLine(s.row.Selected().Label, w) }
func (s *chipRowSection) PanelRows() int   { return 1 }
func (s *chipRowSection) Panel(w, h int) string {
	v := s.row.MarkedView(panelInner(w), "chip:"+s.id+":")
	if idx := strings.IndexByte(v, '\n'); idx >= 0 {
		v = v[:idx]
	}
	return panelBlock(w, h, panelMarked(v, false, theme.Default()))
}

// buildEmptyForm returns a form with two representative-but-still-stub
// Sections (an "issue"-shaped Picker, an "agent"-shaped ChipRow) in their
// default state, for the golden frames task-16 brief step 3 requires:
// one at an ordinary size and one with room to spare.
func buildEmptyForm(palette theme.Palette) Model {
	issue := widgets.NewPicker(palette)
	issue.SetColumns(widgets.PickerColumn{Tone: widgets.ToneMuted}, widgets.PickerColumn{Flex: true})
	// Current, not a "✓" Marker: v3 spec §8.2 gives the current-value
	// glyph to Current and reserves Marker for the attention "!", so a
	// literal check mark here would now collide with the glyph the picker
	// draws itself.
	issue.SetItems(1, []widgets.PickerItem{
		{ID: "eng-100", Cells: []string{"ENG-100", "Fix login bug"}, Badge: "In Progress", BadgeTone: widgets.ToneMuted},
		{ID: "eng-101", Cells: []string{"ENG-101", "Add dark mode"}, Badge: "Todo", BadgeTone: widgets.ToneMuted, Current: true},
		{ID: "eng-102", Cells: []string{"ENG-102", "Refactor auth"}, Badge: "Todo", BadgeTone: widgets.ToneMuted},
	})

	agent := widgets.NewChipRow(palette)
	agent.SetChips([]widgets.Chip{
		{ID: "claude", Label: "claude"},
		{ID: "codex", Label: "codex", FocusHint: "full access mode"},
	})

	m := New(Setup{
		Palette: palette,
		Sections: []Section{
			&pickerSection{id: "issue", picker: issue, enabled: true, rows: 3},
			&chipRowSection{id: "agent", row: agent, enabled: true},
		},
		Name: "new session",
	})
	m.SetContext("herdr-draft · main")
	m.Init()
	return m
}

func TestFrames_Empty(t *testing.T) {
	palette := theme.Default()
	assertFrame(t, "empty-80x24", buildEmptyForm(palette), 80, 24)
	// 150x44 rather than the retired 120x40 -- v3 spec §12; the oversized
	// frame's job is the layout with room to spare, and 120x40 is not a
	// size herdr's fixed-cell popup can produce.
	assertFrame(t, "empty-150x44", buildEmptyForm(palette), 150, 44)
}

// buildManySections returns a form with n stub row Sections, each asking
// for a panel `panelRows` tall -- more stack rows and more panel than a
// short window can hold at once, so v2's own degradation ladder
// (rowlayout.go's layoutFrame plus compose's stackWindow scroll) is
// actually forced to engage rather than never triggered.
func buildManySections(palette theme.Palette, n, panelRows int) Model {
	sections := make([]Section, 0, n)
	for i := 0; i < n; i++ {
		sections = append(sections, newStub(fmt.Sprintf("field-%d", i)).withPanel(panelRows))
	}
	m := New(Setup{Palette: palette, Sections: sections, Name: "new session"})
	m.Init()
	return m
}

// TestDegradation_FooterAndFocusedRowSurvive is v2's replacement for
// v1's "degradation at 80x20 keeps the Create button visible": the
// promise is the same one (spec §6 field 9, v2 spec §9's "the footer and
// its buttons are never dropped"), asserted at the two sizes below the
// popup floor where v2's ladder is doing real work -- 64x12, where the
// header and both rules are gone and the panel sits on its three-row
// floor, and 40x8, where the row stack itself has started scrolling.
//
// Six stub fields with four-row panels is deliberately more than either
// height can show, so a passing result is evidence the ladder ran, not
// that it was never needed.
func TestDegradation_FooterAndFocusedRowSurvive(t *testing.T) {
	palette := theme.Default()
	m := buildManySections(palette, 6, 4)

	assertFrame(t, "degraded-64x12", m, 64, 12)
	assertFrame(t, "degraded-40x8", m, 40, 8)

	for _, size := range []struct{ w, h int }{{64, 12}, {40, 8}} {
		lines := strings.Split(m.ViewAt(size.w, size.h), "\n")
		if len(lines) != size.h {
			t.Fatalf("ViewAt(%d, %d) produced %d rows, want exactly %d", size.w, size.h, len(lines), size.h)
		}
		last := ansi.Strip(lines[len(lines)-1])
		if !strings.Contains(last, "↵ create") {
			t.Fatalf("at %dx%d the last row does not carry the Create button: %q", size.w, size.h, last)
		}
		if !strings.Contains(ansi.Strip(strings.Join(lines, "\n")), "field-0 value") {
			t.Fatalf("at %dx%d the focused row is not visible:\n%s", size.w, size.h, strings.Join(lines, "\n"))
		}
	}
}

// --- the row stack -----------------------------------------------------

// buildRowForm returns a form over the named stub sections, each with a
// three-line panel unless a test replaces it.
func buildRowForm(palette theme.Palette, ids ...string) (Model, []*stubSection) {
	stubs := make([]*stubSection, 0, len(ids))
	sections := make([]Section, 0, len(ids))
	for _, id := range ids {
		s := newStub(id).withPanel(3)
		stubs = append(stubs, s)
		sections = append(sections, s)
	}
	m := New(Setup{Palette: palette, Sections: sections, Name: "new session"})
	m.Init()
	return m, stubs
}

func viewLines(m Model, w, h int) []string {
	return strings.Split(m.ViewAt(w, h), "\n")
}

func strippedLines(m Model, w, h int) []string {
	lines := viewLines(m, w, h)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Strip(l)
	}
	return out
}

// firstStackRow and footerRow are the two y coordinates the tests below
// need, DERIVED from the frame rather than counted by hand. Both stopped
// being constants when v3 spec §7 gave the frame its two margins: the
// header no longer sits on line 0 and the footer no longer sits on line
// h-1, at any height with room to spare.
//
// Deriving them is not a cosmetic tidy. Two of the callers
// (TestRowStack_RowsRenderIdenticallyAtEveryHeight and
// TestFieldRow_IsIdenticalAtEveryWindowHeight) are the mechanical
// enforcement of Section.Row's "takes no height by design" contract --
// they render the same rows at two very different heights and compare the
// BYTES. Re-pinning them to whatever offsets the new frame happens to
// produce would have been the easy fix and the wrong one; the offsets are
// exactly what differs between the two heights, so they have to come from
// the frame while the row content stays the thing under test.
//
// The order they encode is composeRows': PadTop, Header, Rule1, rows,
// Rule2, Region, Rule3, Footer, PadBottom.
func firstStackRow(f frame) int {
	y := f.PadTop
	if f.Header {
		y++
	}
	if f.Rule1 {
		y++
	}
	return y
}

func footerRow(f frame, h int) int {
	return h - 1 - f.PadBottom
}

// TestRowStack_FrameMatchesLayoutFrame pins composeRows against
// rowlayout.go: the render is exactly h lines, the two rules land where
// layoutFrame says, and no blank chrome row creeps in between the header
// and the stack (v2 spec §9 has six components and none of them is a
// spacer).
func TestRowStack_FrameMatchesLayoutFrame(t *testing.T) {
	m, stubs := buildRowForm(theme.Default(), "a", "b", "c", "d")
	const w, h = 80, 24

	f := layoutFrame(h, len(stubs))
	lines := strippedLines(m, w, h)
	if len(lines) != h {
		t.Fatalf("ViewAt(%d, %d) produced %d lines, want %d", w, h, len(lines), h)
	}

	top := firstStackRow(f)
	if hdr := top - 2; !strings.Contains(lines[hdr], "new session") {
		t.Errorf("line %d is not the header: %q", hdr, lines[hdr])
	}
	if r1 := top - 1; !isRuleLine(lines[r1]) {
		t.Errorf("line %d is not rule 1: %q", r1, lines[r1])
	}
	for i, s := range stubs {
		if !strings.Contains(lines[top+i], s.label) || !strings.Contains(lines[top+i], s.value) {
			t.Errorf("stack line %d = %q, want the %q row", top+i, lines[top+i], s.id)
		}
	}
	if rule2 := top + f.Rows; !isRuleLine(lines[rule2]) {
		t.Errorf("line %d is not rule 2: %q", rule2, lines[rule2])
	}
	// v3 spec §7.4's rule 3 closes the card, directly above the footer.
	if rule3 := footerRow(f, h) - 1; !isRuleLine(lines[rule3]) {
		t.Errorf("line %d is not rule 3: %q", rule3, lines[rule3])
	}
	if ftr := footerRow(f, h); !strings.Contains(lines[ftr], "↵ create") {
		t.Errorf("line %d is not the footer: %q", ftr, lines[ftr])
	}
	// The margins are blank, and there is nothing below the bottom one.
	for i := 0; i < f.PadTop; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			t.Errorf("top margin line %d is not blank: %q", i, lines[i])
		}
	}
	for i := footerRow(f, h) + 1; i < h; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			t.Errorf("bottom margin line %d is not blank: %q", i, lines[i])
		}
	}
}

// isRuleLine reports whether an ANSI-stripped line is one of the form's
// two horizontal rules: nothing but the rule glyph, once padding is
// discounted.
func isRuleLine(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && strings.Trim(s, "─") == ""
}

// TestRowStack_RowsNeverMoveAcrossFocus is the test v1 never had, and
// the defect v2 exists to remove: at a fixed (w, h), everything above
// the panel -- header, rule, every stack row, the second rule -- must be
// byte-identical whatever holds focus. v1 refilled the focused section
// from a shared height budget, so moving focus reflowed the whole form;
// v2 sends every spare row to the panel instead (rowlayout.go's
// layoutFrame).
//
// The comparison strips ANSI and blanks each stack row's two-cell
// gutter, because those are exactly the two things v3 spec §5.4 lets
// focus change: two colors (the full-width ActiveRowBG fill and the
// value's bold) and one glyph (the accent bar). Blanking the gutter
// rather than exempting the row is what keeps the bar honest -- a bar
// that ever rendered wider than gutterWidth would shift the label and
// still fail here.
func TestRowStack_RowsNeverMoveAcrossFocus(t *testing.T) {
	palette := theme.Default()
	m, stubs := buildRowForm(palette, "a", "b", "c", "d")
	const w, h = 80, 24

	f := layoutFrame(h, len(stubs))
	firstRow := firstStackRow(f)   // under the margin, header and rule 1
	above := firstRow + f.Rows + 1 // ... through rule 2

	padLeft, _ := contentBox(w)
	blankGutter := func(lines []string) []string {
		out := append([]string(nil), lines...)
		for i := firstRow; i < firstRow+f.Rows; i++ {
			r := []rune(out[i])
			for c := padLeft; c < padLeft+gutterWidth && c < len(r); c++ {
				r[c] = ' '
			}
			out[i] = string(r)
		}
		return out
	}

	want := blankGutter(strippedLines(m, w, h)[:above])
	for step := 0; step < len(stubs)+1; step++ {
		next, _ := m.Update(keyTab)
		m = next.(Model)
		got := blankGutter(strippedLines(m, w, h)[:above])
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("line %d moved after %d Tab(s) (focus is now %q):\n before: %q\n  after: %q",
					i, step+1, m.FocusedID(), want[i], got[i])
			}
		}
	}
}

// TestRowStack_RowsRenderIdenticallyAtEveryHeight is the mechanical
// enforcement of Section.Row's "takes no height by design" contract.
// A doc comment saying a method must not consult the window height is a
// comment; rendering the same row at two very different heights and
// comparing the bytes is the contract.
func TestRowStack_RowsRenderIdenticallyAtEveryHeight(t *testing.T) {
	m, stubs := buildRowForm(theme.Default(), "a", "b", "c", "d")
	const w = 80

	// At h = 8 the frame affords no header and no rules (layoutFrame),
	// so the stack starts on line 0; at h = 60 it starts under the top
	// margin, the header and its rule. Both offsets are DERIVED -- the
	// margin's size is exactly the sort of thing this test must not be
	// pinned to, since the rows' own bytes are what is under test.
	short := layoutFrame(8, len(stubs))
	tall := layoutFrame(60, len(stubs))
	if short.Header || short.Rule1 || !tall.Header || !tall.Rule1 {
		t.Fatalf("this test needs h=8 to drop the chrome and h=60 to keep it; got %+v and %+v", short, tall)
	}
	if short.Rows != len(stubs) || tall.Rows != len(stubs) {
		t.Fatalf("both heights must show the whole stack; got %d and %d rows", short.Rows, tall.Rows)
	}
	shortTop, tallTop := firstStackRow(short), firstStackRow(tall)
	if shortTop == tallTop {
		t.Fatalf("the two heights must put the stack at DIFFERENT offsets for this to test anything; both start at %d", shortTop)
	}

	atShort := viewLines(m, w, 8)
	atTall := viewLines(m, w, 60)
	for i := range stubs {
		if atShort[shortTop+i] != atTall[tallTop+i] {
			t.Errorf("row %d (%q) rendered differently at h=8 and h=60:\n h=8:  %q\n h=60: %q",
				i, stubs[i].id, atShort[shortTop+i], atTall[tallTop+i])
		}
	}
}

// TestRowStack_FocusedRowAndFooterSurviveEveryHeight pins v2 spec §9's
// two promises at the bottom of the ladder: the footer (and therefore
// Create) is never dropped, and the row stack scrolls to keep the
// FOCUSED row visible rather than clipping it. Focus is parked on the
// last stack row, the first one a naive top-down clip would lose.
//
// "Never dropped" is no longer the same statement as "on the last line":
// v3 spec §7 puts a margin under the footer at any height with room to
// spare, so the footer's y is derived from the frame. What is asserted
// below the footer is that the margin is BLANK -- the promise this test
// really carries is that nothing outranks the footer, not that the frame
// ends there.
func TestRowStack_FocusedRowAndFooterSurviveEveryHeight(t *testing.T) {
	palette := theme.Default()
	m, stubs := buildRowForm(palette, "a", "b", "c", "d", "e", "f", "g", "h")
	focused := stubs[len(stubs)-1]
	if cmd := m.FocusByID(focused.id); cmd != nil {
		cmd()
	}

	const w = 80
	for h := 40; h >= 1; h-- {
		lines := strippedLines(m, w, h)
		if len(lines) != h {
			t.Fatalf("ViewAt(%d, %d) produced %d lines, want %d", w, h, len(lines), h)
		}
		ftr := footerRow(layoutFrame(h, len(stubs)), h)
		if !strings.Contains(lines[ftr], "↵ create") {
			t.Fatalf("at h=%d line %d does not carry the Create button: %q", h, ftr, lines[ftr])
		}
		for i := ftr + 1; i < h; i++ {
			if strings.TrimSpace(lines[i]) != "" {
				t.Fatalf("at h=%d line %d is below the footer and not blank margin: %q", h, i, lines[i])
			}
		}
		if h == 1 {
			continue // the footer is the only line there is
		}
		found := false
		for _, l := range lines {
			if strings.Contains(l, focused.value) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("at h=%d the focused row (%q) is not visible:\n%s", h, focused.id, strings.Join(lines, "\n"))
		}
	}
}

// TestRowStack_SmallPanelDoesNotMoveTheFooter pins the asymmetry
// layoutFrame's step 7 buys: a field with one line to show is handed one
// line and the FORM blank-fills the rest of the region. If the region
// shrank to fit instead, the rule and the footer would jump every time
// focus landed on a quiet field.
func TestRowStack_SmallPanelDoesNotMoveTheFooter(t *testing.T) {
	palette := theme.Default()
	big := newStub("big").withPanel(12)
	small := newStub("small").withPanel(1)
	none := newStub("none") // PanelRows() == 0
	m := New(Setup{Palette: palette, Sections: []Section{big, small, none}, Name: "new session"})
	m.Init()

	const w, h = 80, 24
	f := layoutFrame(h, 3)
	rule2 := firstStackRow(f) + f.Rows
	ftr := footerRow(f, h)

	for _, s := range []*stubSection{big, small, none} {
		if cmd := m.FocusByID(s.id); cmd != nil {
			cmd()
		}
		lines := strippedLines(m, w, h)
		if !isRuleLine(lines[rule2]) {
			t.Fatalf("with %q focused, line %d is not rule 2: %q", s.id, rule2, lines[rule2])
		}
		if !strings.Contains(lines[ftr], "↵ create") {
			t.Fatalf("with %q focused, line %d is not the footer: %q", s.id, ftr, lines[ftr])
		}
		// The region is blank-filled below whatever the field had to
		// show, never collapsed. It ends at rule 3, one line above the
		// footer (v3 spec §7.4).
		for i := rule2 + 1 + s.PanelRows(); i < ftr-1; i++ {
			if strings.TrimSpace(lines[i]) != "" {
				t.Fatalf("with %q focused (%d panel rows), region line %d should be blank fill: %q",
					s.id, s.PanelRows(), i, lines[i])
			}
		}
		if !isRuleLine(lines[ftr-1]) {
			t.Fatalf("with %q focused, line %d is not rule 3 closing the card: %q", s.id, ftr-1, lines[ftr-1])
		}
		if s.PanelRows() > 0 && !strings.Contains(lines[rule2+1], s.id+" panel 0") {
			t.Fatalf("with %q focused, the panel's first line is %q", s.id, lines[rule2+1])
		}
	}
}

// TestRowStack_FocusCarriesThreeSignals pins v3 spec §5.4, and is v2's
// own TestRowStack_FocusIsAFullWidthFill grown rather than replaced: the
// full-width ActiveRowBG fill is still required, it is simply no longer
// the only thing marking the focused row. All three signals -- the fill,
// the accent bar in the gutter, and bold over the value -- are asserted
// present on the focused row and absent from every other one, because the
// defect v3 exists to fix was one signal that turned out to be
// imperceptible, and a suite that checks any one of the three in
// isolation could watch the other two rot.
//
// The fill half is really a test that composeRows routes the focused row
// through paintLine with the right color (paintLine is what makes a
// background survive the styled spans inside a row), and the bold half is
// the same statement about boldSpan.
func TestRowStack_FocusCarriesThreeSignals(t *testing.T) {
	palette := theme.Default()
	m, stubs := buildRowForm(palette, "a", "b", "c")
	const w, h = 80, 24
	padLeft, inner := contentBox(w)
	labelW, _ := labelCol(inner)
	top := firstStackRow(layoutFrame(h, len(stubs)))

	fill := ansi.Style{}.BackgroundColor(palette.ActiveRowBG).String()
	panelBG := ansi.Style{}.BackgroundColor(palette.PanelBG).String()
	if fill == panelBG {
		t.Fatalf("this test needs ActiveRowBG and PanelBG to differ in the default palette")
	}
	bold := ansi.Style{}.Bold().String()
	accent := ansi.Style{}.ForegroundColor(palette.Accent).String()

	for want, s := range stubs {
		if cmd := m.FocusByID(s.id); cmd != nil {
			cmd()
		}
		lines := viewLines(m, w, h)
		for i := range stubs {
			row, focused := lines[top+i], i == want

			if got := strings.Contains(row, fill); got != focused {
				t.Errorf("with %q focused, row %d carries the ActiveRowBG fill = %v, want %v", s.id, i, got, focused)
			}
			// The bar sits in the row's own gutter, which starts where
			// the content box does -- derived rather than assumed, so
			// this still holds when the box's left padding moves.
			gutter := []rune(ansi.Strip(row))[padLeft : padLeft+gutterWidth]
			if got := string(gutter) == focusBarGlyph+" "; got != focused {
				t.Errorf("with %q focused, row %d's gutter is %q, want the accent bar = %v", s.id, i, string(gutter), focused)
			}
			if got := strings.Contains(row, accent); got != focused {
				t.Errorf("with %q focused, row %d carries the accent foreground = %v, want %v", s.id, i, got, focused)
			}
			// Bold, and bold on the VALUE rather than the whole row:
			// the label column is scenery and stays dim.
			if got := strings.Contains(row, bold); got != focused {
				t.Errorf("with %q focused, row %d carries bold = %v, want %v", s.id, i, got, focused)
			}
			if valueAt := padLeft + gutterWidth + labelW; focused {
				at := ansi.StringWidth(ansi.Strip(row[:strings.Index(row, bold)]))
				if at != valueAt {
					t.Errorf("with %q focused, bold starts at column %d, want the value column at %d", s.id, at, valueAt)
				}
			}
		}
		// Full width, not just the content box.
		if got := ansi.StringWidth(lines[top+want]); got != w {
			t.Errorf("the focused row is %d cells wide, want the full %d", got, w)
		}
	}
}

// TestRowStack_HeaderCarriesNameAndContext pins v2 spec §4's header --
// and, with it, that SetContext takes a POINTER receiver. Every other
// Model mutator gets away with a value receiver because it writes
// through *focusRing; a plain string field written through a value
// receiver compiles just as happily and is silently dropped, so the only
// way to catch it is to set the context and then look for it on screen.
func TestRowStack_HeaderCarriesNameAndContext(t *testing.T) {
	m, _ := buildRowForm(theme.Default(), "a", "b")
	m.SetContext("herdr-draft · main")

	// Two rows above it (v3 spec §7): the top margin, then the header
	// itself -- so the header's y is firstStackRow minus rule 1 and
	// itself, not 0.
	header := strippedLines(m, 80, 24)[firstStackRow(layoutFrame(24, 2))-2]
	if !strings.Contains(header, "new session") {
		t.Errorf("header %q does not carry Setup.Name", header)
	}
	if !strings.Contains(header, "herdr-draft · main") {
		t.Errorf("header %q does not carry SetContext's text (a value receiver would drop it)", header)
	}
	if trimmed := strings.TrimRight(header, " "); !strings.HasSuffix(trimmed, "herdr-draft · main") {
		t.Errorf("header %q does not right-align the context", header)
	}
	if idx := strings.Index(header, "new session"); idx > strings.Index(header, "herdr-draft") {
		t.Errorf("header %q puts the name to the right of the context", header)
	}
}

// TestModel_InitialFocusID pins v2 spec §8's "focus opens on title, not
// on the first enabled section", plus both fallbacks.
func TestModel_InitialFocusID(t *testing.T) {
	palette := theme.Default()
	build := func(initial string) Model {
		m := New(Setup{Palette: palette, Sections: []Section{
			newStub("a"), newStub("b"), newStub("c"),
		}, InitialFocusID: initial})
		m.Init()
		return m
	}

	if got := build("c").FocusedID(); got != "c" {
		t.Errorf("InitialFocusID \"c\": FocusedID() = %q, want %q", got, "c")
	}
	if got := build("").FocusedID(); got != "a" {
		t.Errorf("InitialFocusID \"\": FocusedID() = %q, want the first enabled section %q", got, "a")
	}
	if got := build("no-such-id").FocusedID(); got != "a" {
		t.Errorf("InitialFocusID \"no-such-id\": FocusedID() = %q, want the first enabled section %q", got, "a")
	}

	// Unlike Tab navigation, and like FocusByID, a named section is
	// reached even when it is disabled.
	disabled := newStub("b")
	disabled.enabled = false
	m := New(Setup{Palette: palette, Sections: []Section{newStub("a"), disabled}, InitialFocusID: "b"})
	m.Init()
	if got := m.FocusedID(); got != "b" {
		t.Errorf("InitialFocusID on a disabled section: FocusedID() = %q, want %q", got, "b")
	}
}

// TestRowStack_FooterIsContextual pins v2 spec §3 rule 4's footer: the
// focused field's own rungs first, then the constant tail. A section
// implementing footerHinter speaks for itself; anything else falls back
// to footer.go's per-zone table, keyed off the same zoneFor the key
// grammar uses.
func TestRowStack_FooterIsContextual(t *testing.T) {
	palette := theme.Default()
	hinting := &hintingStub{rungs: []string{"⇥ do the thing"}}
	hinting.stubSection = *newStub("prompt")
	plain := newStub("placement").withPanel(2)

	m := New(Setup{Palette: palette, Sections: []Section{hinting, plain}, Name: "new session"})
	m.Init()

	const w, h = 120, 24
	ftr := footerRow(layoutFrame(h, 2), h)
	footer := func() string { return strippedLines(m, w, h)[ftr] }

	if cmd := m.FocusByID("prompt"); cmd != nil {
		cmd()
	}
	if got := footer(); !strings.Contains(got, "⇥ do the thing") {
		t.Errorf("footer with a footerHinter focused = %q, want it to carry the section's own rung", got)
	}
	if got := footer(); !strings.Contains(got, "⌃R clear") {
		t.Errorf("footer = %q, want the constant tail appended", got)
	}
	if got := footer(); !strings.Contains(got, "esc cancel") {
		t.Errorf("footer = %q, want v2 spec §4's cancel button beside Create", got)
	}

	if cmd := m.FocusByID("placement"); cmd != nil {
		cmd()
	}
	if got := footer(); !strings.Contains(got, "←→ choose") {
		t.Errorf("footer with the placement zone focused = %q, want the zone table's rung", got)
	}

	// The Create button is never traded away for hint text, whatever
	// the width.
	for _, width := range []int{120, 80, 64, 40, 24, 12} {
		line := ansi.Strip(strings.Split(m.ViewAt(width, h), "\n")[ftr])
		if !strings.Contains(line, "↵ create") {
			t.Errorf("at w=%d the footer lost the Create button: %q", width, line)
		}
	}
}

// TestRenderFooter_ExactFitKeepsCancel pins the boundary v3 spec §7.5
// names: renderFooter's cancel test is `<=`, so a pair of buttons that
// fits the line EXACTLY is drawn, and only one column less drops cancel.
// v2 tested `<` and lost the button on the width where it fitted, which
// contradicted its own documented priority (key ladder first, then
// cancel, create last).
//
// It is a direct call rather than a width in one of the loops above
// because the branch is not reachable through a real frame at any width
// a user can produce: v3 spec §6.2's box is edge-to-edge, so boxWidth is
// w-2, and the exact fit lands on a 26-column terminal. Unreachable
// through the front door is exactly the condition under which a branch
// quietly stops being covered.
func TestRenderFooter_ExactFitKeepsCancel(t *testing.T) {
	palette := theme.Default()
	exact := lipgloss.Width(createButton(palette)) + footerButtonGap + lipgloss.Width(cancelButton(palette))

	for _, c := range []struct {
		width      int
		wantCancel bool
	}{
		{exact + 1, true},
		{exact, true},
		{exact - 1, false},
	} {
		line := ansi.Strip(widgets.Zones.Scan(renderFooter(c.width, nil, palette)))
		if got := strings.Contains(line, "esc cancel"); got != c.wantCancel {
			t.Errorf("renderFooter(%d) carries cancel = %v, want %v: %q", c.width, got, c.wantCancel, line)
		}
		if !strings.Contains(line, "↵ create") {
			t.Errorf("renderFooter(%d) lost the Create button: %q", c.width, line)
		}
	}
}

// TestFooterRungs_PerZone pins footer.go's own per-zone table against the
// zones the key grammar actually defines (v2 spec §3 rule 4, and the plan's
// rung table): every zone a real field maps onto teaches something, and
// the ONE zone whose hint depends on state rather than kind -- Title --
// says what Enter will really do.
//
// The Title pair is the reason this test exists at all: v1's footer said
// "↵ advance" from every zone, including a filled Title where Enter
// submits the form. FocusZone.TitleEmpty was already computed for the
// grammar; the footer simply never read it.
func TestFooterRungs_PerZone(t *testing.T) {
	want := map[ZoneKind]string{
		ZoneIssue:     "type to filter",
		ZoneDir:       "⇥ complete",
		ZonePrompt:    "⌃J newline",
		ZoneWorktree:  "↑↓ part",
		ZonePlacement: "←→ choose",
		ZoneAgent:     "←→ favorites",
		ZoneAccount:   "↑↓ pick",
		ZoneCreate:    "⇧⇥ back",
	}
	for kind, substr := range want {
		rungs := footerRungs(FocusZone{Kind: kind}, false)
		if len(rungs) == 0 {
			t.Errorf("zone %v has no footer rungs at all", kind)
			continue
		}
		if !strings.Contains(rungs[0], substr) {
			t.Errorf("zone %v's widest rung = %q, want it to teach %q", kind, rungs[0], substr)
		}
		if !strings.Contains(rungs[0], "⌃R clear") {
			t.Errorf("zone %v's widest rung = %q, want the constant tail appended", kind, rungs[0])
		}
		// The footer's own buttons say ↵ and esc (form.go's
		// renderFooter). No rung may say them again: at 64 columns the
		// pre-polish ladder plus the button spelled "create" three times
		// on one line.
		for _, dup := range []string{"↵", "esc cancel", "Esc"} {
			if strings.Contains(rungs[0], dup) {
				t.Errorf("zone %v's widest rung = %q, want it NOT to repeat the button's %q", kind, rungs[0], dup)
			}
		}
	}

	// The Title zone is the one exception, and only while EMPTY: Enter
	// does not create there, so the rung has to correct the button.
	filled := footerRungs(FocusZone{Kind: ZoneTitle, TitleEmpty: false}, false)[0]
	if !strings.Contains(filled, "⌃S create now") {
		t.Errorf("a non-empty title's rung = %q, want v2 spec §4's own mockup text", filled)
	}
	if strings.Contains(filled, "↵") {
		t.Errorf("a non-empty title's rung = %q, want ↵ left to the button that carries it", filled)
	}
	empty := footerRungs(FocusZone{Kind: ZoneTitle, TitleEmpty: true}, false)[0]
	if strings.Contains(empty, "create now") || !strings.Contains(empty, "name it to create") {
		t.Errorf("an empty title's rung = %q, want it to say there is nothing to create yet", empty)
	}

	// ⌃R's two states reach every zone through the same tail.
	if armed := footerRungs(FocusZone{Kind: ZoneAgent}, true)[0]; !strings.Contains(armed, "⌃R again") {
		t.Errorf("armed rung = %q, want the ⌃R⌃R gesture's second state", armed)
	}
}

// TestFooterRungs_AZoneHintNeverLosesToTheConstantTail pins the ordering
// property crossRungs and fitFooter exist for: whatever the width, the
// footer keeps teaching the FOCUSED FIELD and gives up the constants
// instead (v2 spec §3 rule 4). Both halves of that have been wrong at
// some point -- a bare constant tail out-measuring a crossing, and then
// a narrower crossing out-measuring a wider lead standing alone -- which
// is why the assertion runs down a ladder of widths rather than at one.
func TestFooterRungs_AZoneHintNeverLosesToTheConstantTail(t *testing.T) {
	rungs := footerRungs(FocusZone{Kind: ZoneTitle, TitleEmpty: false}, false)
	for _, width := range []int{120, 80, 64, 53, 40, 30} {
		got := fitFooter(rungs, width)
		if !strings.Contains(got, "for the prompt") {
			t.Errorf("at width %d the footer = %q, want it to still teach the focused field", width, got)
		}
	}

	// The tail is what goes first, and it goes before the lead is
	// abbreviated: 37 cells is the space a 64-column popup leaves beside
	// the two buttons, and it holds v2 spec §4's own title rung (32) but
	// not that rung plus any tail.
	if got := fitFooter(rungs, 37); got != "⌃S create now · ⇥ for the prompt" {
		t.Errorf("at the 64-column popup's 37 cells the footer = %q, want the whole zone rung and no tail", got)
	}
}

// TestRowStack_ClickOnAFieldRowFocusesIt pins the renamed per-row zone
// (field:<id>, see form.go's zone-scheme doc comment): a click anywhere
// on a stack row focuses that field AND is forwarded to it, exactly as
// v1's section:<id> click did.
func TestRowStack_ClickOnAFieldRowFocusesIt(t *testing.T) {
	m, stubs := buildRowForm(theme.Default(), "click-a", "click-b")
	target := stubs[1]

	_ = m.ViewAt(80, 24)
	syncZones()

	zi := widgets.Zones.Get(zoneFieldPrefix + target.id)
	if zi.IsZero() {
		t.Fatalf("zone %q never resolved after ViewAt(80, 24)'s own Scan", zoneFieldPrefix+target.id)
	}

	before := target.updateCalls
	next, _ := m.Update(clickAt(zi.StartX, zi.StartY))
	m = next.(Model)

	if got := m.FocusedID(); got != target.id {
		t.Errorf("FocusedID() after clicking %q's row = %q, want %q", target.id, got, target.id)
	}
	if target.updateCalls == before {
		t.Errorf("the raw click was not forwarded to the clicked section")
	}
}

// TestRowStack_ClickInThePanelDoesNotMoveFocus pins the one genuinely
// new click branch: the panel belongs to the focused field by
// construction, so a click inside it is forwarded to that field WITHOUT
// re-running the focus fan-out. Re-focusing mid-click is what would stop
// the chip and picker-row zones nested inside a panel from resolving.
func TestRowStack_ClickInThePanelDoesNotMoveFocus(t *testing.T) {
	m, stubs := buildRowForm(theme.Default(), "panel-a", "panel-b")
	focused := stubs[0]

	_ = m.ViewAt(80, 24)
	syncZones()

	zi := widgets.Zones.Get(zonePanel)
	if zi.IsZero() {
		t.Fatalf("zone %q never resolved after ViewAt(80, 24)'s own Scan", zonePanel)
	}

	before := focused.updateCalls
	blurs := focused.blurCalls
	next, _ := m.Update(clickAt(zi.StartX, zi.StartY))
	m = next.(Model)

	if got := m.FocusedID(); got != focused.id {
		t.Errorf("FocusedID() after a click in the panel = %q, want it unchanged at %q", got, focused.id)
	}
	if focused.blurCalls != blurs {
		t.Errorf("the focused section was Blur()ed by a click in its own panel (%d -> %d)", blurs, focused.blurCalls)
	}
	if focused.updateCalls == before {
		t.Errorf("the raw click was not forwarded to the focused section")
	}
}
