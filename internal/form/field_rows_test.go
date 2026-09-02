package form

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// field_rows_test.go covers Section's row-and-panel contract (form.go)
// across the fields that answer it the same way: issue, title, prompt,
// project, placement, agent, account. The worktree field's own row,
// panel and sub-focus grammar are covered in field_worktree_test.go,
// where the sub-cursor they all turn on lives.

// --- helpers --------------------------------------------------------------

// rowText is one row line as the reader sees it: ANSI stripped and the
// column padding trimmed off the right.
func rowText(s string) string { return strings.TrimRight(ansi.Strip(s), " ") }

// panelLineText is rowText for a PANEL line, which additionally carries
// the two-cell gutter Panel composes every line into (rowvalues.go).
func panelLineText(s string) string { return strings.TrimPrefix(rowText(s), "  ") }

// fieldText is a field's whole visible surface as the reader sees it --
// its one row plus its full panel, ANSI stripped. It replaces v1's
// View(inner, h) in the per-field tests, which had one rendering to
// assert against; v2 has two, and every fact they used to check lives in
// one or the other.
func fieldText(s Section, w int) string {
	return ansi.Strip(s.Row(w) + "\n" + s.Panel(w, s.PanelRows()))
}

// panelLineAt returns panel line i of a Panel(w, h) render, as text.
func panelLineAt(panel string, i int) string {
	return panelLineText(strings.Split(panel, "\n")[i])
}

// rowFields builds one of each, populated with the representative data
// v2 spec §6's own table uses, in the spec's row order (minus the
// worktree, which field_worktree_test.go covers).
func rowFields(palette theme.Palette) []Section {
	issue := NewIssueField(palette)
	issue.SetIssues(1, sampleIssues())
	issue.Update(key(tea.KeyDown, 0)) // none -> ENG-1

	title := NewTitleField(palette)
	title.SetTitle("fix login redirect loop", false)

	prompt := NewPromptField(palette)
	prompt.SetValue("look at the redirect chain\nthen the cookie\nthen the cache", false)

	dir := NewDirField(palette)
	dir.SetHomeDir("/home/zvi")
	dir.SetCandidates(1, []string{"/home/zvi/Projects/herdr-draft", "/home/zvi/Projects/herdr"})

	placement := NewPlacementField(palette)

	agent := NewAgentField(palette)
	agent.SetKinds([]string{"claude", "codex", "aider", "goose"})

	account := NewAccountField(palette)
	account.SetAgentIsClaude(true)
	account.SetProfiles(sampleStatus())

	return []Section{issue, title, prompt, dir, placement, agent, account}
}

// repoConfigFields is rowFields' companion for v2 spec §11's own state:
// the three fields a committed .herdr-draft.toml can add a line to, each
// carrying one. They are a SEPARATE slice rather than an addition to
// rowFields because two of them duplicate IDs it already returns, and one
// of its tests assembles a real form out of it.
//
// The worktree is included here even though field_worktree_test.go
// otherwise owns it: the generic Panel/PanelRows contract is exactly what a
// conditional panel line is easiest to break, and this is where that
// contract is checked.
func repoConfigFields(palette theme.Palette) []Section {
	dir := NewDirField(palette)
	dir.SetCandidates(1, []string{"/home/zvi/Projects/herdr-draft", "/home/zvi/Projects/herdr"})
	dir.SetNotes([]string{
		"ignoring agents.extra_args: it becomes part of a launched agent's command line",
		"ignoring clauth: a repository does not configure your clauth accounts",
	})

	placement := NewPlacementField(palette)
	placement.SetProvenance(".herdr-draft.toml")

	worktreeOn := NewWorktreeField(palette)
	worktreeOn.SetGitTarget(true)
	worktreeOn.SetOn(true)
	worktreeOn.SetBranch("zvi/fix-login-redirect-loop", false)
	worktreeOn.SetBaseItems(1, []string{"main", "release/1.4"})
	worktreeOn.SetProvenance(".herdr-draft.toml")

	worktreeOff := NewWorktreeField(palette)
	worktreeOff.SetGitTarget(true)
	worktreeOff.SetProvenance(".herdr-draft.toml")

	return []Section{dir, placement, worktreeOn, worktreeOff}
}

