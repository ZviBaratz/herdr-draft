package form

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// key builds a synthetic tea.KeyPressMsg whose String() matches keystroke,
// using the same Code/Mod construction bubbletea's own decoder produces for
// these chords (verified against charm.land/bubbletea/v2@v2.0.8/key.go and
// ultraviolet's decoder.go control-character table -- e.g. Ctrl+<letter>
// decodes to Code: '<letter>', Mod: ModCtrl, not a distinct control-code
// rune), so these fixtures exercise MapKey exactly as a real keypress would.
func key(code rune, mod tea.KeyMod) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: mod}
}

// rn (rune) builds the synthetic keypress for a typed printable character:
// Text is what carries the character through Key.String(), matching how a
// real printable keypress arrives (Code == the rune, Text == the same rune
// as a string).
func rn(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

var (
	keyTab        = key(tea.KeyTab, 0)
	keyShiftTab   = key(tea.KeyTab, tea.ModShift)
	keyEnter      = key(tea.KeyEnter, 0)
	keyAltEnter   = key(tea.KeyEnter, tea.ModAlt)
	keyShiftEnter = key(tea.KeyEnter, tea.ModShift)
	keyEsc        = key(tea.KeyEscape, 0)
	keyCtrlC      = key('c', tea.ModCtrl)
	keyCtrlS      = key('s', tea.ModCtrl)
	keyCtrlJ      = key('j', tea.ModCtrl)
	keyCtrlR      = key('r', tea.ModCtrl)
	keyX          = rn('x')
)

// allZoneKinds lists every ZoneKind keys.go defines, in declaration order.
// It is this file's single canonical zone list -- TestMapKey_CtrlRArmsRegardlessOfZone
// below iterates it directly, and pickerZoneKinds/nonPickerZoneKinds
// partition it for the Tab-completion tests. Adding a new ZoneKind to
// keys.go without adding it here is caught by nothing automatically (Go
// has no way to enumerate a package's own const block at compile time),
// so this list -- not isPicker() itself, and not a reflection trick -- is
// the thing a reviewer/future editor is expected to update by hand
// alongside keys.go's own const block.
var allZoneKinds = []ZoneKind{
	ZoneIssue, ZoneDir, ZoneTitle, ZoneWorktree, ZoneBranch, ZoneBase,
	ZonePlacement, ZoneAgent, ZoneAccount, ZonePrompt, ZoneCreate,
}

// pickerZoneKinds are exactly the zones this test suite expects
// ZoneKind.isPicker() to report true for -- Tab tries to complete before
// advancing there (ActionComplete), per spec §6's field-order prose
// calling exactly these three fields "picker"s (see isPicker's doc in
// keys.go). This list is independent of isPicker()'s own switch -- it is
// not read off isPicker() -- so TestMapKey_TabCompletesInEveryPickerZone
// below is a real regression check against keys.go's classification, not
// a tautology: a future edit that drops (or adds) a zone in isPicker()
// without updating this list fails that test.
var pickerZoneKinds = []ZoneKind{ZoneIssue, ZoneDir, ZoneBase}

// nonPickerZoneKinds is every other zone spec §6 defines, listed
// explicitly opposite pickerZoneKinds rather than computed by subtracting
// it from allZoneKinds -- so the two sit next to each other in the source
// as visibly one partition, and TestZoneKindPartition_IsExhaustiveAndDisjoint
// below checks that partition actually holds (covers allZoneKinds exactly
// once between the two lists). That check is what makes "add a zone to
// isPicker() without updating the tests" loud: a zone missing from both
// lists fails the partition test directly, and a zone left in the wrong
// list makes either TestMapKey_TabCompletesInEveryPickerZone or
// TestMapKey_TabPlainAdvanceOutsidePickerZones fail against the real
// MapKey behavior.
var nonPickerZoneKinds = []ZoneKind{
	ZoneTitle, ZoneWorktree, ZoneBranch, ZonePlacement, ZoneAgent,
	ZoneAccount, ZonePrompt, ZoneCreate,
}

// TestZoneKindPartition_IsExhaustiveAndDisjoint guards the opposition
// between pickerZoneKinds and nonPickerZoneKinds: together they must name
// every zone in allZoneKinds exactly once. Without this, a ZoneKind added
// to keys.go's const block but left out of both lists here would silently
// go untested by both TestMapKey_TabCompletesInEveryPickerZone and
// TestMapKey_TabPlainAdvanceOutsidePickerZones -- passing the whole suite
// while its Tab behavior (complete or advance) is never asserted at all.
func TestZoneKindPartition_IsExhaustiveAndDisjoint(t *testing.T) {
	seenIn := make(map[ZoneKind]string, len(pickerZoneKinds)+len(nonPickerZoneKinds))
	for _, z := range pickerZoneKinds {
		if prior, ok := seenIn[z]; ok {
			t.Fatalf("zone %v listed in both %s and pickerZoneKinds", z, prior)
		}
		seenIn[z] = "pickerZoneKinds"
	}
	for _, z := range nonPickerZoneKinds {
		if prior, ok := seenIn[z]; ok {
			t.Fatalf("zone %v listed in both %s and nonPickerZoneKinds", z, prior)
		}
		seenIn[z] = "nonPickerZoneKinds"
	}
	for _, z := range allZoneKinds {
		if _, ok := seenIn[z]; !ok {
			t.Errorf("ZoneKind %v is in neither pickerZoneKinds nor nonPickerZoneKinds -- add it to exactly one so its Tab behavior is tested", z)
		}
	}
	if got, want := len(seenIn), len(allZoneKinds); got != want {
		t.Errorf("pickerZoneKinds+nonPickerZoneKinds together name %d zones, want exactly %d (len(allZoneKinds)) -- one of the lists has a stale or duplicate entry not in allZoneKinds", got, want)
	}
}

// sanity-check the fixtures themselves once, up front: if String() ever
// stops matching what MapKey switches on (e.g. an ultraviolet upgrade
// changes Keystroke() formatting), every table test below would silently
// test the wrong branch instead of failing loudly, so pin the strings
// directly.
func TestKeyFixturesMatchExpectedKeystrokes(t *testing.T) {
	cases := []struct {
		msg  tea.KeyPressMsg
		want string
	}{
		{keyTab, "tab"},
		{keyShiftTab, "shift+tab"},
		{keyEnter, "enter"},
		{keyAltEnter, "alt+enter"},
		{keyShiftEnter, "shift+enter"},
		{keyEsc, "esc"},
		{keyCtrlC, "ctrl+c"},
		{keyCtrlS, "ctrl+s"},
		{keyCtrlJ, "ctrl+j"},
		{keyCtrlR, "ctrl+r"},
		{keyX, "x"},
	}
	for _, c := range cases {
		if got := c.msg.String(); got != c.want {
			t.Errorf("%+v.String() = %q, want %q", c.msg, got, c.want)
		}
	}
}

// TestMapKey_GrammarTable is the brief's grammar table, verbatim: each row
// names a keypress, the zone it lands in, and the exact KeyAction MapKey
// must report for it. armed is false (Ctrl+R not currently armed) on every
// row here -- the arm/clear double-tap sequence itself is covered
// separately by TestMapKey_CtrlRDoubleTapArmsThenClears below, since it is
// inherently a two-call sequence rather than a single-row fact.
func TestMapKey_GrammarTable(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyPressMsg
		zone FocusZone
		want KeyAction
	}{
		{
			name: "tab in a picker zone completes rather than plainly advancing",
			msg:  keyTab,
			zone: FocusZone{Kind: ZoneDir},
			want: ActionComplete,
		},
		{
			name: "enter on a non-empty title submits",
			msg:  keyEnter,
			zone: FocusZone{Kind: ZoneTitle, TitleEmpty: false},
			want: ActionSubmit,
		},
		{
			// v2 spec §8, which overrides the row this table carried
			// from v1 ("enter in the prompt advances") and the companion
			// view plan's own footer table: "↵ from the prompt submits
			// rather than advancing. Nothing used it for a newline; ⌃J,
			// ⇧↵ and ⌥↵ keep that job."
			name: "enter in the prompt submits rather than advancing",
			msg:  keyEnter,
			zone: FocusZone{Kind: ZonePrompt},
			want: ActionSubmit,
		},
		{
			name: "enter in a plain zone still advances",
			msg:  keyEnter,
			zone: FocusZone{Kind: ZoneAccount},
			want: ActionAdvance,
		},
		{
			name: "enter on an EMPTY title still advances",
			msg:  keyEnter,
			zone: FocusZone{Kind: ZoneTitle, TitleEmpty: true},
			want: ActionAdvance,
		},
		{
			name: "ctrl+j in the prompt inserts a newline",
			msg:  keyCtrlJ,
			zone: FocusZone{Kind: ZonePrompt},
			want: ActionNewline,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := MapKey(c.msg, c.zone, false)
			if got != c.want {
				t.Errorf("MapKey(%s, %+v, armed=false) = %v, want %v", c.msg.String(), c.zone, got, c.want)
			}
		})
	}
}

