package form

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// testModelID stands in for agentopts' model-id rule, which this package
// cannot import: the app hands the real one in as OptionSpec.Valid.
var testModelID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/\[\]-]{0,63}$`)

// claudeSpecs is Claude's declaration as the app would push it (agent-options
// spec §4.2). Built by hand here because internal/form deliberately does not
// import internal/agentopts.
func claudeSpecs() []OptionSpec {
	return []OptionSpec{
		{
			Name: "model", Label: "model", Flag: "--model",
			Choices: []OptionChoice{
				{Value: "fable", Label: "fable"}, {Value: "opus", Label: "opus"},
				{Value: "sonnet", Label: "sonnet"}, {Value: "haiku", Label: "haiku"},
			},
			FreeText: true,
			Valid:    func(v string) bool { return testModelID.MatchString(v) && v != "inherit" },
			Row:      func(v string) string { return v },
		},
		{
			Name: "effort", Label: "effort", Flag: "--effort",
			Choices: []OptionChoice{
				{Value: "low", Label: "low"}, {Value: "medium", Label: "medium"},
				{Value: "high", Label: "high"}, {Value: "xhigh", Label: "xhigh"}, {Value: "max", Label: "max"},
			},
			Row: func(v string) string { return "effort " + v },
		},
		{
			Name: "permission_mode", Label: "mode", Flag: "--permission-mode",
			Choices: []OptionChoice{
				{Value: "manual", Label: "manual"}, {Value: "plan", Label: "plan"},
				{Value: "acceptEdits", Label: "accept-edits"}, {Value: "auto", Label: "auto"},
			},
			Row: func(v string) string {
				if v == "acceptEdits" {
					v = "accept-edits"
				}
				return v + " mode"
			},
		},
	}
}

func claudeKind(seed map[string]string) KindOptions {
	return KindOptions{Specs: claudeSpecs(), Seed: seed}
}

func newClaudeOptions(t *testing.T, seed map[string]string) *OptionsField {
	t.Helper()
	f := NewOptionsField(theme.Default())
	f.SetKind("claude", claudeKind(seed))
	return f
}

func plainRow(f *OptionsField) string { return strings.TrimRight(ansi.Strip(f.Row(80)), " ") }

func plainPanel(f *OptionsField, h int) string { return ansi.Strip(f.Panel(80, h)) }

func TestOptionsField_IDLabelAndResting(t *testing.T) {
	f := NewOptionsField(theme.Default())
	if f.ID() != "options" || f.Label() != "options" {
		t.Fatalf("ID/Label = %q/%q", f.ID(), f.Label())
	}
	// Before the app names a kind there is nothing to choose.
	if f.Enabled() {
		t.Fatalf("a field with no kind is enabled")
	}
	if f.Values() != nil {
		t.Fatalf("Values() = %v, want nil", f.Values())
	}
}

// Agent-options spec §7.1: nothing set, nothing in extra_args -- the row
// says whose settings decide.
func TestOptionsField_NothingSetSaysTheAgentDecides(t *testing.T) {
	f := newClaudeOptions(t, nil)
	if !f.Enabled() {
		t.Fatalf("claude's options are not enabled")
	}
	if got := plainRow(f); got != "claude's own settings" {
		t.Fatalf("row = %q", got)
	}
	if f.Values() != nil {
		t.Fatalf("Values() = %v, want nil", f.Values())
	}
}

func TestOptionsField_SeedIsTheStartingValue(t *testing.T) {
	f := newClaudeOptions(t, map[string]string{"effort": "xhigh", "permission_mode": "acceptEdits"})
	if got, want := f.Values(), map[string]string{"effort": "xhigh", "permission_mode": "acceptEdits"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Values() = %v, want %v", got, want)
	}
	if got := plainRow(f); got != "effort xhigh · accept-edits mode" {
		t.Fatalf("row = %q", got)
	}
}

// A seeded model id that is not an offered alias lands on `other`, with the
// id in the name input -- a config.toml `model = "claude-opus-5[1m]"` must
// open showing exactly that.
func TestOptionsField_ATypedSeedLandsOnOther(t *testing.T) {
	f := newClaudeOptions(t, map[string]string{"model": "claude-opus-5[1m]"})
	if got := f.Values()["model"]; got != "claude-opus-5[1m]" {
		t.Fatalf("model = %q", got)
	}
	panel := plainPanel(f, 8)
	if !strings.Contains(panel, "name") || !strings.Contains(panel, "claude-opus-5[1m]") {
		t.Fatalf("panel does not show the name part with the seeded id:\n%s", panel)
	}
}

// The part grammar (spec §7.2, the worktree panel's): ↑↓ between parts,
// ←→ along the focused part's chips.
func TestOptionsField_PartGrammar(t *testing.T) {
	f := newClaudeOptions(t, nil)
	f.Focus()
	f.Update(key(tea.KeyRight, 0)) // model: inherit -> fable
	f.Update(key(tea.KeyDown, 0))  // -> effort
	f.Update(key(tea.KeyRight, 0)) // effort: inherit -> low
	f.Update(key(tea.KeyRight, 0)) // -> medium
	f.Update(key(tea.KeyDown, 0))  // -> mode
	f.Update(key(tea.KeyLeft, 0))  // mode: inherit wraps -> auto
	f.Update(key(tea.KeyDown, 0))  // clamped at the last part
	f.Update(key(tea.KeyLeft, 0))  // -> acceptEdits
	want := map[string]string{"model": "fable", "effort": "medium", "permission_mode": "acceptEdits"}
	if got := f.Values(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Values() = %v, want %v", got, want)
	}
	if got := plainRow(f); got != "fable · effort medium · accept-edits mode" {
		t.Fatalf("row = %q", got)
	}
}

// Choosing `other` opens the name part under the model line; typing there
// is the value.
func TestOptionsField_OtherOpensTheNamePart(t *testing.T) {
	f := newClaudeOptions(t, nil)
	f.Focus()
	f.Update(key(tea.KeyLeft, 0)) // model: inherit wraps -> other
	if f.Values() != nil {
		t.Fatalf("other with no name sent %v, want nothing", f.Values())
	}
	f.Update(key(tea.KeyDown, 0)) // -> name, not effort
	for _, r := range "opus[1m]" {
		f.Update(rn(r))
	}
	if got := f.Values()["model"]; got != "opus[1m]" {
		t.Fatalf("model = %q", got)
	}
	f.Update(key(tea.KeyDown, 0)) // name -> effort
	f.Update(key(tea.KeyRight, 0))
	if got := f.Values()["effort"]; got != "low" {
		t.Fatalf("the second ↓ did not reach effort: %v", f.Values())
	}
}

// Typing on the model's chip line goes straight to `other` and the name --
// "just type the model" is the obvious thing to try, and a keystroke that
// did nothing would read as broken.
func TestOptionsField_TypingOnTheModelChipsTypesAName(t *testing.T) {
	f := newClaudeOptions(t, nil)
	f.Focus()
	for _, r := range "sonnet-5" {
		f.Update(rn(r))
	}
	if got := f.Values()["model"]; got != "sonnet-5" {
		t.Fatalf("model = %q, want the typed id", got)
	}
}

// A key that could not start a model id does not leave the chips: a stray
// space or dash on a chosen `opus` must not quietly swap it for an empty
// `other`, which sends nothing.
func TestOptionsField_AnInvalidKeyOnTheModelChipsChangesNothing(t *testing.T) {
	f := newClaudeOptions(t, map[string]string{"model": "opus"})
	f.Focus()
	f.Update(rn(' '))
	f.Update(rn('-'))
	if got := f.Values()["model"]; got != "opus" {
		t.Fatalf("model = %q, want opus untouched", got)
	}
	if part, _ := f.current(); part.onName {
		t.Fatalf("the cursor moved to the name part on a key that types nothing")
	}
}

// Typing on a line that takes no text does nothing at all.
func TestOptionsField_TypingOnAClosedLineIsIgnored(t *testing.T) {
	f := newClaudeOptions(t, nil)
	f.Focus()
	f.Update(key(tea.KeyDown, 0)) // effort
	f.Update(rn('x'))
	if f.Values() != nil {
		t.Fatalf("Values() = %v, want nothing", f.Values())
	}
}

// The name input refuses an edit that would make an invalid id (spec
// §7.2): a leading dash is how a value becomes a flag.
func TestOptionsField_NameRefusesAnInvalidEdit(t *testing.T) {
	f := newClaudeOptions(t, nil)
	f.Focus()
	f.Update(key(tea.KeyLeft, 0)) // other
	f.Update(key(tea.KeyDown, 0)) // name
	f.Update(rn('-'))
	f.Update(rn(' '))
	f.Update(tea.PasteMsg{Content: "--dangerously-skip-permissions"})
	if got := f.Values(); got != nil {
		t.Fatalf("Values() = %v, want the edits refused", got)
	}
	f.Update(rn('o'))
	f.Update(rn(' ')) // a space after a valid id is refused too
	if got := f.Values()["model"]; got != "o" {
		t.Fatalf("model = %q, want %q", got, "o")
	}
}

// Spec §7.2: a kind's choices persist for the life of the popup, so a trip
// through another agent kind and back restores them.
func TestOptionsField_AKindsChoicesSurviveARoundTrip(t *testing.T) {
	f := newClaudeOptions(t, map[string]string{"effort": "high"})
	f.Focus()
	f.Update(key(tea.KeyRight, 0)) // model -> fable
	f.SetKind("codex", KindOptions{})
	if f.Enabled() || f.Values() != nil {
		t.Fatalf("codex: enabled=%v values=%v, want inert and empty", f.Enabled(), f.Values())
	}
	f.SetKind("claude", claudeKind(map[string]string{"effort": "high"}))
	if got, want := f.Values(), map[string]string{"model": "fable", "effort": "high"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Values() after the round trip = %v, want %v", got, want)
	}
}

// Spec §7.1's inert row: a kind that declares nothing costs one dim line,
// no focus stop, and ignores input even when a click focuses it.
func TestOptionsField_AKindWithNothingDeclaredIsInert(t *testing.T) {
	f := NewOptionsField(theme.Default())
	f.SetKind("codex", KindOptions{ExtraArgs: []string{"--full-auto"}})
	if f.Enabled() {
		t.Fatalf("codex options are enabled")
	}
	if got := plainRow(f); got != "none for codex" {
		t.Fatalf("row = %q", got)
	}
	f.Focus()
	f.Update(key(tea.KeyRight, 0))
	f.Update(rn('x'))
	if f.Values() != nil {
		t.Fatalf("an inert field took input: %v", f.Values())
	}
	panel := plainPanel(f, 4)
	if !strings.Contains(panel, "codex takes no session options") || !strings.Contains(panel, "--full-auto") {
		t.Fatalf("inert panel does not say why, or hides extra_args:\n%s", panel)
	}
	if got := f.FooterRungs(); !reflect.DeepEqual(got, []string{"nothing to set here"}) {
		t.Fatalf("FooterRungs = %q", got)
	}
}

// Spec §7.1/§7.2: extra_args' own pin is what an inherited option launches
// with, so the row shows it (dim) and the hint names where it comes from.
func TestOptionsField_ExtraArgsPinShowsThroughInherit(t *testing.T) {
	f := NewOptionsField(theme.Default())
	f.SetKind("claude", KindOptions{
		Specs:     claudeSpecs(),
		ExtraArgs: []string{"--model", "opus", "--verbose"},
		Pinned:    map[string]string{"model": "opus"},
	})
	f.Focus()
	if got := plainRow(f); got != "opus" {
		t.Fatalf("row = %q, want extra_args' model", got)
	}
	panel := plainPanel(f, 8)
	for _, want := range []string{"[agents.extra_args] passes --model opus", "[agents.extra_args] adds: --model opus --verbose"} {
		if !strings.Contains(panel, want) {
			t.Errorf("panel missing %q:\n%s", want, panel)
		}
	}
	f.Update(key(tea.KeyRight, 0)) // fable
	if panel := plainPanel(f, 8); !strings.Contains(panel, "sends --model fable instead of [agents.extra_args]'s opus") {
		t.Errorf("panel does not say the choice displaces extra_args:\n%s", panel)
	}
}

func TestOptionsField_HintSaysWhatThePartSends(t *testing.T) {
	f := newClaudeOptions(t, nil)
	f.Focus()
	if panel := plainPanel(f, 8); !strings.Contains(panel, "inherit sends no --model, so claude's own settings decide") {
		t.Fatalf("inherit hint missing:\n%s", panel)
	}
	f.Update(key(tea.KeyDown, 0))
	f.Update(key(tea.KeyLeft, 0)) // effort: max
	if panel := plainPanel(f, 8); !strings.Contains(panel, "sends --effort max") {
		t.Fatalf("value hint missing:\n%s", panel)
	}
}

// The panel gives up its extras bottom-first and never its parts: at the
// three-row floor, the three options are the whole panel.
func TestOptionsField_PanelShortensFromTheBottom(t *testing.T) {
	f := NewOptionsField(theme.Default())
	f.SetKind("claude", KindOptions{
		Specs: claudeSpecs(), Seed: map[string]string{"effort": "low"}, SeedSource: "config.toml",
		ExtraArgs: []string{"--verbose"},
	})
	f.SetNotes([]string{"ignoring [agents.options.claude] sandbox = \"on\": claude declares no sandbox option"})
	if got, want := f.PanelRows(), 3+1+1+1+1; got != want {
		t.Fatalf("PanelRows = %d, want %d", got, want)
	}
	full := plainPanel(f, f.PanelRows())
	for _, want := range []string{"model", "effort", "mode", "inherit sends no --model", "[agents.extra_args] adds", "ignoring [agents.options.claude]", "from config.toml"} {
		if !strings.Contains(full, want) {
			t.Errorf("full panel missing %q:\n%s", want, full)
		}
	}
	floor := plainPanel(f, 3)
	if strings.Contains(floor, "sends") || !strings.Contains(floor, "mode") {
		t.Fatalf("a three-row panel should be exactly the parts:\n%s", floor)
	}
	five := plainPanel(f, 5)
	if !strings.Contains(five, "[agents.extra_args] adds") || strings.Contains(five, "ignoring") {
		t.Fatalf("a five-row panel keeps the hint and extra_args, drops the note:\n%s", five)
	}
}

// Provenance speaks only while the values ARE the config file's.
func TestOptionsField_ProvenanceOnlyWhileTheSeedStands(t *testing.T) {
	f := NewOptionsField(theme.Default())
	f.SetKind("claude", KindOptions{Specs: claudeSpecs(), Seed: map[string]string{"effort": "low"}, SeedSource: "config.toml"})
	f.Focus()
	if !strings.Contains(plainPanel(f, 8), "from config.toml") {
		t.Fatalf("seeded panel has no provenance")
	}
	f.Update(key(tea.KeyRight, 0)) // model -> fable
	if strings.Contains(plainPanel(f, 8), "from config.toml") {
		t.Fatalf("provenance survived a change")
	}
}

func TestOptionsField_FooterRungsFollowThePart(t *testing.T) {
	f := newClaudeOptions(t, nil)
	f.Focus()
	if got := f.FooterRungs(); len(got) == 0 || !strings.Contains(got[0], "←→ value") {
		t.Fatalf("chip-part rungs = %q", got)
	}
	f.Update(key(tea.KeyLeft, 0)) // other
	f.Update(key(tea.KeyDown, 0)) // name
	if got := f.FooterRungs(); len(got) == 0 || !strings.Contains(got[0], "type") {
		t.Fatalf("name-part rungs = %q", got)
	}
}

// Row is one line at any width, as every Section's must be.
func TestOptionsField_RowFitsNarrowWidths(t *testing.T) {
	f := newClaudeOptions(t, map[string]string{"model": "claude-opus-5[1m]", "effort": "xhigh", "permission_mode": "plan"})
	for _, w := range []int{1, 8, 20, 40} {
		row := f.Row(w)
		if strings.Contains(row, "\n") || ansi.StringWidth(row) != w {
			t.Errorf("Row(%d) = %q (width %d), want one line of exactly %d cells", w, row, ansi.StringWidth(row), w)
		}
	}
}
