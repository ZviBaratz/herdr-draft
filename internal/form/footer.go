// footer.go is an independent implementation, NOT derived from atrium
// (github.com/ZviBaratz/atrium). Atrium's own equivalent -- the
// createFormHelp/promptFocusHelp width ladders and the fitHint selection
// function that picks a rung to fit -- live partly in
// ui/overlay/textInput_render.go (on the audited clean list, and already
// read for sizes.go/form.go: that file only ever *calls* fitHint, it
// never defines it) and partly in ui/overlay/hints.go, which is NOT on
// the clean list and was never opened for this task. So the specific
// ladder text and the rung-selection algorithm below are both written
// fresh -- not ported from or informed by hints.go's actual
// implementation -- against the spec's own description of the grammar
// they teach: originally v1 spec §6's, and now v2's, whose §8 states the
// key grammar, §4's mockups supply the wording (see zoneRungs) and §3
// rule 4 sets the priority between a rung's two halves (see
// footerRungs).
//
// The rung-selection algorithm (fitFooter) mirrors the *shape* of
// widgets/textarea.go's own selectPlaceholder (task 15, same house style:
// measure every candidate's real width, take a candidate that fits, fall
// back to the narrowest when nothing does) rather than Atrium's fitHint,
// for the same reason task 15's own doc comment gives for
// selectPlaceholder: the actual Atrium implementation was never read.
// Where it departs from selectPlaceholder is the choice among the
// candidates that DO fit -- first in an explicitly ordered ladder here,
// widest there; see crossRungs.
package form

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// footerRungs returns v2's CONTEXTUAL key ladder for one focused zone
// (v2 spec §3 rule 4: "the footer teaches the focused field, then states
// the constants"): the zone's own rungs crossed with the constant tail,
// in preference order. fitFooter takes the first crossing that fits the
// space renderFooter left it beside the footer's action buttons.
//
// Reached through footerRungsFor, which is what renderFooter calls: this
// is the branch for a focused section with no lead of its own.
//
// The cross product, rather than one flat list, is what lets the two
// halves degrade independently -- and, ordered as crossRungs orders it,
// what makes the CONSTANT half degrade first: a narrow window keeps
// "⌃S create now · ⇥ for the prompt" and drops "⇥ move · ⌃R clear",
// never the other way round.
func footerRungs(zone FocusZone, armed bool) []string {
	return crossRungs(zoneRungs(zone), tailRungs(armed))
}

// footerRungsFor is footerRungs with the focused section given the first
// word: a Section implementing footerHinter (form.go) supplies its own
// lead rungs -- it knows things the zone table cannot, such as whether
// its picker currently has anything to pick -- and anything else falls
// back to the table. An empty FooterRungs() slice means "nothing to add,
// use the table", not "no hints at all".
//
// The fallback DELEGATES to footerRungs rather than repeating its
// crossing (#16 item 2). Until then the two were independent
// implementations of the same cross product, and the test suite pinned
// the wrong one of them: the five call sites in form_test.go all
// exercise footerRungs, while renderFooter only ever calls this
// function, so the copy production ran was unpinned and free to drift
// away from the copy the tests asserted. Delegating puts those five
// assertions back on the production path, and that path is the common
// one even for the three sections that DO implement footerHinter: each of
// them returns nil unless it has something the zone table cannot know
// (IssueField only when the field is unavailable, and so on), so the
// override branch is the exception on every row, not the rule.
func footerRungsFor(s Section, zone FocusZone, armed bool) []string {
	if h, ok := s.(footerHinter); ok {
		if own := h.FooterRungs(); len(own) > 0 {
			return crossRungs(own, tailRungs(armed))
		}
	}
	return footerRungs(zone, armed)
}