// --- Row: exactly one line, exactly the width it was handed ---------------

// TestFieldRow_IsAlwaysExactlyOneLine pins Section.Row's hardest
// contract (form.go): "exactly ONE physical line, exactly w cells wide".
// It is what makes "row i is always at row i" mechanically true, and it
// is the contract lipgloss most readily breaks -- Style.Render word-wraps
// BEFORE applying MaxWidth, so a row composed without Inline(true)
// silently becomes two lines at exactly the widths a real terminal hits.
func TestFieldRow_IsAlwaysExactlyOneLine(t *testing.T) {
	widths := []int{1, 2, 3, 5, 8, 12, 20, 40, 79, 200}
	for _, s := range append(rowFields(theme.Default()), repoConfigFields(theme.Default())...) {
		for _, w := range widths {
			row := s.Row(w)
			if strings.Contains(row, "\n") {
				t.Errorf("%s.Row(%d) spans %d physical lines, want exactly 1: %q",
					s.ID(), w, strings.Count(row, "\n")+1, row)
				continue
			}
			if got := lipgloss.Width(row); got != w {
				t.Errorf("%s.Row(%d) is %d cells wide, want exactly %d: %q",
					s.ID(), w, got, w, rowText(row))
			}
		}
	}
}

// TestFieldRow_IsIdenticalAtEveryWindowHeight is the field-level half of
// TestRowStack_RowsRenderIdenticallyAtEveryHeight: Row takes no height
// parameter, but a field could still leak the window's height into its
// row through state some HEIGHT-taking method mutated -- PromptField's
// Panel calls area.SetRows(h), which is exactly that shape. Rendering the
// whole form at two very different heights and re-reading every row
// afterwards is what turns the doc comment into a contract.
func TestFieldRow_IsIdenticalAtEveryWindowHeight(t *testing.T) {
	palette := theme.Default()
	fields := rowFields(palette)

	const w = 80
	// h = 11 affords all seven rows with no header and no rules; h = 60
	// affords the full chrome. Both show the whole stack, so no scrolling
	// (stackWindow) can explain a difference.
	const short, tall = 11, 60
	shortFrame, tallFrame := layoutFrame(short, len(fields)), layoutFrame(tall, len(fields))
	if shortFrame.Header || shortFrame.Rule1 || !tallFrame.Header || !tallFrame.Rule1 {
		t.Fatalf("this test needs h=%d to drop the chrome and h=%d to keep it; got %+v and %+v",
			short, tall, shortFrame, tallFrame)
	}
	if shortFrame.Rows != len(fields) || tallFrame.Rows != len(fields) {
		t.Fatalf("both heights must show the whole stack; got %d and %d rows", shortFrame.Rows, tallFrame.Rows)
	}

	m := New(Setup{Palette: palette, Sections: fields, Name: "new session"})
	m.Init()

	// Every field's own Row, sampled between renders at each height.
	before := make([]string, len(fields))
	for i, f := range fields {
		before[i] = f.Row(40)
	}

	atShort := viewLines(m, w, short)
	atTall := viewLines(m, w, tall)
	againShort := viewLines(m, w, short)

	for i, f := range fields {
		if got := f.Row(40); got != before[i] {
			t.Errorf("%s.Row(40) changed after rendering the form at two heights:\n before: %q\n  after: %q",
				f.ID(), rowText(before[i]), rowText(got))
		}
		if atShort[i] != atTall[i+2] { // +2: the header and rule 1 at h=60
			t.Errorf("row %d (%s) rendered differently at h=%d and h=%d:\n short: %q\n  tall: %q",
				i, f.ID(), short, tall, rowText(atShort[i]), rowText(atTall[i+2]))
		}
		if atShort[i] != againShort[i] {
			t.Errorf("row %d (%s) did not survive a round trip through the taller render:\n  first: %q\n second: %q",
				i, f.ID(), rowText(atShort[i]), rowText(againShort[i]))
		}
	}
}