// TestMapKey_CtrlRDoubleTapArmsThenClears is the brief's third grammar
// case, threaded through MapKey's armed in/out contract: the first Ctrl+R
// arms (reports ActionArmClear, returns armed=true); a second, consecutive
// Ctrl+R fires the clear (ActionClear, returns armed=false); any other key
// between the two disarms instead of firing the clear. Ported from Atrium's
// HandleKeyPress (textInput_keys.go): "the first press arms, any other key
// disarms, a second consecutive press requests the clear."
func TestMapKey_CtrlRDoubleTapArmsThenClears(t *testing.T) {
	zone := FocusZone{Kind: ZoneTitle}

	action, armed := MapKey(keyCtrlR, zone, false)
	if action != ActionArmClear || !armed {
		t.Fatalf("first ctrl+r: MapKey = (%v, armed=%v), want (ActionArmClear, armed=true)", action, armed)
	}

	action, armed = MapKey(keyCtrlR, zone, armed)
	if action != ActionClear || armed {
		t.Fatalf("second consecutive ctrl+r: MapKey = (%v, armed=%v), want (ActionClear, armed=false)", action, armed)
	}
}

// TestMapKey_CtrlRThenOtherKeyDisarms is the brief's "⌃R then x → disarmed"
// case: once armed, any key other than a second Ctrl+R disarms the gesture
// (not just a no-op -- the very next Ctrl+R after that must arm again from
// scratch, not fire the clear).
func TestMapKey_CtrlRThenOtherKeyDisarms(t *testing.T) {
	zone := FocusZone{Kind: ZoneTitle}

	_, armed := MapKey(keyCtrlR, zone, false)
	if !armed {
		t.Fatalf("first ctrl+r did not arm")
	}

	action, armed := MapKey(keyX, zone, armed)
	if armed {
		t.Errorf("MapKey(x, armed=true) left armed=true, want false (any non-ctrl+r key disarms)")
	}
	if action != ActionNone {
		t.Errorf("MapKey(x, ...) = %v, want ActionNone ('x' is not part of the grammar)", action)
	}

	// The disarm must be real, not cosmetic: the next ctrl+r arms again
	// rather than firing the clear (which would happen if the previous
	// call had silently stayed armed).
	action, armed = MapKey(keyCtrlR, zone, armed)
	if action != ActionArmClear || !armed {
		t.Fatalf("ctrl+r after a disarming key = (%v, armed=%v), want (ActionArmClear, armed=true) -- disarm must be real", action, armed)
	}
}

