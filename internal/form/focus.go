// Derived from atrium (github.com/ZviBaratz/atrium) ui/overlay/textInput_focus.go,
// © Zvi Baratz, relicensed by the author.
//
// Adaptations from the source: Atrium's focusRing is a ring over a
// closed enum of concrete field kinds (focusStop -- stopTitle,
// stopDirectory, ..., stopEnter), with a second, overlay-owned predicate
// (stopEnabled) and a second, overlay-owned fan-out (updateFocusState)
// doing the actual Focus()/Blur() calls on the overlay's own typed widget
// fields. herdr-draft's form has no closed enum of field kinds -- the
// Section interface (form.go) is how every field, present or future,
// looks the same to this package -- so the port collapses Atrium's three
// pieces (focusRing + stopEnabled + updateFocusState) into one:
//
//   - focusRing here holds `sections []Section` directly instead of
//     `stops []focusStop`, and asks each Section its own Enabled()
//     instead of consulting a separate `enabled func(focusStop) bool`
//     the caller supplies. Atrium's stopEnabled reads several different
//     overlay-owned widgets' own Disabled() methods by hand, one branch
//     per stop kind; Section.Enabled() makes that a single virtual call
//     that works uniformly over every present field.
//   - `set` folds in Atrium's updateFocusState fan-out directly (call
//     Blur() on every stop but the new one, Focus() on the new one)
//     rather than leaving it a separate method a caller must remember to
//     invoke after moving the cursor. Unlike Atrium's Blur()/Focus() (no
//     return value on either), Section.Focus() returns a tea.Cmd -- see
//     form.go's Section doc comment for why this package deliberately
//     widens the brief's literal `Focus()` signature -- so `set` returns
//     that Cmd, and form.go's Update is responsible for folding it into
//     whatever Cmd it itself returns (the PromptArea blink-command
//     concern the task brief calls out).
//   - `current`/`indexOf` return a Section (or -1/nil) instead of a
//     focusStop value, for the same reason.
//   - The always-an-enabled-stop-exists invariant Atrium relies on
//     (nextEnabled's fallback `return r.index` never actually triggers
//     because stopEnter is always enabled) is preserved the same way:
//     form.go always appends its own internal, always-enabled "create"
//     Section as the last ring member, so nextEnabled here still always
//     terminates on a real hit in practice, and the same defensive
//     "return the ring's current index" fallback guards a pathological
//     all-disabled state (e.g. a test's stub sections) from spinning.
//
// What carries over close to verbatim: the wrap-around, skip-disabled
// stepping loop itself (nextEnabled, walking the ring at most once per
// call so it always terminates) is Atrium's own algorithm, unchanged in
// shape.
package form

import tea "charm.land/bubbletea/v2"

// focusRing is the form's focus cursor: the ordered list of Sections
// present in the form (the caller's own Sections, plus form.go's internal
// "create" section always last) plus the index of the focused one. It
// owns navigation -- looking a Section up by ID, moving the cursor, and
// the wrap-around traversal that skips disabled sections -- and the
// Focus()/Blur() fan-out that keeps every Section's own focus state in
// sync with the cursor (Atrium's split between focusRing and
// updateFocusState collapses into this one type; see the package doc).
type focusRing struct {
	sections []Section
	index    int
}

// newFocusRing returns a focusRing over sections, with the cursor on the
// first enabled section (index 0 if none are enabled -- a defensive
// fallback; form.go's internal "create" section is always enabled, so in
// practice an enabled section always exists). It does not call Focus() or
// Blur() on anything -- matching this package's "pure constructor, no
// side effects until Init/Update" convention -- so the caller (form.go's
// Init) is responsible for focusing the initial section and folding in
// its returned tea.Cmd.
func newFocusRing(sections []Section) *focusRing {
	r := &focusRing{sections: sections}
	for i, s := range sections {
		if s.Enabled() {
			r.index = i
			break
		}
	}
	return r
}

// current returns the Section the cursor points at, or nil if the ring
// has no sections (form.go never constructs one that way, since it always
// appends its own "create" section, but this stays safe rather than
// panicking if it ever did).
func (r *focusRing) current() Section {
	if r.index < 0 || r.index >= len(r.sections) {
		return nil
	}
	return r.sections[r.index]
}

// indexOf returns the position of the Section whose ID() == id, or -1 if
// none matches -- ported from Atrium's indexOfStop.
func (r *focusRing) indexOf(id string) int {
	for i, s := range r.sections {
		if s.ID() == id {
			return i
		}
	}
	return -1
}

// set moves the cursor to index i (a no-op on an out-of-range i) and syncs
// every Section's own focus state to it: Blur() on every Section but the
// one at i, Focus() on the one at i. It returns the tea.Cmd the newly
// focused Section's own Focus() call produces (e.g. PromptArea's cursor
// blink command, once a concrete Prompt Section wraps one) -- ported in
// spirit from Atrium's setFocusIndex + updateFocusState pair, folded into
// one call because Section.Focus() (unlike Atrium's own Blur()/Focus(),
// and unlike Section.Blur() here) has a return value that must not be
// silently dropped by a caller who forgot the separate sync step existed.
func (r *focusRing) set(i int) tea.Cmd {
	if i < 0 || i >= len(r.sections) {
		return nil
	}
	var cmd tea.Cmd
	for idx, s := range r.sections {
		if idx == i {
			cmd = s.Focus()
		} else {
			s.Blur()
		}
	}
	r.index = i
	return cmd
}

// nextEnabled returns the first enabled section index reached from the
// current cursor by repeatedly stepping delta (+1 forward, -1 backward),
// wrapping around the section list -- ported near-verbatim from Atrium's
// focusRing.nextEnabled. The loop visits each section at most once, so it
// terminates even with every section but one disabled; when every section
// is disabled (a state form.go's own internal "create" section makes
// unreachable in practice, but a test's stub sections could construct) it
// falls back to the ring's current, unmoved index, matching Atrium's own
// fallback.
func (r *focusRing) nextEnabled(delta int) int {
	n := len(r.sections)
	if n == 0 {
		return r.index
	}
	i := r.index
	for range r.sections {
		i = (i + delta + n) % n
		if r.sections[i].Enabled() {
			return i
		}
	}
	return r.index
}

// move steps the cursor to the next enabled section in direction delta
// (see nextEnabled), syncing focus state (see set) and returning whatever
// tea.Cmd that produces.
func (r *focusRing) move(delta int) tea.Cmd {
	return r.set(r.nextEnabled(delta))
}

// focusByID moves the cursor directly to the section whose ID() == id (if
// present), regardless of that section's Enabled() state -- ported from
// Atrium's focusStop(kind). Its real caller is spec §9's submit-time
// validation, "inline verdicts, submit blocked, focus moved":
// internal/app's checkSubmitValidation re-focuses whichever field
// blocked the create. (The rule was first written as v1 spec §6 field
// 3's "a failing submit re-focuses Title"; v2 §6 replaces that section
// with a row table and does not restate it, but §9 is untouched by v2
// and states it generally.) A no-op (returns nil) when no section has
// that ID.
func (r *focusRing) focusByID(id string) tea.Cmd {
	if i := r.indexOf(id); i >= 0 {
		return r.set(i)
	}
	return nil
}