// --- Panel: exactly h lines ----------------------------------------------

// TestFieldPanel_IsAlwaysExactlyHLines pins Section.Panel's contract
// (form.go): "exactly h physical lines". The form books the panel's
// height from PanelRows BEFORE calling Panel, so a field that returned a
// different number would push the footer off the frame -- the one line
// v2 spec §9 says is never dropped.
func TestFieldPanel_IsAlwaysExactlyHLines(t *testing.T) {
	heights := []int{1, 2, 3, 4, 6, 10, 24}
	widths := []int{4, 20, 80}
	for _, s := range append(rowFields(theme.Default()), repoConfigFields(theme.Default())...) {
		for _, w := range widths {
			for _, h := range heights {
				lines := strings.Split(s.Panel(w, h), "\n")
				if len(lines) != h {
					t.Errorf("%s.Panel(%d, %d) produced %d lines, want exactly %d",
						s.ID(), w, h, len(lines), h)
				}
			}
		}
	}
}

// TestFieldPanelRows_NeverReservesRowsItCannotFill pins the other half of
// the panel contract: PanelRows is "the greatest number of rows this
// field can put to GOOD USE" -- at least one, never absurd, and (since
// the form hands Panel min(PanelRows(), Region)) always a height Panel
// itself honors.
func TestFieldPanelRows_NeverReservesRowsItCannotFill(t *testing.T) {
	for _, s := range append(rowFields(theme.Default()), repoConfigFields(theme.Default())...) {
		want := s.PanelRows()
		if want < 1 {
			t.Errorf("%s.PanelRows() = %d, want at least 1 (0 means 'no panel at all', which none of these fields is)", s.ID(), want)
			continue
		}
		if got := len(strings.Split(s.Panel(80, want), "\n")); got != want {
			t.Errorf("%s.Panel(80, PanelRows()=%d) produced %d lines", s.ID(), want, got)
		}
	}
}

// TestFieldLabel_IsBarePlainLowercase pins Section.Label's contract:
// the FORM owns the label column, so a label carries no colon, no
// padding, and no capital -- v2 spec §7's "lowercase terse labels".
func TestFieldLabel_IsBarePlainLowercase(t *testing.T) {
	want := map[string]string{
		"issue":     "issue",
		"title":     "title",
		"prompt":    "prompt",
		"dir":       "project", // the row is "project"; the ID stays "dir"
		"placement": "placement",
		"agent":     "agent",
		"account":   "account",
	}
	for _, s := range rowFields(theme.Default()) {
		got := s.Label()
		if got != want[s.ID()] {
			t.Errorf("%s.Label() = %q, want %q", s.ID(), got, want[s.ID()])
		}
		if got != strings.ToLower(got) || strings.ContainsAny(got, ": ") {
			t.Errorf("%s.Label() = %q, want bare lowercase words with no colon and no padding", s.ID(), got)
		}
		if len(got) > labelColWidth-2 {
			t.Errorf("%s.Label() = %q (%d cells), wider than the label column affords (%d)",
				s.ID(), got, len(got), labelColWidth-2)
		}
	}
}

// --- the row vocabulary, per field ---------------------------------------

// TestIssueField_RowVocabulary pins v2 spec §6's issue row in each of its
// three states. The unavailable cell is spelled `unavailable  <reason>`
// with two spaces, per the spec's own table -- the row-stack plan wrote
// it with an em dash and the spec wins.
func TestIssueField_RowVocabulary(t *testing.T) {
	palette := theme.Default()

	unset := NewIssueField(palette)
	if got := rowText(unset.Row(40)); got != "none" {
		t.Errorf("Row on a fresh IssueField = %q, want %q", got, "none")
	}

	set := NewIssueField(palette)
	set.SetIssues(1, sampleIssues())
	set.Update(key(tea.KeyDown, 0))
	if got, want := rowText(set.Row(40)), "ENG-1 · Fix login bug"; got != want {
		t.Errorf("Row with ENG-1 selected = %q, want %q", got, want)
	}

	inert := NewIssueField(palette)
	inert.SetUnavailable("no API key")
	if got, want := rowText(inert.Row(40)), "unavailable  no API key"; got != want {
		t.Errorf("Row while unavailable = %q, want %q", got, want)
	}
	if inert.Enabled() {
		t.Errorf("Enabled() = true while unavailable, want false")
	}
}