// TestMapKey_CtrlRArmsRegardlessOfZone pins that the double-tap clear is a
// form-global gesture, not scoped to one zone -- Atrium's own gate is
// "isCreateForm", not any particular focused field, and herdr-draft's form
// is always the create form (task 16 strips Atrium's other overlay roles).
func TestMapKey_CtrlRArmsRegardlessOfZone(t *testing.T) {
	for _, kind := range allZoneKinds {
		zone := FocusZone{Kind: kind}
		action, armed := MapKey(keyCtrlR, zone, false)
		if action != ActionArmClear || !armed {
			t.Errorf("zone %+v: MapKey(ctrl+r, armed=false) = (%v, armed=%v), want (ActionArmClear, true)", zone, action, armed)
		}
	}
}

// TestMapKey_EveryNonCtrlRKeyDisarms exercises the "any other key" half of
// the double-tap gesture across every case MapKey's switch recognizes, not
// just an arbitrary typed character -- Atrium disarms unconditionally
// before its own switch runs (textInput_keys.go: `t.clearArmed = false`
// right after the ctrl+r branch), so navigation and submission chords must
// disarm exactly like a plain keystroke does.
func TestMapKey_EveryNonCtrlRKeyDisarms(t *testing.T) {
	msgs := []tea.KeyPressMsg{keyTab, keyShiftTab, keyEnter, keyAltEnter, keyEsc, keyCtrlC, keyCtrlS, keyCtrlJ, keyX}
	for _, msg := range msgs {
		_, armed := MapKey(msg, FocusZone{Kind: ZonePrompt}, true)
		if armed {
			t.Errorf("MapKey(%s, armed=true) left armed=true, want false", msg.String())
		}
	}
}

