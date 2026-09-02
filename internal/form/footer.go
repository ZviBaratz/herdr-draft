// footer.go is an independent implementation, NOT derived from atrium
// (github.com/ZviBaratz/atrium). Atrium's own equivalent -- the
// createFormHelp/promptFocusHelp width ladders and the fitHint selection
// function that picks a rung to fit -- live partly in
// ui/overlay/textInput_render.go (on the audited clean list, and already
// read for sizes.go/form.go: that file only ever *calls* fitHint, it
// never defines it) and partly in ui/overlay/hints.go, which is NOT on
// the clean list and was never opened for this task. So the specific
// ladder text and the rung-selection algorithm below are both written
// fresh against spec §6's own grammar description, not ported from or
// informed by hints.go's actual implementation.
//
// The rung-selection algorithm (fitFooter) mirrors the *shape* of
// widgets/textarea.go's own selectPlaceholder (task 15, same house style:
// measure every candidate's real width, prefer the widest that still
// fits, fall back to the narrowest when nothing does) rather than
// Atrium's fitHint, for the same reason task 15's own doc comment gives
// for selectPlaceholder: the actual Atrium implementation was never read.
package form

import "charm.land/lipgloss/v2"

// footerRungs returns v2's CONTEXTUAL key ladder for one focused zone
// (v2 spec §3 rule 4: "the footer teaches the focused field, then states
// the constants"): the zone's own rungs crossed with the constant tail,
// widest first. fitFooter picks the widest crossing that fits the space
// renderFooter left it beside the Create button.
//
// The cross product, rather than one flat list, is what lets the two
// halves degrade independently: a narrow window can keep "↑↓ pick" and
// drop back to a bare "Esc" tail, or keep the full tail and drop the
// zone hint, whichever is wider and still fits.
func footerRungs(zone FocusZone, armed bool) []string {
	return crossRungs(zoneRungs(zone), tailRungs(armed))
}

// footerRungsFor is footerRungs with the focused section given the first
// word: a Section implementing footerHinter (form.go) supplies its own
// lead rungs -- it knows things the zone table cannot, such as whether
// its picker currently has anything to pick -- and anything else falls
// back to the table. An empty FooterRungs() slice means "nothing to add,
// use the table", not "no hints at all".
func footerRungsFor(s Section, zone FocusZone, armed bool) []string {
	lead := zoneRungs(zone)
	if h, ok := s.(footerHinter); ok {
		if own := h.FooterRungs(); len(own) > 0 {
			lead = own
		}
	}
	return crossRungs(lead, tailRungs(armed))
}

// zoneRungs is the per-zone lead of v2's footer, widest first: what THIS
// field's keys do, in its own words. The wording is v2 spec §4's
// mockups' and the view plan's; a field with something better to say
// overrides it via footerHinter.
//
// ZoneBranch and ZoneBase still appear here, with no section currently
// mapped onto them: the worktree collapse took form.go's zoneKindByID
// entries for those IDs, but the ZoneKinds themselves are still part of
// keys.go's vocabulary, and a table that answers for every member of an
// enum is cheaper to trust than one that answers for most of them.
func zoneRungs(zone FocusZone) []string {
	switch zone.Kind {
	case ZoneIssue:
		return []string{"↑↓ pick · type to filter · Tab complete", "↑↓ pick"}
	case ZoneDir:
		return []string{"↑↓ pick · Tab complete · / ~ . browse", "↑↓ pick · Tab complete"}
	case ZoneTitle:
		// The one zone whose hint depends on state rather than kind:
		// Enter submits from a filled title and advances from an empty
		// one (keys.go's MapKey), and zoneFor already computes exactly
		// that distinction for the grammar. Reusing it here is what
		// stops the footer lying about what Enter does.
		if zone.TitleEmpty {
			return []string{"↵ next · Tab next", "↵ next"}
		}
		return []string{"↵ create · Tab next", "↵ create"}
	case ZonePrompt:
		return []string{"⌃J newline · ↵ next", "⌃J newline"}
	case ZoneWorktree:
		return []string{"↑↓ part · ←→ toggle · ↵ next", "↑↓ part · ←→ toggle"}
	case ZoneBranch:
		return []string{"type to edit · ↵ next", "↵ next"}
	case ZoneBase:
		return []string{"↑↓ pick · ↵ next", "↑↓ pick"}
	case ZonePlacement:
		return []string{"←→ choose · ↵ next", "←→ choose"}
	case ZoneAgent:
		return []string{"←→ favorites · ↑↓ all kinds", "↑↓ pick"}
	case ZoneAccount:
		return []string{"↑↓ pick · ↵ next", "↑↓ pick"}
	case ZoneCreate:
		return []string{"↵ create · ⇧Tab back", "↵ create"}
	default:
		return nil
	}
}

// tailRungs is v2's constant tail, widest first -- the form-global
// grammar every zone shares. armed selects between "⌃R clear" and "⌃R
// again", exactly as v1's ladder did.
func tailRungs(armed bool) []string {
	clearHint := "⌃R clear"
	if armed {
		clearHint = "⌃R again"
	}
	return []string{
		"Tab/⇧Tab move · ⌃S create · Esc cancel · " + clearHint,
		"Tab move · ⌃S create · Esc cancel",
		"⌃S create · Esc",
		"Esc",
	}
}

// crossRungs joins every lead with every tail, then adds the NARROWEST
// tail on its own as the floor. A nil/empty lead degrades to the tails
// alone rather than to an empty ladder.
//
// Only the narrowest tail is offered bare, and that restriction is the
// whole point rather than an economy. fitFooter picks the WIDEST rung
// that fits, with no notion of which half matters; offering the full tail
// ladder unaccompanied meant a wide constant tail could out-measure --
// and silently replace -- a narrower crossing that still carried the
// focused field's own hint. At 64 columns with the title focused that is
// exactly what happened: `Tab/⇧Tab move · ⌃S create · Esc cancel · ⌃R
// clear` (49 cells) beat `↵ create · Tab move · ⌃S create · Esc cancel`
// (45), so the footer stated the constants and taught nothing, which is
// v2 spec §3 rule 4 backwards. With only `Esc` bare, any crossing that
// fits always wins, and the bare rung is reached only on a window too
// narrow for even the shortest crossing.
func crossRungs(lead, tail []string) []string {
	if len(lead) == 0 {
		return tail
	}
	out := make([]string, 0, len(lead)*len(tail)+1)
	for _, l := range lead {
		for _, t := range tail {
			out = append(out, l+" · "+t)
		}
	}
	if len(tail) > 0 {
		out = append(out, tail[len(tail)-1])
	}
	return out
}

// fitFooter returns the widest entry in rungs whose rendered width fits
// within width, or the narrowest entry when none do (so a footer is still
// shown, rather than blanked, on a terminal narrower than every rung) --
// same widest-fit-or-narrowest-fallback shape as
// widgets/textarea.go's selectPlaceholder (task 15). An empty rungs slice
// returns "".
func fitFooter(rungs []string, width int) string {
	if len(rungs) == 0 {
		return ""
	}

	narrowest := rungs[0]
	narrowestWidth := lipgloss.Width(narrowest)

	var bestFit string
	bestFitWidth := -1 // -1: no candidate has fit yet.

	for _, candidate := range rungs {
		w := lipgloss.Width(candidate)
		if w < narrowestWidth {
			narrowest, narrowestWidth = candidate, w
		}
		if w <= width && w > bestFitWidth {
			bestFit, bestFitWidth = candidate, w
		}
	}

	if bestFitWidth == -1 {
		return narrowest
	}
	return bestFit
}