// zoneRungs is the per-zone lead of v2's footer, widest first: what THIS
// field's keys do, in its own words. The wording is v2 spec §4's
// mockups'; a field with something better to say overrides it via
// footerHinter.
//
// Nothing here repeats a key the footer's own BUTTONS already carry.
// Both halves of the footer are read at once, so `↵ create` in a rung
// beside a `↵ create` button, or `Esc cancel` beside an `esc cancel`
// button, spends the scarcest line on the screen saying one thing twice
// (the 64-column render said "create" three times).
//
// The buttons own esc and ONE create key, and which key that is depends
// on the zone (form.go's createKey): ↵ where a bare Enter really
// submits, ⌃S in the seven zones where it does not. So the rule is not
// "the rungs may never say ↵" but "the rungs own whatever the button
// is not wearing here" -- a rung may teach ↵ exactly where the button
// has left it alone, which is how `↵ pin` (ZoneAccount below) and
// `↵ use HEAD` (field_worktree.go, #269) say what they say without
// contradicting the button two inches to their right.
//
// Until #281 this paragraph read "the buttons own ↵ and esc" with one
// written exception, and the tree had three situations needing it: the
// button said `↵ create` on every row while ↵ created on three of
// ten, and on two of those a rung said ↵ meant something else on the
// same line. The fix was the button, because a rung is per-zone by
// construction and a constant legend cannot be.
//
// ONE exception survives, and it is the one the clause was written for:
// an EMPTY title, where neither key creates yet -- ⌃S reaches a submit
// that checkSubmitValidation refuses with "title required" -- so the
// rung corrects the button rather than echoing it.
//
// ZoneBranch and ZoneBase still appear here, with no section currently
// mapped onto them: the worktree collapse took form.go's zoneKindByID
// entries for those IDs, but the ZoneKinds themselves are still part of
// keys.go's vocabulary, and a table that answers for every member of an
// enum is cheaper to trust than one that answers for most of them.
func zoneRungs(zone FocusZone) []string {
	switch zone.Kind {
	case ZoneIssue:
		return []string{"type to filter · ↑↓ choose · ⇥ complete", "↑↓ choose · ⇥ complete", "↑↓ choose"}
	case ZoneDir:
		return []string{"⇥ complete · ↑↓ choose · / ~ . browse", "⇥ complete · ↑↓ choose", "↑↓ choose"}
	case ZoneTitle:
		// The one zone whose hint depends on state rather than kind:
		// Enter submits from a filled title and advances from an empty
		// one (keys.go's MapKey), and zoneFor already computes exactly
		// that distinction for the grammar. Reusing it here is what
		// stops the footer lying about what Enter does -- and, with an
		// empty title, what stops the `↵ create` BUTTON lying about it.
		if zone.TitleEmpty {
			// Three rungs, not two, and the middle one is what keeps
			// this zone teaching itself under pressure. crossRungs
			// trades the CONSTANT tail away first, but it can only do
			// that down to the narrowest LEAD it is given: with just
			// the pair, the step below "name it to create · ⇥ for the
			// prompt" (36 cells) was "⇥ for the prompt · ⌃R clear"
			// (27), so one cell of pressure dropped the only words on
			// the line about creating -- the inversion crossRungs'
			// own doc comment says must not happen, on the OPENING
			// screen. #281's button is one cell wider while it says
			// ⌃S, which put a 64-column popup exactly there.
			return []string{"name it to create · ⇥ for the prompt", "name it to create", "⇥ for the prompt"}
		}
		return []string{"⌃S create now · ⇥ for the prompt", "⇥ for the prompt"}
	case ZonePrompt:
		// Two keys, and the narrow rung keeps ⌃J: ↵ creates from this zone
		// (v2 spec §8), so the newline is the one a user cannot do without,
		// while the keep · reap line is on screen in the panel even when
		// its chord is not (reap spec §5.2).
		return []string{"⌃J newline · ⌃X keep or reap", "⌃J newline"}
	case ZoneWorktree:
		return []string{"↑↓ part · ←→ toggle", "↑↓ part"}
	case ZoneBranch:
		return []string{"type to edit · ↑↓ part", "↑↓ part"}
	case ZoneBase:
		return []string{"↑↓ pick a base", "↑↓ pick"}
	case ZonePlacement:
		return []string{"←→ choose"}
	case ZoneAgent:
		return []string{"←→ favorites · ↑↓ all kinds", "↑↓ all kinds"}
	case ZoneOptions:
		// OptionsField.FooterRungs overrides this per part; this is the
		// chip-part answer, for a caller that asks the zone alone.
		return []string{"↑↓ option · ←→ value", "←→ value"}
	case ZoneAccount:
		// ↵ is this row's to name: it pins (v3 spec §10.3) rather than
		// creating, so the button beside it wears ⌃S and leaves the
		// glyph free. A row where browsing and choosing are different
		// gestures has to say which key chooses, or the distinction is
		// invisible.
		//
		// This rung is where #281 was found. It said exactly this before
		// the fix too, beside a button that also said ↵ -- the same
		// glyph twice on one line meaning two different things, in the
		// very function whose doc comment forbade it.
		return []string{"↑↓ browse · ↵ pin", "↵ pin"}
	case ZoneCreate:
		return []string{"⇧⇥ back to the form", "⇧⇥ back"}
	default:
		return nil
	}
}

