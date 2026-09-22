package form

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// TestDisplayText pins the rule external text is drawn by (#151): a tab
// and each CR or LF become a space, every other control character and
// U+FFFD are dropped, and everything else is kept.
func TestDisplayText(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"plain is untouched", "fix login redirect loop", "fix login redirect loop"},
		{"a CR is a space, so it cannot return to the start of the row", "fix\rOVERWRITE login loop", "fix OVERWRITE login loop"},
		{"a tab and a line break are a space each", "one\ttwo\nthree", "one two three"},
		{"CR and LF are a space each", "one\r\ntwo", "one  two"},
		{"ESC is dropped and its sequence's text kept", "fix\x1b[2Jlogin", "fix[2Jlogin"},
		{"BEL is dropped", "fix\alogin", "fixlogin"},
		{"DEL is dropped", "fix\x7flogin", "fixlogin"},
		{"a C1 control is dropped, NEL and CSI included", "fix\u0085\u009blogin", "fixlogin"},
		{"an invalid UTF-8 byte is dropped", "fix\xfflogin", "fixlogin"},
		{"a literal U+FFFD is dropped too", "fix\ufffdlogin", "fixlogin"},
		{"non-ASCII is kept", "Fix café login", "Fix café login"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DisplayText(tc.in); got != tc.want {
				t.Errorf("DisplayText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestDisplayText_AgreesWithTheTitleRowForEveryRune holds the rule to what
// its doc comment claims for all of Unicode: no control character comes
// out, and what does come out is what plan.SanitizeTitle keeps, so an
// issue's title reads the same in the issue row and the title row. The
// second half is a statement about today's bubbles; if an upgrade moves
// SanitizeTitle, it is the first half that must still hold.
func TestDisplayText_AgreesWithTheTitleRowForEveryRune(t *testing.T) {
	var b strings.Builder
	for r := rune(0); r <= utf8.MaxRune; r++ {
		b.WriteRune(r)
	}
	all := b.String()
	got := DisplayText(all)
	for _, r := range got {
		if unicode.IsControl(r) || r == utf8.RuneError {
			t.Fatalf("DisplayText kept %U", r)
		}
	}
	if got != plan.SanitizeTitle(all) {
		t.Error("DisplayText and plan.SanitizeTitle disagree about some rune")
	}
}

// TestExternalTextIsDrawnWithoutControlCharacters is #151 at every place
// this package draws text it did not write and the user did not type.
// Each case feeds a CR, and most an ESC sequence or a BEL, through the
// setter the app layer uses, and reads the RAW render: ansi.Strip would
// remove an escape sequence whole, which is exactly what hides one.
//
// A CR is the case #151 names. lipgloss counts it as no cells, so the
// row measured right, and the terminal went back to the start of the line
// and drew the rest over the row's label: "fix\rOVERWRITE login loop"
// rendered as "OVERWRITE login loop fix".
func TestExternalTextIsDrawnWithoutControlCharacters(t *testing.T) {
	for _, tc := range []struct {
		name   string
		render func() string
		// want is what the reader should see: each part, cleaned,
		// somewhere in the render.
		want []string
	}{
		{
			name: "the issue picker's identifier, title and state",
			render: func() string {
				f := NewIssueField(theme.Default())
				f.SetIssues(1, []linear.Issue{{Identifier: "ENG\a-1", Title: "fix\rOVERWRITE login loop", StateName: "To\x1b[2Jdo"}})
				return f.Panel(80, f.PanelRows())
			},
			want: []string{"ENG-1", "fix OVERWRITE login loop", "To[2Jdo"},
		},
		{
			name: "the chosen issue on its row",
			render: func() string {
				f := NewIssueField(theme.Default())
				f.SetIssues(1, []linear.Issue{{Identifier: "ENG\a-1", Title: "fix\rOVERWRITE login loop"}})
				f.Update(key(tea.KeyDown, 0))
				return f.Row(80)
			},
			want: []string{"ENG-1 · fix OVERWRITE login loop"},
		},
		{
			name: "a workspace's label, status and repository in the title panel's session list",
			render: func() string {
				f := NewTitleField(theme.Default())
				f.SetSessions([]Session{{Label: "fix\rOVERWRITE", Status: "id\x1b[2Jle", Panes: 1, Repo: "herdr\a-draft"}})
				return f.Panel(101, f.PanelRows())
			},
			want: []string{"fix OVERWRITE", "id[2Jle", "herdr-draft"},
		},
		{
			name: "a workspace's label on the tab-in chip",
			render: func() string {
				f := NewPlacementField(theme.Default())
				f.SetSpace("fix\rOVER\x1b[2JWRITE")
				f.SetValue(plan.PlacementTabIn)
				return f.Row(60) + "\n" + f.Panel(80, f.PanelRows())
			},
			want: []string{"tab in fix OVER[2JWRITE"},
		},
		{
			name: "a directory's name in the project picker",
			render: func() string {
				d := NewDirField(theme.Default())
				d.SetCandidates(1, []string{"/srv/fix\rOVER\x1b[2JWRITE"})
				return d.Panel(80, d.PanelRows())
			},
			want: []string{"/srv/fix OVER[2JWRITE"},
		},
		{
			name: "a directory's name on the project row",
			render: func() string {
				d := NewDirField(theme.Default())
				d.SetCandidates(1, []string{"/srv/fix\rOVER\x1b[2JWRITE"})
				return d.Row(80)
			},
			want: []string{"/srv/fix OVER[2JWRITE"},
		},
		{
			name: "a directory's name in the form's header",
			render: func() string {
				m := New(Setup{Palette: theme.Default(), Sections: []Section{NewTitleField(theme.Default())}, Name: "new session"})
				m.SetContext("fix\rOVER\x1b[2JWRITE · main")
				return m.ViewAt(80, 24)
			},
			want: []string{"fix OVER[2JWRITE · main"},
		},
		{
			name: "a directory's name in the submit view's header",
			render: func() string {
				v := NewSubmitView(theme.Default())
				v.SetHeader("new session", "fix\rOVER\x1b[2JWRITE")
				return v.ViewAt(80, 24)
			},
			want: []string{"fix OVER[2JWRITE"},
		},
		{
			// A note can quote a repository's own .herdr-draft.toml, which
			// arrives with `git clone`: a key's name is its text. The TOML
			// library escapes a C0 control in one itself; the C1 CSI here
			// is one it passes through.
			name: "a note on a panel",
			render: func() string {
				d := NewDirField(theme.Default())
				d.SetNotes([]string{"ignoring \x1b[2Jkey\u009b2J\r: not allowed"})
				return d.Panel(80, d.PanelRows())
			},
			want: []string{"ignoring [2Jkey2J : not allowed"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.render()
			for _, bad := range []string{"\r", "\a", "\x1b[2J", "\u009b"} {
				if strings.Contains(raw, bad) {
					t.Errorf("the render carries %q:\n%q", bad, raw)
				}
			}
			shown := ansi.Strip(raw)
			for _, want := range tc.want {
				if !strings.Contains(shown, want) {
					t.Errorf("the render does not show %q:\n%s", want, shown)
				}
			}
		})
	}
}
