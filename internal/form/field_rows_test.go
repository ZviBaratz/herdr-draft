package form

import (
	"strconv"
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

// panelStatusMessage returns the MESSAGE half of a panel's status line:
// everything left of the filter count v3 spec §8.5 right-flushes onto it.
//
// count is the caller's own expectation for that readout rather than a
// pattern, so a wrong count fails the message assertion too, by staying
// in the string. Pass "" for a field that shows none.
func panelStatusMessage(line, count string) string {
	return strings.TrimRight(strings.TrimSuffix(line, count), " ")
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
	account.SetProfiles(sampleStatus(), sampleNow())

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
	// affords the full chrome AND v3 spec §7's top margin. Both show the
	// whole stack, so no scrolling (stackWindow) can explain a difference.
	//
	// The two stack offsets are DERIVED from the frames rather than
	// written down, because they are precisely what differs between the
	// two heights -- and this test's whole job is that the row BYTES do
	// not.
	const short, tall = 11, 60
	shortFrame, tallFrame := layoutFrame(short, len(fields)), layoutFrame(tall, len(fields))
	if shortFrame.Header || shortFrame.Rule1 || !tallFrame.Header || !tallFrame.Rule1 {
		t.Fatalf("this test needs h=%d to drop the chrome and h=%d to keep it; got %+v and %+v",
			short, tall, shortFrame, tallFrame)
	}
	if shortFrame.Rows != len(fields) || tallFrame.Rows != len(fields) {
		t.Fatalf("both heights must show the whole stack; got %d and %d rows", shortFrame.Rows, tallFrame.Rows)
	}
	shortTop, tallTop := firstStackRow(shortFrame), firstStackRow(tallFrame)
	if shortTop == tallTop {
		t.Fatalf("the two heights must put the stack at DIFFERENT offsets for this to test anything; both start at %d", shortTop)
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
		if atShort[shortTop+i] != atTall[tallTop+i] {
			t.Errorf("row %d (%s) rendered differently at h=%d and h=%d:\n short: %q\n  tall: %q",
				i, f.ID(), short, tall, rowText(atShort[shortTop+i]), rowText(atTall[tallTop+i]))
		}
		if atShort[shortTop+i] != againShort[shortTop+i] {
			t.Errorf("row %d (%s) did not survive a round trip through the taller render:\n  first: %q\n second: %q",
				i, f.ID(), rowText(atShort[shortTop+i]), rowText(againShort[shortTop+i]))
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

// --- PanelRows: the rows each field books, state by state ----------------

// panelRowsCase is one field in one state, paired with the number of
// panel rows that state is worth under the field's OWN rule.
//
// build constructs the field rather than the table carrying one already
// built, so a case that needs several setters (or a typed filter) reads
// as the sequence that produces the state, and no two cases can share a
// field and leak state into each other.
type panelRowsCase struct {
	name  string
	build func() Section
	want  int
}

// capOverflowItems is how many items a */over-the-cap case feeds its
// field, and it is a LITERAL on purpose -- deliberately not derived from
// the cap it is meant to overflow.
//
// Sizing the fixture as `cap+5` scales it with the constant, so the cap
// cases followed a retuned cap in silence: raising issuePanelMaxRows from
// 24 to 50 kept the fixture overflowing and kept `want` equal to the new
// value, which is the same "expectation derived from the thing under
// test" shape the rest of this table exists to remove. A typo -- 240 for
// 24 -- would have passed. With a literal, a cap raised above it fails
// instead, and the over-the-cap guard in the test below says why rather
// than leaving the next reader to work out that the case went vacuous.
//
// 40 clears every cap in the package by a wide margin (the largest is
// issuePanelMaxRows at 24). Raising a cap past it is what the guard
// catches.
const capOverflowItems = 40

// panelRowsCases enumerates every branch the eight fields' PanelRows()
// implementations have, with each want spelled out as ARITHMETIC OVER
// THE FIXTURE from that field's own documented rule -- never read back
// off the field, and never through Panel.
//
// That independence is the whole substance of #33. The assertion this
// table feeds used to compare PanelRows() against the line count of
// Panel(80, PanelRows()), and every Panel ends in panelBlock
// (rowvalues.go), which pads a short block and truncates a long one to
// exactly the height it is handed -- so the comparison held however
// wrong PanelRows() was, in a test named for the opposite.
//
// A per-state ROW COUNT is the only thing available to compare against.
// Counting a Panel's real content was the other candidate fix and does
// not work, and the reason is that panelBlock is the SECOND of two
// padding stages: seven of the eight fields size their content FROM the
// height they are handed before it ever runs -- panelPickerLines fills
// to h, PromptField.Panel calls area.SetRows(h), IssueField.Panel pads
// to h-1, and TitleField, DirField and WorktreeField each take h minus
// their own fixed lines. So an accessor that skipped only panelBlock
// would be the same tautology one layer down, for seven of eight.
//
// PlacementField is the exception, and not because of anything about
// placement: its panel is the only one with no windowed list and no
// textarea, so nothing in it needs to know h. Any future field of that
// shape would be equally open to the direct approach the precedent
// beside this table takes (field_placement_test.go), and any field with
// a picker in it would not.
//
// Restating eight small formulas here duplicates arithmetic,
// deliberately. The duplication is what makes a disagreement fail, and
// it fails LOUDLY: changing a PanelRows() without changing its rows here
// is a red test, not a green one.
//
// A COMPOUND condition needs a case per side, and this is the one gap the
// coverage guard below cannot see -- it guards which FIELDS are listed,
// not which branches. An independent review of the first version of this
// table found both instances: worktree/non-git was the table's only
// non-git case and had the toggle off too, so `!w.isGitRepo || !w.On()`
// was satisfied by the second disjunct and dropping the first changed
// nothing anywhere in the tree; and placement had no worktree-off case
// with a non-default chip, so `f.worktreeOn && ID != "new"` was decided
// by the second conjunct alone. worktree/non-git-while-on and
// placement/off-other-chip exist for those two sides and nothing else.
func panelRowsCases(p theme.Palette) []panelRowsCase {
	// chipsRight advances a chip row n places from its fresh selection,
	// through the same key path a user would.
	//
	// SetValue would do it in one call. The keypresses are used because
	// the sibling matrix in field_placement_test.go moves the chip this
	// way, and two tests over one field's arithmetic that disagree about
	// how the field arrives at a state are two tests whose disagreement
	// is the first thing to rule out when one of them fails.
	//
	// Returns nothing, deliberately: a helper that both mutates and
	// returns its argument invites the two spellings this table had, one
	// case returning the call and the next discarding it.
	chipsRight := func(s Section, n int) {
		for i := 0; i < n; i++ {
			s.Update(key(tea.KeyRight, 0))
		}
	}
	// countedNames is n distinct one-word items, for the cases whose only
	// job is to overflow a cap.
	countedNames := func(prefix string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = prefix + strconv.Itoa(i)
		}
		return out
	}
	// textLines is a prompt value of exactly n lines.
	textLines := func(n int) string {
		return strings.TrimSuffix(strings.Repeat("x\n", n), "\n")
	}

	return []panelRowsCase{
		// issue: one row per offered issue -- the `none` sentinel
		// included, since it is a row you can pick -- plus the status
		// line, capped at issuePanelMaxRows. An inert field draws no
		// list at all, so it wants the status line alone.
		{"issue/offered", func() Section {
			f := NewIssueField(p)
			f.SetIssues(1, sampleIssues())
			return f
		}, len(sampleIssues()) + 1 + 1},
		{"issue/filtered", func() Section {
			f := NewIssueField(p)
			f.SetIssues(1, sampleIssues())
			// Focus first: the filter is a bubbles textinput, which
			// ignores every keypress until it has focus, so an unfocused
			// field would silently stay unfiltered and this case would
			// duplicate issue/offered.
			f.Focus()
			// "dark" keeps ENG-2 ("Add dark mode") and nothing else --
			// not even the `none` row, which is filtered like any other.
			for _, r := range "dark" {
				f.Update(rn(r))
			}
			return f
		}, 1 + 1},
		{"issue/unavailable", func() Section {
			f := NewIssueField(p)
			f.SetUnavailable("no API key")
			return f
		}, 1},
		{"issue/over-the-cap", func() Section {
			f := NewIssueField(p)
			issues := make([]linear.Issue, 0, capOverflowItems)
			for _, id := range countedNames("ENG-", capOverflowItems) {
				issues = append(issues, linear.Issue{Identifier: id, Title: "a queued issue"})
			}
			f.SetIssues(1, issues)
			return f
		}, issuePanelMaxRows},

		// title: the verdict line alone until the app pushes a session
		// list in, then a blank, the heading, and one row per session,
		// the list capped at titleSessionsMaxRows.
		{"title/no-sessions", func() Section { return NewTitleField(p) }, 1},
		{"title/sessions", func() Section {
			f := NewTitleField(p)
			f.SetSessions(sampleSessions())
			return f
		}, 3 + len(sampleSessions())},
		{"title/over-the-cap", func() Section {
			f := NewTitleField(p)
			sessions := make([]Session, 0, capOverflowItems)
			for _, label := range countedNames("ws-", capOverflowItems) {
				sessions = append(sessions, Session{Label: label, Status: "idle", Panes: 1})
			}
			f.SetSessions(sessions)
			return f
		}, 3 + titleSessionsMaxRows},

		// prompt: one row per line of text plus one to type the next line
		// into, floored at promptPanelMinRows and capped at
		// promptPanelMaxRows.
		{"prompt/empty", func() Section { return NewPromptField(p) }, promptPanelMinRows},
		{"prompt/under-the-floor", func() Section {
			f := NewPromptField(p)
			f.SetValue(textLines(3), false)
			return f
		}, promptPanelMinRows},
		{"prompt/growing", func() Section {
			f := NewPromptField(p)
			f.SetValue(textLines(8), false)
			return f
		}, 8 + 1},
		{"prompt/over-the-cap", func() Section {
			f := NewPromptField(p)
			f.SetValue(textLines(capOverflowItems), false)
			return f
		}, promptPanelMaxRows},

		// dir: one row per candidate, one per repo-config note, plus the
		// status line, capped at dirPanelMaxRows.
		{"dir/candidates", func() Section {
			d := NewDirField(p)
			d.SetCandidates(1, []string{"/home/zvi/Projects/herdr-draft", "/home/zvi/Projects/herdr"})
			return d
		}, 2 + 1},
		{"dir/candidates-and-notes", func() Section {
			d := NewDirField(p)
			d.SetCandidates(1, []string{"/home/zvi/Projects/herdr-draft", "/home/zvi/Projects/herdr"})
			d.SetNotes([]string{"ignoring agents.extra_args", "ignoring clauth"})
			return d
		}, 2 + 2 + 1},
		{"dir/over-the-cap", func() Section {
			d := NewDirField(p)
			d.SetCandidates(1, countedNames("/home/zvi/p", capOverflowItems))
			return d
		}, dirPanelMaxRows},

		// placement: the chip row and its explanation, plus the
		// disclosure line a worktree adds for the two non-default chips,
		// plus the provenance line when a config file chose the value
		// (placement spec §6.1). field_placement_test.go's own matrix
		// crosses all three axes; these five are the branches, so the
		// coverage guard below has something to find.
		{"placement/off", func() Section { return NewPlacementField(p) }, 2},
		{"placement/off-with-provenance", func() Section {
			f := NewPlacementField(p)
			f.SetProvenance(".herdr-draft.toml")
			return f
		}, 2 + 1},
		{"placement/off-other-chip", func() Section {
			f := NewPlacementField(p)
			chipsRight(f, 1) // tab here, no worktree
			return f
		}, 2},
		{"placement/on-default-chip", func() Section {
			f := NewPlacementField(p)
			f.SetWorktreeOn(true)
			return f
		}, 2},
		{"placement/on-other-chip", func() Section {
			f := NewPlacementField(p)
			f.SetWorktreeOn(true)
			chipsRight(f, 1) // tab here
			return f
		}, 2 + 1},
		{"placement/on-other-chip-with-provenance", func() Section {
			f := NewPlacementField(p)
			f.SetWorktreeOn(true)
			chipsRight(f, 1)
			f.SetProvenance(".herdr-draft.toml")
			return f
		}, 2 + 1 + 1},

		// agent: the favorites chip row plus one line per known kind,
		// capped at agentPanelMaxRows. With no kinds at all it still
		// wants two, so agentPanelEmpty has somewhere to land.
		{"agent/no-kinds", func() Section { return NewAgentField(p) }, 2},
		{"agent/kinds", func() Section {
			f := NewAgentField(p)
			f.SetKinds([]string{"claude", "codex", "aider", "goose"})
			return f
		}, 1 + 4},
		{"agent/over-the-cap", func() Section {
			f := NewAgentField(p)
			f.SetKinds(countedNames("kind", capOverflowItems))
			return f
		}, agentPanelMaxRows},

		// account: the `active` row, one row per profile, and the status
		// line, capped at accountPanelMaxRows. PanelRows does not branch
		// on the agent kind -- an inert account panel still books its
		// rows, because the row above it is what says it is inert.
		{"account/no-profiles", func() Section {
			f := NewAccountField(p)
			f.SetAgentIsClaude(true)
			return f
		}, 2},
		{"account/profiles", func() Section {
			f := NewAccountField(p)
			f.SetAgentIsClaude(true)
			f.SetProfiles(sampleStatus(), sampleNow())
			return f
		}, 2 + len(sampleStatus().Profiles)},
		{"account/over-the-cap", func() Section {
			f := NewAccountField(p)
			f.SetAgentIsClaude(true)
			status := clauth.Status{Schema: 1, ActiveProfile: "p0"}
			for _, name := range countedNames("p", capOverflowItems) {
				status.Profiles = append(status.Profiles, clauth.Profile{Name: name, Tier: "Team", AuthStatus: "ok"})
			}
			f.SetProfiles(status, sampleNow())
			return f
		}, accountPanelMaxRows},

		// worktree: the three parts, the provenance line when a config
		// file supplied a value, and -- only while a worktree is
		// actually on -- one row per base candidate, the HEAD sentinel
		// included, capped at worktreePanelMaxRows. An off or non-git
		// field reserves no list rows: there is no list to show.
		{"worktree/non-git", func() Section { return NewWorktreeField(p) }, worktreePanelParts},
		{"worktree/non-git-while-on", func() Section {
			w := NewWorktreeField(p)
			w.SetGitTarget(true)
			w.SetOn(true)
			w.SetBaseItems(1, []string{"main", "release/1.4"})
			// The project then switched to a non-repository, which is how
			// this state arrives in production: async.go pushes git-ness
			// from the debounced dir check, and SetGitTarget(false) does
			// not move the toggle. The field goes inert, but focus does
			// not leave it (focus.go's focusByID moves "regardless of
			// that section's Enabled() state", and nothing ejects focus
			// from a section that goes disabled), so its panel is still
			// rendered from these rows.
			w.SetGitTarget(false)
			return w
		}, worktreePanelParts},
		{"worktree/off", func() Section {
			w := NewWorktreeField(p)
			w.SetGitTarget(true)
			w.SetBaseItems(1, []string{"main", "release/1.4"})
			return w
		}, worktreePanelParts},
		{"worktree/off-with-provenance", func() Section {
			w := NewWorktreeField(p)
			w.SetGitTarget(true)
			w.SetProvenance(".herdr-draft.toml")
			return w
		}, worktreePanelParts + 1},
		{"worktree/on", func() Section {
			w := NewWorktreeField(p)
			w.SetGitTarget(true)
			w.SetOn(true)
			w.SetBaseItems(1, []string{"main", "release/1.4"})
			return w
		}, worktreePanelParts + 1 + 2},
		{"worktree/on-with-provenance", func() Section {
			w := NewWorktreeField(p)
			w.SetGitTarget(true)
			w.SetOn(true)
			w.SetBaseItems(1, []string{"main", "release/1.4"})
			w.SetProvenance(".herdr-draft.toml")
			return w
		}, worktreePanelParts + 1 + 1 + 2},
		{"worktree/over-the-cap", func() Section {
			w := NewWorktreeField(p)
			w.SetGitTarget(true)
			w.SetOn(true)
			w.SetBaseItems(1, countedNames("release/", capOverflowItems))
			return w
		}, worktreePanelMaxRows},
	}
}

// TestFieldPanelCeilings_AreTheNumbersThatWereChosen pins the nine
// bounds panelRowsCases' expectations are written in terms of.
//
// It exists because those expectations are SYMBOLIC -- issue/over-the-cap
// wants issuePanelMaxRows, not 24 -- which is the right shape for the
// cases (their job is "the cap is applied here, in this state", and a
// literal in each would scatter one deliberate retune across seven
// unrelated places) and leaves exactly one thing unpinned: the constant's
// own VALUE. Both sides read the same symbol, so raising
// issuePanelMaxRows from 24 to 30 kept the whole tree green. Five of
// these nine were unpinned by any assertion in the package; the other
// four already failed something, by accident rather than design.
//
// Splitting the two claims is what makes each one honest. The cases say
// the cap is applied; this table says which number it is. A retune then
// shows up in review as one line saying the issue panel's ceiling went
// from 24 to 50, which is the sentence a reviewer wants to read, in the
// one place whose whole purpose is to be edited deliberately.
//
// The `why` column is each constant's OWN stated reason, quoted from its
// doc comment rather than restated -- if the two ever disagree, the doc
// comment is right and this table is stale.
func TestFieldPanelCeilings_AreTheNumbersThatWereChosen(t *testing.T) {
	for _, c := range []struct {
		name string
		got  int
		want int
		why  string
	}{
		{"issuePanelMaxRows", issuePanelMaxRows, 24,
			"spec §10 fetches up to 50 issues, far more than a panel should claim from the rest of the form"},
		{"agentPanelMaxRows", agentPanelMaxRows, 10,
			"spec §6 sizes the full kind list at the known 23 kinds"},
		{"accountPanelMaxRows", accountPanelMaxRows, 8,
			"a clauth profile set is small"},
		{"dirPanelMaxRows", dirPanelMaxRows, 12,
			"a project list can be long"},
		{"promptPanelMinRows", promptPanelMinRows, 6,
			"enough rows to be worth focusing even for a one-line prompt"},
		{"promptPanelMaxRows", promptPanelMaxRows, 20,
			"a very tall window must not hand a 40-row textarea to a field most sessions leave empty"},
		{"titleSessionsMaxRows", titleSessionsMaxRows, 15,
			"panelCapRows: the region cannot show more than that anyway (v3 spec §7.2), so a larger number would make PanelRows lie about a height it can never be given"},
		{"worktreePanelParts", worktreePanelParts, 3,
			"the chips, the branch and the base selection, one line each"},
		{"worktreePanelMaxRows", worktreePanelMaxRows, 10,
			"a repository can have hundreds of branches; three parts plus a usable window onto the list"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d -- %s.\nIf this change is deliberate, edit the literal here and say why in the commit; a retune is meant to be visible in review, which is the whole point of this table.",
				c.name, c.got, c.want, c.why)
		}
	}

	// titleSessionsMaxRows is an ALIAS, so pin the coupling as well as
	// the value. Replacing `= panelCapRows` with a literal 15 is
	// undetectable here and harmless today -- the two numbers agree --
	// and this assertion is deliberately not trying to catch it. What it
	// catches is the moment that stops being harmless: panelCapRows
	// moving while title does not follow, which is when the lost coupling
	// becomes a PanelRows() that lies about a height the region can never
	// give (v3 spec §7.2).
	if titleSessionsMaxRows != panelCapRows {
		t.Errorf("titleSessionsMaxRows = %d but panelCapRows = %d; the alias is what makes PanelRows honest about a height the region can actually give (v3 spec §7.2)",
			titleSessionsMaxRows, panelCapRows)
	}
}

// TestFieldPanelRows_NeverReservesRowsItCannotFill pins the other half of
// the panel contract: PanelRows is "the greatest number of rows this
// field can put to GOOD USE" -- at least one, and exactly the number the
// field's own rule says its current state is worth (panelRowsCases).
//
// #33: the second assertion used to be a shape check,
// len(split(Panel(80, PanelRows()))) == PanelRows(), which cannot fail --
// panelBlock pads and truncates to the height it is handed. It is gone
// rather than repaired, because Panel's line count is already pinned
// where that contract belongs, by TestFieldPanel_IsAlwaysExactlyHLines
// above, over three widths crossed with seven heights. The floor check
// below it was always real and stays: 0 is a meaningful answer from
// Section.PanelRows ("no panel at all", which the Create section gives)
// and a meaningless one from a field with a chooser in it.
//
// An EQUALITY, because both directions of a wrong count are defects and
// the name only warns about one. Over-reporting is the named one, and it
// is visible rather than merely wasteful: the extra row is padded in by
// the FIELD, so it lands wherever that field's own composition puts it
// -- injecting it into IssueField.PanelRows() opens a blank line between
// the last issue and the status line panelStatusLine pins last, which
// moved three committed frames. Under-reporting is the direction no
// frame is likely to hold, and it loses content rather than adding
// blanks: the form hands Panel min(PanelRows(), Region) (form.go's
// renderPanelRegion), so a field asking for too little is rendered at
// too little and drops what the region had room for -- worktree's
// provenance line below worktreePanelParts, placement's disclosure --
// truncated from the bottom by the very panelBlock call that used to
// hide all of this.
func TestFieldPanelRows_NeverReservesRowsItCannotFill(t *testing.T) {
	palette := theme.Default()
	covered := map[string]bool{}

	for _, c := range panelRowsCases(palette) {
		s := c.build()
		covered[s.ID()] = true

		got := s.PanelRows()
		if got < 1 {
			t.Errorf("%s: %s.PanelRows() = %d, want at least 1 (0 means 'no panel at all', which none of these fields is)", c.name, s.ID(), got)
			continue
		}
		if got != c.want {
			t.Errorf("%s: %s.PanelRows() = %d, want %d", c.name, s.ID(), got, c.want)
		}
		// A cap case proves nothing once its cap rises above the fixture
		// feeding it -- see capOverflowItems. This says so out loud
		// instead of passing vacuously.
		if strings.HasSuffix(c.name, "/over-the-cap") && c.want >= capOverflowItems {
			t.Errorf("%s: the cap is %d but the fixture feeds only %d items, so this case no longer proves the cap binds -- raise capOverflowItems",
				c.name, c.want, capOverflowItems)
		}
	}

	// Every row field must state the rows it books, or this test is back
	// to covering eight fields in name and none in fact -- which is the
	// state #33 found it in.
	//
	// Note what this reaches: the two FIXTURES, which are hand-maintained
	// and which nothing derives from the real form. A ninth field added
	// to internal/app's own sections slice and left out of rowFields
	// would escape this guard entirely -- it is caught instead by
	// internal/app's TestNew_SectionOrder, which pins the real ID
	// sequence. So the two together are what make a ninth field
	// impossible to add silently, and neither is sufficient alone. That
	// test's failure message names this table, so the chain reads in the
	// direction someone hitting it travels.
	for _, s := range append(rowFields(palette), repoConfigFields(palette)...) {
		if !covered[s.ID()] {
			t.Errorf("no panelRowsCases entry for the %q field: every row field states the rows its PanelRows() books, per state", s.ID())
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

	// placement spec §6.1: the field stopped going inert under a worktree.
	// The loop above left the cursor on the last entry in want, "split
	// here" -- SetWorktreeOn(true) must change neither the row (still the
	// chip's own label) nor the selection, only the panel's explanation
	// (which switches to placementWorktreeHint's worktree-on wording for
	// the SAME chip).
	f.SetWorktreeOn(true)
	if got := rowText(f.Row(60)); got != "split here" {
		t.Errorf("Row while a worktree is on = %q, want %q (unchanged -- the row states only the chip label)", got, "split here")
	}
	if got, want := panelLineAt(f.Panel(60, 2), 1), placementWorktreeHint("split-here"); got != want {
		t.Errorf("Panel explanation while a worktree is on = %q, want %q", got, want)
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

// TestAccountField_RowVocabulary pins v3 spec §10.1's account row in each
// of its states.
//
// The v2 row this replaces read `personal · Max 20x · ok`, because
// accountRowState showed the literal AuthStatus for a PINNED profile and
// the utilization only for an unpinned one. §10.1 calls that backwards
// and says why in five words -- **ok is the state that needs no words**.
// So both windows are shown either way, and the auth status appears only
// when it is not "ok".
func TestAccountField_RowVocabulary(t *testing.T) {
	palette := theme.Default()

	inert := NewAccountField(palette)
	inert.SetProfiles(sampleStatus(), sampleNow())
	if got := rowText(inert.Row(60)); got != accountInertPlaceholder {
		t.Errorf("Row while the agent kind is not claude = %q, want %q", got, accountInertPlaceholder)
	}

	f := NewAccountField(palette)
	f.SetAgentIsClaude(true)
	f.SetProfiles(sampleStatus(), sampleNow())

	// Unpinned: the LIVE profile's plan and BOTH windows, which is what
	// the author actually asked the row for. v2 discarded the second
	// window in a single constant nobody remembered.
	if got, want := rowText(f.Row(60)), "active · Team · 5h 12% · 7d 40%"; got != want {
		t.Errorf("Row with nothing pinned = %q, want %q", got, want)
	}

	f.SetPin("alpha")
	if got, want := rowText(f.Row(60)), "alpha · Team · 5h 12% · 7d 40%"; got != want {
		t.Errorf("Row pinned to a healthy profile = %q, want %q -- `ok` is the state that needs no words", got, want)
	}

	// gamma reports only a 5h window, at 100%: the missing 7d part is
	// simply absent rather than rendered as an em dash, and its separator
	// goes with it.
	f.SetPin("gamma")
	if got, want := rowText(f.Row(60)), "gamma · Team · 5h 100%"; got != want {
		t.Errorf("Row pinned to a rate-limited profile = %q, want %q", got, want)
	}
	if !strings.Contains(f.Row(60), ansiColor(palette.Warning)) {
		t.Errorf("a rate-limited row does not carry the warning color; §10.1 puts the percentage in it")
	}

	f.SetPin("beta") // auth_status "expired"
	if got, want := rowText(f.Row(60)), "beta · Max 20x · 5h 0% · sign in again"; got != want {
		t.Errorf("Row pinned to an auth-failed profile = %q, want %q", got, want)
	}
	if !strings.Contains(f.Row(60), ansiColor(palette.Danger)) {
		t.Errorf("an auth-failed row does not carry the danger color; §10.1 puts the state word in it")
	}
}

// TestAccountField_RowWarnsAtNinetyFive is the threshold change of v3
// spec §10.2, and the number it uses is the one that shipped broken: at
// v2's >= 100 a profile sitting at 98% -- a real, observed live value --
// warned nowhere, in either surface, and read exactly like one sitting
// at 3%. 95 is clauth's own default auto-switch trip point.
func TestAccountField_RowWarnsAtNinetyFive(t *testing.T) {
	palette := theme.Default()
	warned := ansiColor(palette.Warning)

	for _, c := range []struct {
		pct  float64
		warn bool
	}{{3, false}, {94, false}, {95, true}, {98, true}, {100, true}} {
		f := NewAccountField(palette)
		f.SetAgentIsClaude(true)
		f.SetProfiles(clauth.Status{
			Schema:        1,
			ActiveProfile: "solo",
			Profiles: []clauth.Profile{{
				Name: "solo", Tier: "Team", AuthStatus: "ok",
				Windows: []clauth.Window{{Label: "5h", UtilizationPct: c.pct}},
			}},
		}, sampleNow())

		if got := strings.Contains(f.Row(60), warned); got != c.warn {
			t.Errorf("at %.0f%% the row carries the warning color = %v, want %v (threshold is %.0f)",
				c.pct, got, c.warn, accountWarnThreshold)
		}
		// The panel's own badge must fire on the same number; v2 had the
		// two disagree, the marker at >= 100 and nothing else anywhere.
		badge := strings.Contains(ansi.Strip(f.Panel(80, f.PanelRows())), accountWarnRateLimited)
		if badge != c.warn {
			t.Errorf("at %.0f%% the panel badge says rate limited = %v, want %v -- it must fire on the same threshold as the row",
				c.pct, badge, c.warn)
		}
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
	f.SetProfiles(status, sampleNow())
	f.SetPin("alpha")

	if got := rowText(f.Row(60)); got != "alpha" {
		t.Errorf("Row on a degraded status = %q, want the name alone -- tier, auth status and windows are all unreliable", got)
	}
	line := panelLineAt(f.Panel(60, f.PanelRows()), f.PanelRows()-1)
	if got := panelStatusMessage(line, "3 profiles"); got != accountDegradedHint {
		t.Errorf("Panel status line on a degraded status = %q, want %q plus the count", got, accountDegradedHint)
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
	}, sampleNow())

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

	f.SetProfiles(clauth.Status{Schema: 1, ActiveProfile: "vanished"}, sampleNow())
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

// ansiBackground is ansiColor's other half, for asserting that a line was
// (or was not) painted on a specific palette background.
func ansiBackground(c theme.Color) string {
	rendered := lipgloss.NewStyle().Background(c).Render("x")
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