// TestMapKey_TabPlainAdvanceOutsidePickerZones pins the other half of "Tab
// = complete-then-advance in pickers, plain advance elsewhere" (spec §6):
// a non-picker zone gets ActionAdvance on Tab, never ActionComplete. Its
// zone list is nonPickerZoneKinds, kept in explicit opposition to
// pickerZoneKinds -- see TestMapKey_TabCompletesInEveryPickerZone
// immediately below for the other half, and
// TestZoneKindPartition_IsExhaustiveAndDisjoint for the check that the two
// lists actually partition every zone.
func TestMapKey_TabPlainAdvanceOutsidePickerZones(t *testing.T) {
	for _, kind := range nonPickerZoneKinds {
		zone := FocusZone{Kind: kind}
		if got, _ := MapKey(keyTab, zone, false); got != ActionAdvance {
			t.Errorf("zone %+v: MapKey(tab) = %v, want ActionAdvance", zone, got)
		}
	}
}

// TestMapKey_TabCompletesInEveryPickerZone is the picker-side half of "Tab
// = complete-then-advance in pickers, plain advance elsewhere" (spec §6),
// covering all three zones ZoneKind.isPicker() reports true for
// (pickerZoneKinds: ZoneIssue, ZoneDir, ZoneBase). Before this test, only
// ZoneDir was asserted (via TestMapKey_GrammarTable's literal brief-table
// row), so a regression dropping ZoneIssue or ZoneBase from isPicker()
// (keys.go) would have passed the entire suite -- this closes that gap by
// asserting ActionComplete for every zone the list names, symmetric with
// TestMapKey_TabPlainAdvanceOutsidePickerZones above.
func TestMapKey_TabCompletesInEveryPickerZone(t *testing.T) {
	for _, kind := range pickerZoneKinds {
		zone := FocusZone{Kind: kind}
		if got, _ := MapKey(keyTab, zone, false); got != ActionComplete {
			t.Errorf("zone %+v: MapKey(tab) = %v, want ActionComplete", zone, got)
		}
	}
}

// TestMapKey_ShiftTabAlwaysBacksRegardlessOfZone pins that, unlike Tab,
// Shift+Tab never gets the complete-then-advance treatment in any zone --
// Atrium's HandleKeyPress calls nextEnabledIndex(-1) unconditionally for
// "shift+tab", with no picker special-case at all.
func TestMapKey_ShiftTabAlwaysBacksRegardlessOfZone(t *testing.T) {
	for _, zone := range []FocusZone{{Kind: ZoneDir}, {Kind: ZoneIssue}, {Kind: ZoneBase}, {Kind: ZoneTitle}} {
		if got, _ := MapKey(keyShiftTab, zone, false); got != ActionBack {
			t.Errorf("zone %+v: MapKey(shift+tab) = %v, want ActionBack", zone, got)
		}
	}
}

// TestMapKey_EscAndCtrlCCancelFromEveryZone covers "Esc/⌃C cancel" (spec
// §6) as a from-anywhere shortcut, the same way Ctrl+S submits from
// anywhere (TestMapKey_CtrlSSubmitsFromEveryZone below).
func TestMapKey_EscAndCtrlCCancelFromEveryZone(t *testing.T) {
	zones := []FocusZone{{Kind: ZoneTitle}, {Kind: ZonePrompt}, {Kind: ZoneCreate}, {Kind: ZoneDir}}
	for _, zone := range zones {
		if got, _ := MapKey(keyEsc, zone, false); got != ActionCancel {
			t.Errorf("zone %+v: MapKey(esc) = %v, want ActionCancel", zone, got)
		}
		if got, _ := MapKey(keyCtrlC, zone, false); got != ActionCancel {
			t.Errorf("zone %+v: MapKey(ctrl+c) = %v, want ActionCancel", zone, got)
		}
	}
}