// TestTitleField_RowAndPanelVocabulary pins v2 spec §6's title row and
// the verdict's move into the panel -- the change that retired v1's
// 21-cell titleVerdictMaxCells clamp, which cut this exact verdict.
func TestTitleField_RowAndPanelVocabulary(t *testing.T) {
	palette := theme.Default()

	empty := NewTitleField(palette)
	if got := rowText(empty.Row(40)); got != titleRowUnset {
		t.Errorf("Row on an empty TitleField = %q, want %q", got, titleRowUnset)
	}
	if got := panelLineAt(empty.Panel(40, 1), 0); got != "" {
		t.Errorf("Panel with no verdict = %q, want a blank line", got)
	}

	f := NewTitleField(palette)
	f.SetTitle("fix login redirect loop", false)
	if got, want := rowText(f.Row(40)), "fix login redirect loop"; got != want {
		t.Errorf("Row = %q, want %q", got, want)
	}

	// 26 cells of verdict: longer than v1's titleVerdictMaxCells clamp,
	// and the exact string v2 spec §6 names as the one v1 cuts.
	const verdict = "branch: zvi/fix-login-redirect-loop"
	f.SetVerdict(f.Value(), verdict)
	if got, want := panelLineAt(f.Panel(80, 1), 0), verdict; got != want {
		t.Errorf("Panel verdict = %q, want the whole of %q -- the panel is full width", got, want)
	}
	if len(verdict) <= 21 {
		t.Fatalf("this test is only meaningful with a verdict longer than v1's retired 21-cell clamp")
	}
	if got := rowText(f.Row(80)); strings.Contains(got, "branch:") {
		t.Errorf("Row = %q, want no verdict at all: v2 spec §6 puts verdicts in the panel so a recomputing verdict cannot shift text under the cursor", got)
	}

	// A verdict computed for a title the user has since edited away from
	// stops rendering, with no separate Clear call.
	f.SetTitle("something else", false)
	if got := panelLineAt(f.Panel(40, 1), 0); got != "" {
		t.Errorf("Panel after the title moved on = %q, want a blank line", got)
	}
}

// TestPromptField_RowVocabulary pins v2 spec §6's prompt row: the first
// line plus a dim "+N more", or an em dash when empty.
func TestPromptField_RowVocabulary(t *testing.T) {
	palette := theme.Default()

	empty := NewPromptField(palette)
	if got := rowText(empty.Row(40)); got != promptRowEmpty {
		t.Errorf("Row on an empty PromptField = %q, want %q", got, promptRowEmpty)
	}

	one := NewPromptField(palette)
	one.SetValue("just the one line", false)
	if got, want := rowText(one.Row(40)), "just the one line"; got != want {
		t.Errorf("Row of a one-line prompt = %q, want %q (no count)", got, want)
	}

	many := NewPromptField(palette)
	many.SetValue("first line\nsecond\nthird", false)
	if got, want := rowText(many.Row(40)), "first line +2 more"; got != want {
		t.Errorf("Row of a three-line prompt = %q, want %q", got, want)
	}

	// A trailing newline is not another line of content, and a leading
	// blank line is not the first one.
	trailing := NewPromptField(palette)
	trailing.SetValue("\nfirst line\n", false)
	if got, want := rowText(trailing.Row(40)), "first line"; got != want {
		t.Errorf("Row of %q = %q, want %q -- blank lines are not content", "\\nfirst line\\n", got, want)
	}
}

