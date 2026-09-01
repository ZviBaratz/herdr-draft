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

// footerRungs returns spec §6's key-ladder hint, widest (most descriptive)
// first, narrowest last, for the form's global grammar: Tab/Shift+Tab
// move the focus ring, Enter advances (or submits from Title/Create),
// Ctrl+S submits from anywhere, Esc/Ctrl+C cancel, and Ctrl+R Ctrl+R
// clears. armed selects between "⌃R clear" (arm the gesture) and "⌃R
// again" (fire it) for the trailing clause, matching keys.go's own
// MapKey/HandlePaste arm-state contract.
//
// This is the form-global footer only -- spec §6's per-zone rungs (e.g. a
// picker's "Tab complete/move" or the prompt's "⌃J newline") are a
// concrete field Section's own business once one exists (Tasks 17-18),
// not something the form root can word on a caller's behalf through the
// opaque Section interface.
func footerRungs(armed bool) []string {
	clearHint := "⌃R clear"
	if armed {
		clearHint = "⌃R again"
	}
	base := []string{
		"Tab/⇧Tab move · ↵ advance · ⌃S create · Esc cancel",
		"Tab move · ⌃S create · Esc cancel",
		"⌃S create · Esc cancel",
	}
	rungs := make([]string, len(base))
	for i, b := range base {
		rungs[i] = b + " · " + clearHint
	}
	return rungs
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
