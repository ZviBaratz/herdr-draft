package widgets

import "github.com/charmbracelet/x/ansi"

// Ellipsis marks an elided value. internal/form's sizes.go file doc
// records the decision this spelling belongs to: a HINT line may be
// clipped silently, because running out of room there costs the reader a
// suggestion they can live without, but a one-line VALUE cell that loses
// an end unmarked is not incomplete -- it is MISREAD, since
// "~/Projects/herdr-dra" and "zvi/fix-login-redir" both read as real
// values.
const Ellipsis = "…"

// KeepHead clips s to exactly width cells KEEPING ITS HEAD, marking the
// cut with Ellipsis: the rule for titles, branches, prose and
// identifiers, whose informative end is the one you read first (v2 spec
// §7).
//
// width < 1 renders nothing; a width of 1 that cannot hold s renders the
// ellipsis alone, which is still an honest "there is more here than fits"
// -- the same degrade-rather-than-panic contract picker.go's widthStyle
// documents for its own degenerate dimensions.
//
// This and KeepTail live here, rather than in internal/form where they
// were written, for the reason PaintLine gives above its own body: v3
// spec §8.1 gives every PickerColumn an ElideMode, so the picker needs
// both rules, and widgets cannot import form (form already imports
// widgets, so the dependency only runs one way). form.keepHead and
// form.keepTail are now delegators.
func KeepHead(s string, width int) string {
	if width < 1 {
		return ""
	}
	if ansi.StringWidth(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, Ellipsis)
}

// KeepTail clips s to exactly width cells KEEPING ITS TAIL, marking the
// cut with a leading Ellipsis: the rule for PATHS, where the last
// segments are what distinguish "~/Projects/herdr" from
// "~/Projects/herdr-draft" and the shared prefix is what everything on
// screen already has in common.
//
// ansi.TruncateLeft removes n cells and prepends the marker, so the n
// that leaves exactly width cells behind is (rendered width - width + 1),
// the +1 paying for the ellipsis itself. At width 1 that n would consume
// the whole string and TruncateLeft would emit nothing at all, so the
// degenerate widths short-circuit to the bare marker.
func KeepTail(s string, width int) string {
	if width < 1 {
		return ""
	}
	w := ansi.StringWidth(s)
	if w <= width {
		return s
	}
	if width == 1 {
		return Ellipsis
	}
	return ansi.TruncateLeft(s, w-width+1, Ellipsis)
}