// TestDirField_RowVocabulary pins v2 spec §6's project row: the path with
// "~" collapsed, plus the spec's own `invalid` / `not a repository`
// verdict words.
func TestDirField_RowVocabulary(t *testing.T) {
	palette := theme.Default()
	const path = "/home/zvi/Projects/herdr-draft"

	fresh := NewDirField(palette)
	if got := rowText(fresh.Row(40)); got != dirRowNone {
		t.Errorf("Row with no candidates = %q, want %q", got, dirRowNone)
	}

	d := NewDirField(palette)
	d.SetCandidates(1, []string{path})
	if got := rowText(d.Row(60)); got != path {
		t.Errorf("Row with no home dir installed = %q, want the raw path %q", got, path)
	}

	d.SetHomeDir("/home/zvi")
	if got, want := rowText(d.Row(60)), "~/Projects/herdr-draft"; got != want {
		t.Errorf("Row after SetHomeDir = %q, want %q", got, want)
	}
	if got := d.Value(); got != path {
		t.Errorf("Value() after SetHomeDir = %q, want the REAL path %q -- collapsing is display only", got, path)
	}

	d.SetValidity(path, ValidityInvalid)
	if got, want := rowText(d.Row(60)), "~/Projects/herdr-draft  invalid"; got != want {
		t.Errorf("Row with an invalid verdict = %q, want %q", got, want)
	}
	d.SetValidity(path, ValidityDirect)
	if got, want := rowText(d.Row(60)), "~/Projects/herdr-draft  not a repository"; got != want {
		t.Errorf("Row with a non-repository verdict = %q, want %q", got, want)
	}
	d.SetValidity(path, ValidityRepo)
	if got, want := rowText(d.Row(60)), "~/Projects/herdr-draft"; got != want {
		t.Errorf("Row with a plain repository = %q, want %q (no marker)", got, want)
	}
}

// TestDirField_CollapseHomeStopsAtASegmentBoundary pins the one way a
// naive prefix replacement goes wrong: "/home/zvi" must not turn
// "/home/zvirus/x" into "~us/x".
func TestDirField_CollapseHomeStopsAtASegmentBoundary(t *testing.T) {
	d := NewDirField(theme.Default())
	d.SetHomeDir("/home/zvi")

	cases := map[string]string{
		"/home/zvi":           "~",
		"/home/zvi/":          "~/",
		"/home/zvi/Projects":  "~/Projects",
		"/home/zvirus/x":      "/home/zvirus/x",
		"/homely/zvi":         "/homely/zvi",
		"/var/tmp":            "/var/tmp",
		"relative/no/leading": "relative/no/leading",
	}
	for in, want := range cases {
		if got := d.collapseHome(in); got != want {
			t.Errorf("collapseHome(%q) = %q, want %q", in, got, want)
		}
	}

	// An uninstalled (or root) home collapses nothing at all.
	none := NewDirField(theme.Default())
	if got := none.collapseHome("/home/zvi/x"); got != "/home/zvi/x" {
		t.Errorf("collapseHome with no home installed = %q, want the path unchanged", got)
	}
	none.SetHomeDir("/")
	if got := none.collapseHome("/home/zvi/x"); got != "/home/zvi/x" {
		t.Errorf("collapseHome with home=\"/\" = %q, want the path unchanged -- \"/\" would collapse every absolute path", got)
	}
}

