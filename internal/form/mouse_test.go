// mouse_test.go is task 21's own TDD suite: bubblezone/v2 zone
// registration (form.go's compose/renderFooter, field_*.go's
// MarkedView call sites) and this package's own click/wheel handling
// (form.go's handleMouseClick/handleMouseWheel, field_*.go's Update
// additions). New code, not a port of anything in atrium
// (github.com/ZviBaratz/atrium) -- Atrium has no mouse support at all.
package form

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// syncZones gives bubblezone/v2's own background worker goroutine (a
// single, package-shared widgets.Zones instance -- see zones.go's own
// doc comment) time to finish applying the most recent ViewAt/Scan
// before a test trusts widgets.Zones.Get: Scan buffers zone info onto a
// channel a separate goroutine drains, so an immediate Get right after
// Scan is a real, documented race (bubblezone/v2's own doc comment on
// Scan: "an immediate call to Get(id) may not return the correct
// information"). This is a fixed sleep, not a poll-until-non-zero loop,
// deliberately: widgets.Zones is ONE shared singleton across every test
// in this package, so a STALE zone left over from an earlier test (this
// same ID, a DIFFERENT position, from a form rendered at different
// dimensions) would satisfy a naive "non-zero yet" poll prematurely,
// handing the test the WRONG bounds. A fixed sleep long enough for the
// worker to drain (matching the vendored v2.0.0 source's own test
// suite's convention -- manager_test.go sleeps 15-100ms after every
// Scan) has no such failure mode: it does not matter what the poll
// SEES, only that enough real time has passed.
func syncZones() {
	time.Sleep(50 * time.Millisecond)
}