// TestMapKey_CtrlSSubmitsFromEveryZone covers "⌃S submits from anywhere"
// (spec §6), ported from Atrium's unconditional `case "ctrl+s"` branch.
func TestMapKey_CtrlSSubmitsFromEveryZone(t *testing.T) {
	zones := []FocusZone{
		{Kind: ZoneTitle, TitleEmpty: true}, // even an empty, non-submittable-by-Enter title
		{Kind: ZonePrompt}, {Kind: ZoneCreate}, {Kind: ZoneDir}, {Kind: ZoneAccount},
	}
	for _, zone := range zones {
		if got, _ := MapKey(keyCtrlS, zone, false); got != ActionSubmit {
			t.Errorf("zone %+v: MapKey(ctrl+s) = %v, want ActionSubmit", zone, got)
		}
	}
}

// TestMapKey_EnterOnEmptyTitleAdvancesInsteadOfSubmitting is the other half
// of "Enter ... submits from a non-empty Title" (spec §6): an empty title
// falls through to the shared advance, exactly as Atrium's comment
// documents ("an *empty* title falls through to the advance below").
func TestMapKey_EnterOnEmptyTitleAdvancesInsteadOfSubmitting(t *testing.T) {
	got, _ := MapKey(keyEnter, FocusZone{Kind: ZoneTitle, TitleEmpty: true}, false)
	if got != ActionAdvance {
		t.Errorf("MapKey(enter, title, titleEmpty=true) = %v, want ActionAdvance", got)
	}
}

// TestMapKey_EnterOnCreateAlwaysSubmits covers "Enter ... submits ... from
// Create" (spec §6): the Create button submits on Enter regardless of any
// title state (TitleEmpty is meaningless off the Title zone, but must not
// leak in and block the submit).
func TestMapKey_EnterOnCreateAlwaysSubmits(t *testing.T) {
	got, _ := MapKey(keyEnter, FocusZone{Kind: ZoneCreate}, false)
	if got != ActionSubmit {
		t.Errorf("MapKey(enter, create) = %v, want ActionSubmit", got)
	}
}

// TestMapKey_AltEnterInsertsNewlineInPromptOnly covers the Alt+Enter half
// of "⌃J/⇧↵/⌥↵ newline ONLY in the prompt zone" -- ported from Atrium's
// comment on the "enter", "alt+enter" case: Alt+Enter is the
// terminal-independent newline route (it arrives even on a legacy
// terminal, as ESC CR, unlike the real Shift+Enter). Outside the prompt it
// must not insert anything -- MapKey reports ActionNone, same as any other
// keystroke that isn't part of the grammar for that zone.
func TestMapKey_AltEnterInsertsNewlineInPromptOnly(t *testing.T) {
	if got, _ := MapKey(keyAltEnter, FocusZone{Kind: ZonePrompt}, false); got != ActionNewline {
		t.Errorf("MapKey(alt+enter, prompt) = %v, want ActionNewline", got)
	}
	// Outside the prompt, alt+enter is grouped with plain "enter" in
	// Atrium's own switch (`case "enter", "alt+enter":`) -- its
	// newline-insertion behavior only fires inside the isTextarea()
	// branch. So on a zone where Enter itself would advance (an empty
	// title -- see TestMapKey_EnterOnEmptyTitleAdvancesInsteadOfSubmitting
	// for why this zone, not e.g. ZoneWorktree, isolates "behaves like
	// plain enter" from "submits"), alt+enter must advance too, not
	// insert anything.
	if got, _ := MapKey(keyAltEnter, FocusZone{Kind: ZoneTitle, TitleEmpty: true}, false); got != ActionAdvance {
		t.Errorf("MapKey(alt+enter, title, titleEmpty=true) = %v, want ActionAdvance (alt+enter is a plain enter outside the prompt)", got)
	}
	// And on a zone/title state where plain Enter would submit, alt+enter
	// must submit too -- it is not a "never submits" key, only a
	// "inserts a newline instead of advancing, in the prompt" key.
	if got, _ := MapKey(keyAltEnter, FocusZone{Kind: ZoneTitle, TitleEmpty: false}, false); got != ActionSubmit {
		t.Errorf("MapKey(alt+enter, title, titleEmpty=false) = %v, want ActionSubmit (grouped with plain enter outside the prompt)", got)
	}
}