// TestPlacementField_RowAndPanelVocabulary pins v2 spec §6's placement
// row (lowercase, one word per choice) and the panel's per-choice
// explanation, which lives on widgets.Chip.FocusHint -- v2 spec §7's one
// widget-adjacent change, and the only reason no new widget code was
// needed for it.
func TestPlacementField_RowAndPanelVocabulary(t *testing.T) {
	palette := theme.Default()
	f := NewPlacementField(palette)

	want := []struct {
		value plan.Placement
		row   string
	}{
		{plan.PlacementNewSpace, "new space"},
		{plan.PlacementTabHere, "tab here"},
		{plan.PlacementSplitHere, "split here"},
	}
	for _, c := range want {
		f.SetValue(c.value)
		if got := rowText(f.Row(40)); got != c.row {
			t.Errorf("Row for %v = %q, want %q", c.value, got, c.row)
		}
		hint := panelLineAt(f.Panel(40, 2), 1)
		if hint == "" {
			t.Errorf("Panel for %v has no explanation line", c.value)
		}
	}

	// Every chip carries its own explanation and its own lowercase label:
	// the row reads the label straight off the selected chip, so a chip
	// added without either would show an empty row rather than a wrong
	// one, which is the failure this catches.
	for _, chip := range placementChips {
		if chip.FocusHint == "" {
			t.Errorf("placementChips[%q].FocusHint is empty; the panel's explanation line reads it", chip.ID)
		}
		if chip.Label != strings.ToLower(chip.Label) {
			t.Errorf("placementChips[%q].Label = %q, want lowercase (v2 spec §3 rule 5)", chip.ID, chip.Label)
		}
	}

	f.SetWorktreeOn(true)
	if got := rowText(f.Row(60)); got != placementInertHint {
		t.Errorf("Row while a worktree is on = %q, want %q", got, placementInertHint)
	}
	if got := panelLineAt(f.Panel(60, 2), 1); got != "" {
		t.Errorf("Panel explanation while inert = %q, want nothing -- the row already carries the reason", got)
	}
}

// TestAgentField_RowAndPanelVocabulary pins v2 spec §6's agent row (the
// kind, nothing else) and the panel that now carries both the favorites
// and the full list.
func TestAgentField_RowAndPanelVocabulary(t *testing.T) {
	palette := theme.Default()

	fresh := NewAgentField(palette)
	if got := rowText(fresh.Row(40)); got != agentRowUnset {
		t.Errorf("Row before SetKinds = %q, want %q", got, agentRowUnset)
	}
	if got := panelLineAt(fresh.Panel(40, 2), 1); got != agentPanelEmpty {
		t.Errorf("Panel with no kinds = %q, want %q (the field's own words, never a bare \"no matches\")", got, agentPanelEmpty)
	}

	f := NewAgentField(palette)
	f.SetKinds([]string{"claude", "codex", "aider", "goose", "amp"})
	if got := rowText(f.Row(40)); got != "claude" {
		t.Errorf("Row = %q, want %q", got, "claude")
	}

	// Both halves of the panel are visible at once, with no expansion
	// gesture in between -- the whole reason "more…" moves out of the
	// chip row in v2.
	panel := ansi.Strip(f.Panel(60, f.PanelRows()))
	for _, kind := range []string{"claude", "goose", "amp"} {
		if !strings.Contains(panel, kind) {
			t.Errorf("Panel does not list %q:\n%s", kind, panel)
		}
	}

	f.SetKind("goose")
	if got := rowText(f.Row(40)); got != "goose" {
		t.Errorf("Row after SetKind(\"goose\") = %q, want %q", got, "goose")
	}
}

// TestAccountField_RowVocabulary pins v2 spec §6's account row in each of
// its states, including the colored state words that replace v1's bare
// "!" marker.
func TestAccountField_RowVocabulary(t *testing.T) {
	palette := theme.Default()

	inert := NewAccountField(palette)
	inert.SetProfiles(sampleStatus())
	if got := rowText(inert.Row(60)); got != accountInertPlaceholder {
		t.Errorf("Row while the agent kind is not claude = %q, want %q", got, accountInertPlaceholder)
	}

	f := NewAccountField(palette)
	f.SetAgentIsClaude(true)
	f.SetProfiles(sampleStatus())

	// Unpinned: the LIVE profile's tier and utilization, which is exactly
	// what v1's SetProfiles discarded along with status.ActiveProfile.
	if got, want := rowText(f.Row(60)), "active · Team · 12%"; got != want {
		t.Errorf("Row with nothing pinned = %q, want %q", got, want)
	}

	f.SetPin("alpha")
	if got, want := rowText(f.Row(60)), "alpha · Team · ok"; got != want {
		t.Errorf("Row pinned to a healthy profile = %q, want %q", got, want)
	}

	f.SetPin("gamma") // 5h window at 100%
	if got, want := rowText(f.Row(60)), "gamma · Team · 100%"; got != want {
		t.Errorf("Row pinned to a rate-limited profile = %q, want %q", got, want)
	}
	if !strings.Contains(f.Row(60), ansiColor(palette.Warning)) {
		t.Errorf("a rate-limited row does not carry the warning color; v2 spec §6 puts the percentage in it")
	}

	f.SetPin("beta") // auth_status "expired"
	if got, want := rowText(f.Row(60)), "beta · Max 20x · sign in again"; got != want {
		t.Errorf("Row pinned to an auth-failed profile = %q, want %q", got, want)
	}
	if !strings.Contains(f.Row(60), ansiColor(palette.Danger)) {
		t.Errorf("an auth-failed row does not carry the danger color; v2 spec §6 puts the state word in it")
	}
}

