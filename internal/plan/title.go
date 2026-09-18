// title.go is the one place the session title's length cap exists (spec
// §6 field 3, "Cap 32 runes"). It is in plan for reap.go's reason:
// internal/form and internal/create both import plan already, and they
// read this ONE definition rather than keeping a copy each. The form caps
// its title row with TitleMaxRunes; `create` cuts with CutTitle. Before
// #176 only the form had a cap, so one long title built two different
// sessions.
package plan

// TitleMaxRunes is the longest session title, in runes: the title row's
// cap (spec §6 field 3). The title names the space, the tab and the agent,
// and a branch derived from it inherits the cap too.
const TitleMaxRunes = 32

// CutTitle returns title's first TitleMaxRunes runes, or title unchanged
// when it already fits. The cut is plain, mid-word if that is where rune 32
// falls, because that is what the form's title row does to a pasted or
// Linear-seeded title (bubbles' textinput CharLimit), and #176 decided the
// two paths cut the same way rather than a better way.
func CutTitle(title string) string {
	runes := []rune(title)
	if len(runes) <= TitleMaxRunes {
		return title
	}
	return string(runes[:TitleMaxRunes])
}