// TestMapKey_ShiftEnterInsertsNewlineInPromptOnlyElseInert covers the
// Shift+Enter half of "⌃J/⇧↵/⌥↵ newline ONLY in the prompt zone" -- ported
// from Atrium's dedicated "shift+enter" case, which is newline-only and
// deliberately NOT folded into the "enter"/"alt+enter" case (Atrium's
// comment: folding it in "would mean Shift+Enter creating a session from
// the field next door"). Outside the prompt, Atrium swallows it as a
// silent no-op rather than treating it as a plain Enter; MapKey reports
// ActionNone for the same effect (nothing in the grammar advances, submits,
// or cancels on it outside the prompt).
func TestMapKey_ShiftEnterInsertsNewlineInPromptOnlyElseInert(t *testing.T) {
	if got, _ := MapKey(keyShiftEnter, FocusZone{Kind: ZonePrompt}, false); got != ActionNewline {
		t.Errorf("MapKey(shift+enter, prompt) = %v, want ActionNewline", got)
	}
	if got, _ := MapKey(keyShiftEnter, FocusZone{Kind: ZoneCreate}, false); got != ActionNone {
		t.Errorf("MapKey(shift+enter, create) = %v, want ActionNone (inert outside the prompt, not a submit)", got)
	}
}

// TestMapKey_CtrlJInertOutsidePrompt is the ctrl+j half of
// TestMapKey_TabPlainAdvanceOutsidePickerZones-style zone-scoping: the
// grammar table only requires ctrl+j -> ActionNewline in the prompt zone;
// this pins that it does nothing grammar-wise anywhere else.
func TestMapKey_CtrlJInertOutsidePrompt(t *testing.T) {
	for _, zone := range []FocusZone{{Kind: ZoneTitle}, {Kind: ZoneCreate}, {Kind: ZoneDir}} {
		if got, _ := MapKey(keyCtrlJ, zone, false); got != ActionNone {
			t.Errorf("zone %+v: MapKey(ctrl+j) = %v, want ActionNone", zone, got)
		}
	}
}

// TestMapKey_UnrecognizedKeyReportsActionNone covers the "forward to the
// focused widget" default path: a plain typed character isn't part of the
// grammar in any zone, so MapKey must report ActionNone (not
// ActionAdvance, not a panic) and leave it to the caller to route the
// keypress to whichever widget owns the zone.
func TestMapKey_UnrecognizedKeyReportsActionNone(t *testing.T) {
	for _, zone := range []FocusZone{{Kind: ZoneTitle}, {Kind: ZonePrompt}, {Kind: ZoneDir}} {
		if got, _ := MapKey(keyX, zone, false); got != ActionNone {
			t.Errorf("zone %+v: MapKey(x) = %v, want ActionNone", zone, got)
		}
	}
}

// TestHandlePaste_AlwaysDisarms is the provenance-required paste-routing
// guard, ported from Atrium's HandlePaste (textInput_keys.go): "A paste is
// not the second half of a double-tap: it disarms the clear gesture
// exactly as any other non-Ctrl+R input does." HandlePaste is a function
// distinct from MapKey/HandleKeyPress precisely so a pasted clipboard can
// never be routed through the msg.String()-keyed switch above -- MapKey
// only accepts tea.KeyPressMsg, a type tea.PasteMsg's bracketed-paste
// content is never wrapped in, so a clipboard containing the literal text
// "esc" structurally cannot reach the "esc" -> ActionCancel case. This test
// pins HandlePaste's own half of the contract: whatever the prior armed
// state was, a paste always reports it disarmed.
func TestHandlePaste_AlwaysDisarms(t *testing.T) {
	if got := HandlePaste(); got {
		t.Errorf("HandlePaste() = %v, want false (a paste always disarms the ctrl+r double-tap)", got)
	}
}