// TestAccountField_RowDegradesToNamesOnly pins spec §11's "schema != 1 ->
// degrade to name-only entries" on the row, where v1 only ever applied it
// to the picker's items.
func TestAccountField_RowDegradesToNamesOnly(t *testing.T) {
	status := sampleStatus()
	status.Schema = 7
	status.Degraded = true

	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)
	f.SetProfiles(status)
	f.SetPin("alpha")

	if got := rowText(f.Row(60)); got != "alpha" {
		t.Errorf("Row on a degraded status = %q, want the name alone -- tier, auth status and windows are all unreliable", got)
	}
	if got := panelLineAt(f.Panel(60, f.PanelRows()), f.PanelRows()-1); got != accountDegradedHint {
		t.Errorf("Panel status line on a degraded status = %q, want %q", got, accountDegradedHint)
	}
}

// TestAccountField_RowWithNoThirdPart pins the separator arithmetic for a
// profile clauth reported with neither an auth status nor a usage window:
// the row has two parts, not two parts and a dangling " · ".
func TestAccountField_RowWithNoThirdPart(t *testing.T) {
	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)
	f.SetProfiles(clauth.Status{
		Schema:        1,
		ActiveProfile: "bare",
		Profiles:      []clauth.Profile{{Name: "bare", Tier: "Pro"}},
	})

	if got, want := rowText(f.Row(60)), "active · Pro"; got != want {
		t.Errorf("unpinned Row over a bare profile = %q, want %q", got, want)
	}
	f.SetPin("bare")
	if got, want := rowText(f.Row(60)), "bare · Pro"; got != want {
		t.Errorf("pinned Row over a bare profile = %q, want %q", got, want)
	}
}

// TestAccountField_RowWithoutAResolvableProfile pins the empty-feed case:
// clauth reported nothing (or nothing under the active profile's name),
// so there is no tier to print and the row says only who it would use.
func TestAccountField_RowWithoutAResolvableProfile(t *testing.T) {
	f := NewAccountField(theme.Default())
	f.SetAgentIsClaude(true)

	if got := rowText(f.Row(60)); got != accountRowActive {
		t.Errorf("Row with no profiles at all = %q, want %q", got, accountRowActive)
	}

	f.SetProfiles(clauth.Status{Schema: 1, ActiveProfile: "vanished"})
	if got := rowText(f.Row(60)); got != accountRowActive {
		t.Errorf("Row whose active profile is not in the feed = %q, want %q", got, accountRowActive)
	}
	if got := panelLineAt(f.Panel(60, f.PanelRows()), f.PanelRows()-1); got != accountPanelEmpty {
		t.Errorf("Panel status line with no profiles = %q, want %q", got, accountPanelEmpty)
	}
}

// ansiColor renders c's foreground SGR the way lipgloss emits it, so a
// test can assert a span was drawn in a specific palette color without
// reimplementing the escape grammar.
func ansiColor(c theme.Color) string {
	rendered := lipgloss.NewStyle().Foreground(c).Render("x")
	return rendered[:strings.Index(rendered, "x")]
}

// --- elision --------------------------------------------------------------