// clickAt synthesizes a left-button tea.MouseClickMsg at (x, y) -- every
// zone this task registers only ever reacts to msg.Button ==
// tea.MouseLeft (form.go's handleMouseClick doc comment).
func clickAt(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

// TestMouseZones_ButtonCreate is the brief's own "zone-ID map" test:
// render a form at 120x40, confirm the Create section's own
// "button:create" zone (form.go's renderFooter and its zoneCreateButton
// constant) resolved to real on-screen bounds, and that a synthesized
// left-click at those bounds' own top-left corner produces SubmitMsg --
// the same message Enter from Create already produces (keys.go's
// ActionSubmit), so a mouse click on Create must be behaviorally
// equivalent to that keyboard path.
func TestMouseZones_ButtonCreate(t *testing.T) {
	m := New(Setup{Palette: theme.Default(), Sections: []Section{newStub("a")}})
	m.Init()
	_ = m.ViewAt(120, 40)
	syncZones()

	zi := widgets.Zones.Get(zoneCreateButton)
	if zi.IsZero() {
		t.Fatalf("zone %q never resolved after ViewAt(120, 40)'s own Scan", zoneCreateButton)
	}

	next, cmd := m.Update(clickAt(zi.StartX, zi.StartY))
	m = next.(Model)
	if cmd == nil {
		t.Fatalf("click on button:create's own top-left corner (%d,%d) produced a nil cmd", zi.StartX, zi.StartY)
	}
	if _, ok := cmd().(SubmitMsg); !ok {
		t.Fatalf("click on button:create cmd's message = %#v, want SubmitMsg{}", cmd())
	}
}

// TestMouseZones_ButtonCancel is the same contract for the SECOND footer
// button v2 spec §4 puts beside Create: a click on "button:cancel"
// produces CancelMsg, exactly like Esc (keys.go's ActionCancel). It owns
// no Section and takes no focus stop -- it is esc's clickable
// affordance, and the reason footer.go's key ladder no longer spends
// cells repeating "Esc cancel" on every zone.
func TestMouseZones_ButtonCancel(t *testing.T) {
	m := New(Setup{Palette: theme.Default(), Sections: []Section{newStub("a")}})
	m.Init()
	_ = m.ViewAt(120, 40)
	syncZones()

	zi := widgets.Zones.Get(zoneCancelButton)
	if zi.IsZero() {
		t.Fatalf("zone %q never resolved after ViewAt(120, 40)'s own Scan", zoneCancelButton)
	}

	next, cmd := m.Update(clickAt(zi.StartX, zi.StartY))
	m = next.(Model)
	if cmd == nil {
		t.Fatalf("click on button:cancel's own top-left corner (%d,%d) produced a nil cmd", zi.StartX, zi.StartY)
	}
	if _, ok := cmd().(CancelMsg); !ok {
		t.Fatalf("click on button:cancel cmd's message = %#v, want CancelMsg{}", cmd())
	}
}

// TestMouseZones_ClickOnAScrolledPanelListPicksTheClickedRow is the
// regression for the click/row mismatch v2's variable-height panel
// introduced: widgets.Picker.SelectAt maps a physical row back to an
// item by recomputing the SAME scrollOffset the render used, which only
// works if it is handed the SAME height MarkedView got. DirField,
// IssueField and AccountField each kept passing v1's fixed row count
// (4/6/4), so on any list the panel had scrolled -- which is every list
// longer than the panel, at every window size but one -- a click landed
// on a different candidate than the one under the pointer.
//
// The list here is deliberately longer than the panel and the cursor is
// driven to its END, so the render is scrolled and a height mismatch
// cannot coincidentally agree.
func TestMouseZones_ClickOnAScrolledPanelListPicksTheClickedRow(t *testing.T) {
	palette := theme.Default()

	d := NewDirField(palette)
	d.SetHomeDir("/home/zvi")
	dirs := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		dirs = append(dirs, "/home/zvi/Projects/p"+string(rune('a'+i)))
	}
	d.SetCandidates(1, dirs)

	m := New(Setup{Palette: palette, Sections: []Section{d}, Name: "new session"})
	m.Init()
	if cmd := m.FocusByID("dir"); cmd != nil {
		cmd()
	}
	// Drive the cursor past the bottom of any window this panel gets, so
	// the render really is scrolled.
	for i := 0; i < len(dirs); i++ {
		next, _ := m.Update(key(tea.KeyDown, 0))
		m = next.(Model)
	}
	_ = m.ViewAt(80, 24)
	syncZones()

	// The cursor row is the LAST rendered row of a list scrolled to its
	// end; clicking it must be a no-op on the selection, and clicking the
	// row above it must select exactly the candidate rendered there.
	const zoneID = "row:dir:0"
	zi := widgets.Zones.Get(zoneID)
	if zi.IsZero() {
		t.Fatalf("zone %q never resolved after ViewAt(80, 24)'s own Scan", zoneID)
	}

	// What is actually drawn on that physical row, per the picker's own
	// windowing -- the answer the click must agree with.
	rows := d.pickerRowsShown
	if rows < 2 {
		t.Fatalf("dir panel drew %d list rows at 80x24, want a real window", rows)
	}
	want := ""
	if !d.picker.SelectVisibleRow(0, rows) {
		t.Fatalf("SelectVisibleRow(0, %d) found no item", rows)
	}
	if sel, ok := d.picker.Selected(); ok {
		want = sel.ID
	}
	// Put the cursor back where the render left it before clicking.
	for i := 0; i < len(dirs); i++ {
		d.picker.CursorNext()
	}
	_ = m.ViewAt(80, 24)
	syncZones()

	next, _ := m.Update(clickAt(zi.StartX, zi.StartY))
	m = next.(Model)

	if got := d.Value(); got != want {
		t.Fatalf("Value() after clicking %s of a scrolled list = %q, want the row actually drawn there, %q", zoneID, got, want)
	}
}

// TestMouseZones_ChipClickSelectsPlacement is the brief's own second
// scenario: a click on chip:placement:tab-here selects it.
// PlacementField starts on "New space" (index 0, plan.PlacementNewSpace,
// the zero value); clicking the "Tab here" chip's own
// "chip:placement:tab-here" zone (field_placement.go's own
// PlacementField.View/Update) must move Value() to plan.PlacementTabHere
// -- exactly as if the user had pressed Right once -- and, per task 21's
// click grammar (form.go's handleMouseClick), also focus the placement
// section itself, the same way clicking any other section's own area
// does.
func TestMouseZones_ChipClickSelectsPlacement(t *testing.T) {
	f := NewPlacementField(theme.Default())
	if got := f.Value(); got != plan.PlacementNewSpace {
		t.Fatalf("fresh PlacementField.Value() = %v, want PlacementNewSpace", got)
	}

	m := New(Setup{Palette: theme.Default(), Sections: []Section{f}})
	m.Init()
	_ = m.ViewAt(80, 24)
	syncZones()

	const zoneID = "chip:placement:tab-here"
	zi := widgets.Zones.Get(zoneID)
	if zi.IsZero() {
		t.Fatalf("zone %q never resolved after ViewAt(80, 24)'s own Scan", zoneID)
	}

	next, _ := m.Update(clickAt(zi.StartX, zi.StartY))
	m = next.(Model)

	if got := f.Value(); got != plan.PlacementTabHere {
		t.Fatalf("PlacementField.Value() after clicking %s = %v, want PlacementTabHere", zoneID, got)
	}
	if got := m.FocusedID(); got != "placement" {
		t.Fatalf("FocusedID() after clicking a placement chip = %q, want %q (a chip click also focuses its own section, per handleMouseClick)", got, "placement")
	}
}

