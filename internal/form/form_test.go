package form

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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

// stubSection is a minimal, fully configurable Section test double --
// "Stub sections for tests are fine and expected; real fields land in
// Tasks 17-18" (task-16 brief). It also spies on Focus/Blur/Update call
// counts so focus-ring and grammar-wiring tests can assert on behavior,
// not just rendered output.
type stubSection struct {
	id      string
	enabled bool
	height  func(winH int) int
	content func(inner int) string

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
		height:  func(int) int { return 1 },
		content: func(int) string { return id },
	}
}

func (s *stubSection) ID() string            { return s.id }
func (s *stubSection) Enabled() bool         { return s.enabled }
func (s *stubSection) Height(w int) int      { return s.height(w) }
func (s *stubSection) View(inner int) string { return s.content(inner) }

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

// TestModel_ConstantHeightAcrossFocusMoves pins spec §6's core promise --
// "constant-height sections for a given window size" -- directly: at a
// FIXED (w, h), moving focus between sections must not change the
// composed content's own line count. Nothing in this package's design
// varies a Section's reported Height() (or the number of physical lines
// its View emits) by whether it currently has focus (see decorateFocus's
// own doc comment), so this holds by construction; this test is the
// cheap, direct check of that promise, not merely inferred from the
// design.
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

// --- golden frames ---------------------------------------------------------

// pickerSection and chipRowSection wrap this package's own real widgets
// (internal/form/widgets, Task 14) rather than fabricated placeholder
// content, so the golden frames below exercise -- and the paintLine
// background-painting fix in sizes.go is stress-tested against -- actual
// multi-span styled output (Picker's cursor-row accent highlighting and
// its "✓" Marker convention, ChipRow's cursor highlighting), not just
// plain unstyled stub text.
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
func (s *pickerSection) View(inner int) string {
	return s.picker.View(inner, s.rows)
}

// chipRowSection always reserves the hint line regardless of whether the
// currently selected chip carries one -- the "field wrappers must always
// reserve the hint line" rule this task's own brief calls out
// (widgets.ChipRow.View's line count is otherwise hint-dependent; see
// chiprow.go's own doc comment), so Height stays a true constant.
type chipRowSection struct {
	id      string
	row     *widgets.ChipRow
	enabled bool
	width   int
}

func (s *chipRowSection) ID() string             { return s.id }
func (s *chipRowSection) Enabled() bool          { return s.enabled }
func (s *chipRowSection) Focus() tea.Cmd         { return nil }
func (s *chipRowSection) Blur()                  {}
func (s *chipRowSection) Update(tea.Msg) tea.Cmd { return nil }
func (s *chipRowSection) Height(int) int         { return 2 }
func (s *chipRowSection) View(inner int) string {
	v := s.row.View(inner)
	if !strings.Contains(v, "\n") {
		pad := inner
		if pad < 0 {
			pad = 0
		}
		v += "\n" + strings.Repeat(" ", pad)
	}
	return v
}

// buildEmptyForm returns a form with two representative-but-still-stub
// Sections (an "issue"-shaped Picker, an "agent"-shaped ChipRow) in their
// default state, for the golden frames task-16 brief step 3 requires:
// empty-80x24 and empty-120x40.
func buildEmptyForm(palette theme.Palette) Model {
	issue := widgets.NewPicker(palette)
	issue.SetItems(1, []widgets.PickerItem{
		{ID: "eng-100", Label: "Fix login bug", Hint: "In Progress"},
		{ID: "eng-101", Label: "Add dark mode", Hint: "Todo", Marker: "✓"},
		{ID: "eng-102", Label: "Refactor auth", Hint: "Todo"},
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
	})
	m.Init()
	return m
}

func TestFrames_Empty(t *testing.T) {
	palette := theme.Default()
	assertFrame(t, "empty-80x24", buildEmptyForm(palette), 80, 24)
	assertFrame(t, "empty-120x40", buildEmptyForm(palette), 120, 40)
}

// buildManySections returns a form with n generic stub Sections, each
// always reporting height lines regardless of the window height handed
// to it (deliberately NOT self-degrading), specifically so their combined
// height overflows a short window and forces sizes.go's fitToHeight
// degradation ladder to actually engage -- see
// TestDegradation_CreateNeverClippedAt80x20's own comment for the height
// arithmetic this is tuned against.
func buildManySections(palette theme.Palette, n, height int) Model {
	sections := make([]Section, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("field-%d", i)
		sections = append(sections, &stubSection{
			id:      id,
			enabled: true,
			height:  func(int) int { return height },
			content: func(inner int) string {
				lines := make([]string, height)
				for j := range lines {
					lines[j] = fmt.Sprintf("%s content line %d", id, j)
				}
				return strings.Join(lines, "\n")
			},
		})
	}
	m := New(Setup{Palette: palette, Sections: sections})
	m.Init()
	return m
}

// TestDegradation_CreateNeverClippedAt80x20 is task-16 brief step 3's
// other literal requirement: "degradation at 80x20 keeps the Create
// button visible (stub sections + shrink)."
//
// Six 4-line stub sections (chosen so the ladder is actually forced to
// engage, not merely never triggered): body alone is
// 6*(4 content + 1 divider) = 30 lines, plus 2 vertical-padding lines,
// the footer line, and the Create button's own line = 34 -- well over
// the 20-row budget, so fitToHeight's drop-blanks/drop-dividers stages
// (sizes.go) cannot possibly bring it under 20 on their own (dropping
// every one of the 6 dividers and both padding lines only recovers 8
// lines, 34-8=26 > 20), which means the final clipTail stage MUST engage
// for this test to pass at all -- so a passing result is real evidence
// the degradation ladder's last-resort stage, not just its earlier
// stages, correctly preserves the Create button.
func TestDegradation_CreateNeverClippedAt80x20(t *testing.T) {
	palette := theme.Default()
	m := buildManySections(palette, 6, 4)

	assertFrame(t, "degraded-80x20", m, 80, 20)

	got := m.ViewAt(80, 20)
	lines := strings.Split(got, "\n")
	if len(lines) != 20 {
		t.Fatalf("ViewAt(80, 20) produced %d rows, want exactly 20", len(lines))
	}

	last := ansi.Strip(lines[len(lines)-1])
	if !strings.Contains(last, "Create") {
		t.Fatalf("last row does not contain the Create button text: %q", last)
	}
}