// TestKeepHeadKeepTail pins the two elision primitives directly: the
// result is exactly the width asked for, the marked end is the one that
// was cut, and the cut is never silent.
func TestKeepHeadKeepTail(t *testing.T) {
	const s = "abcdefghij" // 10 cells

	head := map[int]string{12: s, 10: s, 5: "abcd…", 2: "a…", 1: "…"}
	for w, want := range head {
		if got := keepHead(s, w); got != want {
			t.Errorf("keepHead(%q, %d) = %q, want %q", s, w, got, want)
		}
	}

	tail := map[int]string{12: s, 10: s, 5: "…ghij", 2: "…j", 1: "…"}
	for w, want := range tail {
		if got := keepTail(s, w); got != want {
			t.Errorf("keepTail(%q, %d) = %q, want %q", s, w, got, want)
		}
	}

	for w := 1; w <= 12; w++ {
		if got := ansi.StringWidth(keepHead(s, w)); got != min(w, 10) {
			t.Errorf("keepHead(%q, %d) is %d cells wide, want %d", s, w, got, min(w, 10))
		}
		if got := ansi.StringWidth(keepTail(s, w)); got != min(w, 10) {
			t.Errorf("keepTail(%q, %d) is %d cells wide, want %d", s, w, got, min(w, 10))
		}
	}
}

// TestFieldRow_ElidesTowardTheInformativeEnd pins v2 spec §7's rule as
// each field applies it: a PATH keeps its tail (the segments that tell
// two candidates apart), while a title, an issue and a prompt keep their
// head (what you read first).
func TestFieldRow_ElidesTowardTheInformativeEnd(t *testing.T) {
	palette := theme.Default()

	d := NewDirField(palette)
	d.SetHomeDir("/home/zvi")
	d.SetCandidates(1, []string{"/home/zvi/Projects/deeply/nested/herdr-draft"})
	got := rowText(d.Row(20))
	if !strings.HasPrefix(got, rowEllipsis) {
		t.Errorf("a clipped project row = %q, want it to start with %q -- a path keeps its TAIL", got, rowEllipsis)
	}
	if !strings.HasSuffix(got, "herdr-draft") {
		t.Errorf("a clipped project row = %q, want it to end in the last segment", got)
	}

	title := NewTitleField(palette)
	title.SetTitle("fix the login redirect loop on cold start", false)
	got = rowText(title.Row(20))
	if !strings.HasSuffix(got, rowEllipsis) {
		t.Errorf("a clipped title row = %q, want it to end with %q -- a title keeps its HEAD", got, rowEllipsis)
	}
	if !strings.HasPrefix(got, "fix the login") {
		t.Errorf("a clipped title row = %q, want it to start with the title's first words", got)
	}

	issue := NewIssueField(palette)
	issue.SetIssues(1, []linear.Issue{{Identifier: "ENG-101", Title: "fix login redirect loop"}})
	issue.Update(key(tea.KeyDown, 0))
	got = rowText(issue.Row(20))
	if !strings.HasPrefix(got, "ENG-101") || !strings.HasSuffix(got, rowEllipsis) {
		t.Errorf("a clipped issue row = %q, want the identifier kept and the title marked as cut", got)
	}
}

// TestPromptField_RowKeepsItsCountWhenElided pins the ordering inside the
// prompt row: the " +N more" suffix is paid for BEFORE the first line is
// clipped, so the count is never what gets cut. Losing it would turn a
// summary of a long prompt into what reads as the whole prompt.
func TestPromptField_RowKeepsItsCountWhenElided(t *testing.T) {
	f := NewPromptField(theme.Default())
	f.SetValue("a very long opening line that will not fit\nsecond\nthird\nfourth", false)

	got := rowText(f.Row(24))
	if !strings.HasSuffix(got, "+3 more") {
		t.Errorf("a clipped prompt row = %q, want it to still end in %q", got, "+3 more")
	}
	if !strings.Contains(got, rowEllipsis) {
		t.Errorf("a clipped prompt row = %q, want the cut marked with %q", got, rowEllipsis)
	}
}