// TestMouseZones_WheelMovesBasePickerCursor is the brief's own third
// scenario: a wheel over the base picker moves its cursor. Task 21's own
// wheel grammar (form.go's handleMouseWheel doc comment: "scroll the
// focused picker or the prompt") scrolls whichever section CURRENTLY has
// focus, not whatever the mouse happens to sit over, so this focuses the
// worktree field first (FocusByID, the same entry point spec §9's own
// submit-time re-focus rule uses) and walks the part cursor down onto the
// base list, then asserts WorktreeField.Base() moved off "HEAD" (row 0,
// Base()'s own "" sentinel) onto the first real ref -- exactly as Down
// already does.
//
// Walking the part cursor is the point rather than an inconvenience: the
// wheel means "scroll the list" only where a list is what the user is
// looking at, and v2's worktree panel has two other parts that are not
// scrollable at all.
func TestMouseZones_WheelMovesBasePickerCursor(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetGitTarget(true)
	w.SetOn(true)
	w.SetBaseItems(1, []string{"main", "develop"})

	if got := w.Base(); got != "" {
		t.Fatalf("fresh WorktreeField.Base() = %q, want \"\" (HEAD)", got)
	}

	m := New(Setup{Palette: theme.Default(), Sections: []Section{w}})
	m.Init()

	if cmd := m.FocusByID("worktree"); cmd != nil {
		cmd()
	}
	if got := m.FocusedID(); got != "worktree" {
		t.Fatalf("FocusedID() after FocusByID(\"worktree\") = %q, want %q", got, "worktree")
	}
	for i := 0; i < 2; i++ { // chips -> branch -> base
		next, _ := m.Update(key(tea.KeyDown, 0))
		m = next.(Model)
	}

	next, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	m = next.(Model)

	if got := w.Base(); got != "main" {
		t.Fatalf("WorktreeField.Base() after a wheel-down over the focused base picker = %q, want %q", got, "main")
	}
}

// TestMouseZones_ClickOnABaseRowSelectsItAndMovesThePart pins the click
// half of v2's worktree sub-focus: a click on one of the panel's own
// "row:base:<n>" zones both selects that ref AND parks the part cursor on
// the base list, so the keyboard picks up where the mouse left off rather
// than the next ↑ meaning something else.
func TestMouseZones_ClickOnABaseRowSelectsItAndMovesThePart(t *testing.T) {
	w := NewWorktreeField(theme.Default())
	w.SetGitTarget(true)
	w.SetOn(true)
	w.SetHeadBranch("main")
	w.SetBaseItems(1, []string{"develop", "release/1.4"})

	m := New(Setup{Palette: theme.Default(), Sections: []Section{w}, Name: "new session"})
	m.Init()
	if cmd := m.FocusByID("worktree"); cmd != nil {
		cmd()
	}
	_ = m.ViewAt(80, 24)
	syncZones()

	const zoneID = "row:base:1" // row 0 is the HEAD sentinel
	zi := widgets.Zones.Get(zoneID)
	if zi.IsZero() {
		t.Fatalf("zone %q never resolved after ViewAt(80, 24)'s own Scan", zoneID)
	}

	next, _ := m.Update(clickAt(zi.StartX, zi.StartY))
	m = next.(Model)

	if got := w.Base(); got != "develop" {
		t.Fatalf("Base() after clicking %s = %q, want %q", zoneID, got, "develop")
	}
	if w.part != partBase {
		t.Fatalf("part after clicking a base row = %v, want partBase", w.part)
	}
}