// tailRungs is v2's constant tail, widest first -- the form-global
// grammar every zone shares, and only the part of it no rung and no
// button already carries. armed selects between "⌃R clear" and "⌃R
// again", exactly as v1's ladder did.
//
// It is deliberately short. v1's tail restated ⌃S, Esc AND the whole Tab
// pair on every field; ↵ and esc now live on the footer's own buttons
// (form.go's renderFooter), ⌃S is taught where it is worth teaching (the
// title's own rung, v2 spec §4's mockup), and what is left is the row
// move and the clear gesture -- the two keys nothing else on screen
// mentions.
func tailRungs(armed bool) []string {
	clearHint := "⌃R clear"
	if armed {
		clearHint = "⌃R again"
	}
	return []string{
		"⇥ move · " + clearHint,
		clearHint,
		"⇥ move",
	}
}

// crossRungs builds the footer's ladder in PREFERENCE order, best first:
// for each lead widest-first, that lead crossed with each tail
// widest-first, then that lead ALONE, and finally the narrowest tail
// alone as the floor. A nil/empty lead degrades to the tails alone
// rather than to an empty ladder.
//
// The "lead alone" entry after each lead's crossings is what makes the
// CONSTANT TAIL the half that gets traded away as the window narrows,
// which is v2 spec §3 rule 4's priority ("the footer teaches the focused
// field, then states the constants") read under pressure. It only works
// because fitFooter takes the FIRST rung that fits rather than the
// widest: by width alone a narrower lead carrying the whole tail
// routinely out-measures a wider lead carrying none, and at 64 columns
// with the title focused that is exactly what happened -- `⇥ for the
// prompt · ⇥ move · ⌃R clear` (36 cells) beat `⌃S create now · ⇥ for the
// prompt` (32), so the footer dropped the one rung teaching this field in
// order to restate two keys that apply to every field.
func crossRungs(lead, tail []string) []string {
	if len(lead) == 0 {
		return tail
	}
	out := make([]string, 0, len(lead)*(len(tail)+1)+1)
	for _, l := range lead {
		for _, t := range tail {
			if repeatsAKey(l, t) {
				continue
			}
			out = append(out, l+" · "+t)
		}
		out = append(out, l)
	}
	if len(tail) > 0 {
		out = append(out, tail[len(tail)-1])
	}
	return out
}

// repeatsAKey reports whether tail would say a key the lead has already
// said. Three of the zone rungs teach ⇥ in their own terms ("⇥ complete",
// "⇥ for the prompt"), and crossing those with the constant "⇥ move"
// produced `⌃S create now · ⇥ for the prompt · ⇥ move · ⌃R clear` -- the
// same glyph twice on the one line the form has for teaching keys, which
// is the defect this whole pass is about, in miniature. Skipping the
// crossing rather than rewording either half leaves the shorter tail
// (`⌃R clear` alone) to be picked instead, so nothing is lost but the
// repetition.
func repeatsAKey(lead, tail string) bool {
	for _, glyph := range []string{"⇥", "⌃R", "⌃S", "↑↓", "←→"} {
		if strings.Contains(lead, glyph) && strings.Contains(tail, glyph) {
			return true
		}
	}
	return false
}

// fitFooter returns the FIRST entry in rungs that fits within width, or
// the narrowest entry when none does (so a footer is still shown, rather
// than blanked, on a terminal narrower than every rung). An empty rungs
// slice returns "".
//
// First-fit, not widest-fit: crossRungs hands this function a ladder
// already ordered by what matters most, and "widest wins" has no notion
// of which half of a rung is worth the cells -- see crossRungs' own doc
// comment for the 64-column case that made the difference visible. The
// narrowest-when-nothing-fits fallback is unchanged, and matches
// widgets/textarea.go's selectPlaceholder (task 15).
func fitFooter(rungs []string, width int) string {
	if len(rungs) == 0 {
		return ""
	}

	narrowest := rungs[0]
	narrowestWidth := lipgloss.Width(narrowest)

	for _, candidate := range rungs {
		w := lipgloss.Width(candidate)
		if w <= width {
			return candidate
		}
		if w < narrowestWidth {
			narrowest, narrowestWidth = candidate, w
		}
	}
	return narrowest
}
