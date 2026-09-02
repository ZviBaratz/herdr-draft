// Derived from atrium (github.com/ZviBaratz/atrium)
// ui/overlay/textInput_keys.go, © Zvi Baratz, relicensed by the author.
//
// Adaptations from the source: Atrium's HandleKeyPress/HandlePaste are
// methods on a stateful *TextInputOverlay that both interprets keys AND
// dispatches the results directly into its own owned widgets (title input,
// textarea, directory/branch/account pickers, ...), returning
// (shouldClose, branchFilterChanged). herdr-draft splits that in two, per
// the task-15 brief and the project's runtime/client-boundary framing: this
// file owns only the pure key-to-intent grammar (no widget references, no
// I/O), leaving dispatch -- actually moving focus, calling a widget's own
// key handler, closing the form -- to whichever caller wires this package
// to the concrete form (task 16 and later field tasks). Concretely:
//
//   - MapKey(msg, zone, armed) (KeyAction, armed) replaces
//     HandleKeyPress(msg) (shouldClose, branchFilterChanged): it returns
//     a KeyAction enum describing *what the keypress means* -- advance,
//     back, complete, submit, cancel, insert a newline, arm/fire/leave the
//     clear gesture, or "not part of the grammar, forward it" -- instead of
//     mutating overlay state and reporting two booleans. What to actually
//     do with that meaning (move the focus ring, call a picker's own
//     CompletePrefix-equivalent, close the form) is the caller's job.
//   - Atrium threads clearArmed as a field on the overlay, silently reset
//     to false at the top of every non-ctrl+r keypress. This file has no
//     owned state at all (MapKey takes no pointer receiver), so the armed
//     bool is threaded explicitly: the caller passes in whatever MapKey
//     last returned (or false for a fresh form), and reads back the new
//     value from the second return -- same "any other key disarms, a
//     second consecutive ctrl+r fires the clear" contract, just made
//     explicit at the call boundary instead of hidden in a field mutation.
//   - Atrium's isTitle/isTextarea/isDirectoryPicker/... zone tests are
//     boolean methods reading which concrete field type is focused off the
//     overlay's own field list. FocusZone replaces that with a plain
//     value the caller supplies: a ZoneKind (which of the form's 11 fields,
//     see the ZoneKind doc) plus the one piece of extra context the
//     grammar itself needs and no widget can supply on its own --
//     TitleEmpty, since "does Enter submit from Title" depends on whether
//     the title the caller is holding is blank, a fact this package has no
//     way to ask a widget for.
//   - Atrium's isDirectoryPicker/isModelField (the two fields whose Tab
//     tries a widget-owned completion before falling back to advancing)
//     become ZoneKind.isPicker(), covering every zone spec §6's field
//     order names a "picker": ZoneIssue, ZoneDir, ZoneBase. herdr-draft has
//     no model-alias field (spec §16 non-goal 3 drops it), so that half of
//     Atrium's Tab special-case has no equivalent here. Whether a
//     "picker" zone's own widget actually implements a completion step is
//     that widget's business, not this package's -- MapKey's contract is
//     just "try to complete before advancing" (ActionComplete); a zone
//     whose widget has nothing to complete is expected to treat
//     ActionComplete as a plain advance, the same way Atrium's
//     CompletePrefix() returning false falls through to
//     nextEnabledIndex(1).
//   - Atrium's isEnterButton (the Create-form Create button) becomes
//     ZoneCreate; isVariantPicker/isModelField/isModeField/isEffortField/
//     isDepsField (Atrium's claude-shaped fields, spec §16 non-goal 3) have
//     no equivalent zone here at all -- herdr-draft does not re-ship "the
//     Claude-shaped form."
//   - The "ctrl+j" case (MapKey below) is a real addition beyond the
//     ported source, not an adaptation of anything Atrium's
//     HandleKeyPress switches on directly: Atrium never special-cases
//     Ctrl+J at all, because its wrapped v1 bubbles textarea binds it to
//     insert-newline by default, so a Ctrl+J that reaches Atrium's
//     switch's default branch falls straight through to
//     `t.textarea, _ = t.textarea.Update(msg)` and the widget's own v1
//     keymap handles it. herdr-draft's wrapped charm.land/bubbles/v2
//     textarea does NOT bind ctrl+j to InsertNewline by default (verified
//     against DefaultKeyMap in the vendored v2.1.1 source -- only
//     "enter"/"ctrl+m" do), so spec §6's "⌃J ... newline" requirement has
//     no widget-level binding to fall back on, and this grammar layer
//     supplies it directly instead.
//   - HandlePaste's guard is preserved by construction rather than by
//     porting its body: Atrium's HandlePaste is a second function,
//     entirely separate from HandleKeyPress's msg.String()-keyed switch,
//     specifically so a clipboard payload can never accidentally match a
//     case like "esc" and cancel the form. This file's HandlePaste is
//     smaller still -- it takes no tea.PasteMsg at all, because MapKey's
//     signature only accepts tea.KeyPressMsg, a type bracketed-paste
//     content is never delivered as (bubbletea reports it as a distinct
//     tea.PasteMsg{Content}, see charm.land/bubbletea/v2@v2.0.8/paste.go).
//     So the Go type system itself makes "a pasted keyword falls into the
//     key switch" unreachable; the only behavior HandlePaste still needs
//     to supply is Atrium's unconditional disarm ("A paste ... disarms the
//     clear gesture exactly as any other non-Ctrl+R input does"), which is
//     all it does.
package form

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// KeyAction is what a keypress means to the form, independent of which
// widget happens to be focused -- MapKey's result. Deciding what to
// actually do with it (move the focus ring, forward the raw key to a
// widget, close the form) belongs to the caller, not this package.
type KeyAction int

const (
	// ActionNone means the keypress is not part of the grammar for the
	// given zone. The caller should forward the original tea.Msg to the
	// focused zone's own widget (typing, cursor movement within a field,
	// and any other editing the widget itself understands).
	ActionNone KeyAction = iota
	// ActionAdvance moves focus to the next enabled zone.
	ActionAdvance
	// ActionBack moves focus to the previous enabled zone.
	ActionBack
	// ActionComplete asks the current zone's own widget to try completing
	// the current input (e.g. shell-style path completion, or accepting
	// the highlighted picker row) before advancing. A zone whose widget
	// has nothing to complete should treat this the same as ActionAdvance
	// -- see the package doc's ZoneKind.isPicker note.
	ActionComplete
	// ActionSubmit requests the form submit and close.
	ActionSubmit
	// ActionCancel requests the form cancel and close, discarding the
	// draft.
	ActionCancel
	// ActionNewline asks the current zone's own widget to insert a literal
	// newline (only ever returned for ZonePrompt; see MapKey).
	ActionNewline
	// ActionArmClear arms the Ctrl+R Ctrl+R double-tap clear gesture. The
	// caller should not clear anything yet -- only track that the gesture
	// is now armed (MapKey's second return value already reports this,
	// but ActionArmClear lets a caller distinguish "just armed" from "key
	// unrelated to the gesture" for UI feedback, e.g. a footer hint).
	ActionArmClear
	// ActionClear is the fired clear gesture: the second, consecutive
	// Ctrl+R. The caller should rebuild the form to its default state --
	// this package has no config/profile data to do that with itself
	// (same division of labor as Atrium: "The app performs the rebuild --
	// it owns the config/profiles the pickers need").
	ActionClear
)

// String names a KeyAction for test failure output and any future
// debug/log line, matching the OpKind convention in internal/plan/build.go.
func (a KeyAction) String() string {
	switch a {
	case ActionNone:
		return "ActionNone"
	case ActionAdvance:
		return "ActionAdvance"
	case ActionBack:
		return "ActionBack"
	case ActionComplete:
		return "ActionComplete"
	case ActionSubmit:
		return "ActionSubmit"
	case ActionCancel:
		return "ActionCancel"
	case ActionNewline:
		return "ActionNewline"
	case ActionArmClear:
		return "ActionArmClear"
	case ActionClear:
		return "ActionClear"
	default:
		return fmt.Sprintf("KeyAction(%d)", int(a))
	}
}

// ZoneKind identifies which of the form's fields a keypress is aimed at.
// The set below covers every row v2 spec §6's table lists, plus Create,
// plus the two worktree sub-fields (Branch, Base) that v1 made rows of
// their own -- see ZoneBranch below for why those two outlived the
// sections they named.
type ZoneKind int

const (
	// ZoneIssue is the Linear issue picker (spec §6 field 1).
	ZoneIssue ZoneKind = iota
	// ZoneDir is the project directory picker (spec §6 field 2).
	ZoneDir
	// ZoneTitle is the title text field (spec §6 field 3).
	ZoneTitle
	// ZoneWorktree is the worktree on/off chip row (spec §6 field 4).
	ZoneWorktree
	// ZoneBranch is the worktree branch text field. v1 made it a Section
	// and a tabbable row of its own -- one third of spec §6 field 4,
	// present only while Worktree was on. v2 spec §6 collapses all three
	// parts into ONE worktree row with a three-part panel, so no section
	// maps onto this kind any more: form.go's zoneKindByID lost its
	// entry and the whole worktree row answers to ZoneWorktree.
	//
	// It is kept anyway, and so is footer.go's zoneRungs entry for it:
	// the kind is still part of this grammar's vocabulary, and a table
	// that answers for every member of an enum is cheaper to trust than
	// one that answers for most of them.
	ZoneBranch
	// ZoneBase is the worktree base-ref picker -- likewise a v1 Section
	// of its own, likewise now one part of the worktree panel rather
	// than a row, and likewise kept in the vocabulary. See ZoneBranch.
	ZoneBase
	// ZonePlacement is the placement chip row (spec §6 field 5).
	ZonePlacement
	// ZoneAgent is the agent chip row (spec §6 field 6).
	ZoneAgent
	// ZoneAccount is the clauth account picker (spec §6 field 7).
	ZoneAccount
	// ZonePrompt is the prompt textarea (spec §6 field 8) -- the only zone
	// in which MapKey ever returns ActionNewline, and (v2 spec §8) one of
	// the three from which a bare Enter submits.
	ZonePrompt
	// ZoneCreate is the Create button (spec §6 field 9): the form's last
	// focus stop, and, along with ZonePrompt and a non-empty ZoneTitle,
	// one of the three zones from which Enter submits instead of
	// advancing.
	//
	// v2 spec §8's closing line, "ZoneCreate is removed with the Create
	// section", is a spec ERRATUM: it predates the decision (v2 spec §5,
	// and issue #3's authoritative Revision) to keep Create as a Section
	// rendered on the footer line. The section stayed, so this zone stays
	// with it.
	ZoneCreate
)

// isPicker reports whether Tab tries "complete, then advance" in this zone
// rather than a plain advance (spec §6: "Tab = complete-then-advance in
// pickers, plain advance elsewhere"). Only the zones spec §6's field-order
// section itself calls a "picker" qualify: the Linear issue picker (field
// 1: "type-to-filter picker"), the project directory picker (field 2:
// "dual-mode directory picker"), and the worktree base picker (field 4:
// "Base picker"). ZoneAccount is deliberately excluded even though it is
// also a list: spec §6 never calls it a picker, and Atrium's own
// accountPicker (textInput_keys.go's isAccountPicker branch) only responds
// to arrow keys, never to Tab-complete, unlike Atrium's directoryPicker/
// modelField.
func (z ZoneKind) isPicker() bool {
	switch z {
	case ZoneIssue, ZoneDir, ZoneBase:
		return true
	default:
		return false
	}
}

// FocusZone identifies the zone a keypress is aimed at, plus the one piece
// of external context MapKey's grammar needs that no widget can answer on
// its own.
type FocusZone struct {
	// Kind is which form field the keypress targets.
	Kind ZoneKind
	// TitleEmpty is only consulted when Kind == ZoneTitle: Enter submits
	// from a non-empty title (spec §6) and advances from an empty one
	// (Atrium's comment: "an *empty* title falls through to the advance
	// below -- submitting would only bounce off the title-required
	// validation"). It is ignored for every other Kind.
	TitleEmpty bool
}

// MapKey maps a keypress to the form-grammar action it means in zone,
// implementing spec §6's interaction grammar (ported from Atrium's
// HandleKeyPress; see the package doc for the shape of the port).
//
// armed is the Ctrl+R Ctrl+R clear gesture's state coming in (false for a
// fresh form, or whatever MapKey's own previous call returned); the
// returned bool is that state after this keypress, threaded the same way
// through every subsequent call. Every keypress except a second consecutive
// Ctrl+R disarms it, matching Atrium's unconditional
// `t.clearArmed = false` immediately after its own ctrl+r branch.
func MapKey(msg tea.KeyPressMsg, zone FocusZone, armed bool) (KeyAction, bool) {
	keystroke := msg.String()

	// Double-tap Ctrl+R clears the form: the first press arms, a second
	// consecutive press fires the clear and disarms. This is a
	// form-global gesture, not scoped to zone -- Atrium's own gate is
	// "isCreateForm" (any focused field), and herdr-draft's form is
	// always the create form (task 16 strips Atrium's other overlay
	// roles).
	if keystroke == "ctrl+r" {
		if armed {
			return ActionClear, false
		}
		return ActionArmClear, true
	}
	// Every other key disarms, unconditionally -- ported from Atrium's
	// `t.clearArmed = false` running right after the ctrl+r branch, before
	// its own switch on msg.String() begins.
	armed = false

	switch keystroke {
	case "tab":
		if zone.Kind.isPicker() {
			return ActionComplete, armed
		}
		return ActionAdvance, armed
	case "shift+tab":
		// Unlike Tab, Shift+Tab never gets the complete-then-advance
		// treatment in any zone -- Atrium's HandleKeyPress calls
		// nextEnabledIndex(-1) unconditionally for "shift+tab", with no
		// picker special-case at all.
		return ActionBack, armed
	case "esc", "ctrl+c":
		return ActionCancel, armed
	case "ctrl+s":
		// Submit from any field -- Enter submits only from Create, the
		// prompt and a filled Title (it advances everywhere else), so
		// Ctrl+S is the submit-from-anywhere shortcut.
		return ActionSubmit, armed
	case "ctrl+j":
		// Newline-only, and only in the prompt: ctrl+j is bubbles
		// textarea's own default InsertNewline binding is NOT bound to
		// this chord (only "enter"/"ctrl+m" are, see
		// charm.land/bubbles/v2@v2.1.1/textarea/textarea.go
		// DefaultKeyMap), so this grammar layer -- not the widget --
		// owns interpreting it.
		if zone.Kind == ZonePrompt {
			return ActionNewline, armed
		}
		return ActionNone, armed
	case "shift+enter":
		// The newline key a footer would advertise on a terminal that
		// disambiguates modified keys (ported comment from Atrium's
		// textInput_keys.go) -- Ctrl+J and Alt+Enter remain the routes that
		// also work on a terminal that does not. Deliberately its own
		// case, not folded into "enter"/"alt+enter" below: that case
		// submits from Create, from a filled Title and (v2 spec §8) from
		// the prompt itself, so sharing it would mean Shift+Enter
		// creating a session instead of breaking a line. Outside the
		// prompt it is
		// inert (ActionNone), matching Atrium's silent swallow -- it is
		// not a navigation key and no footer claims it is anywhere else.
		if zone.Kind == ZonePrompt {
			return ActionNewline, armed
		}
		return ActionNone, armed
	case "enter", "alt+enter":
		if zone.Kind == ZoneCreate {
			return ActionSubmit, armed
		}
		if zone.Kind == ZoneAccount {
			// ↵ COMMITS the account pin (v3 spec §10.3). Complete rather
			// than a bespoke action because that is exactly what
			// ActionComplete already means -- "accept the highlighted
			// picker row" -- and because a Complete the field declines
			// (nothing changed) falls through to a plain advance, so a
			// second ↵ moves on.
			//
			// Enter and not Tab, even though Tab is the key isPicker()
			// zones complete on: Tab is how a user LEAVES a row, and a Tab
			// that pinned whatever the cursor was resting on would
			// reintroduce the very defect §10.3 removes. ZoneAccount stays
			// out of isPicker() for that reason.
			return ActionComplete, armed
		}
		if zone.Kind == ZoneTitle && !zone.TitleEmpty {
			// The quick-create contract: choosing a title is choosing a
			// branch (spec §6 field 3), so "n -> name -> Enter" creates
			// the session one-handed. An empty title falls through to
			// the shared advance below instead (submitting would only
			// bounce off the title-required validation).
			return ActionSubmit, armed
		}
		if zone.Kind == ZonePrompt {
			if msg.Mod.Contains(tea.ModAlt) {
				// Alt+Enter inserts a newline in the prompt: the
				// terminal-independent route that arrives even on a
				// legacy terminal (as ESC CR), unlike the real
				// Shift+Enter.
				return ActionNewline, armed
			}
			// A BARE Enter in the prompt submits (v2 spec §8: "↵ from the
			// prompt submits rather than advancing. Nothing used it for a
			// newline; ⌃J, ⇧↵ and ⌥↵ keep that job"). The prompt is the
			// last field a user fills before creating, and v1's advance
			// only moved focus onto the Create button -- one keystroke to
			// save, at the cost of a newline key nothing was using. The
			// companion view plan's footer table still says "↵ next" for
			// this zone; the spec wins, and footer.go's ZonePrompt rung
			// says so.
			return ActionSubmit, armed
		}
		// Every other field (an empty Title, pickers, chip rows) advances
		// to the next enabled zone, exactly like Tab in a non-picker
		// zone. Advancing by one rather than jumping to Create keeps
		// Enter consistent regardless of where a field sits in the order.
		return ActionAdvance, armed
	default:
		return ActionNone, armed
	}
}

// HandlePaste reports the Ctrl+R Ctrl+R clear gesture's armed state
// following a bubbletea tea.PasteMsg: always false. See the package doc's
// Adaptations section for why this alone is HandlePaste's entire job: the
// paste-routing guard spec §6 requires ("a clipboard containing 'esc' must
// not cancel the form") holds by construction here, because MapKey only
// accepts tea.KeyPressMsg and pasted content is never delivered as one --
// bubbletea reports bracketed paste as a distinct tea.PasteMsg{Content}. So
// the only behavior left for this function to supply is Atrium's
// unconditional disarm on paste (textInput_keys.go HandlePaste: "A paste is
// not the second half of a double-tap: it disarms the clear gesture exactly
// as any other non-Ctrl+R input does").
//
// Callers should call this whenever the form's update loop receives a
// tea.PasteMsg, threading its result into whatever armed state they
// otherwise thread through MapKey's second return value, and forward the
// paste's Content to the focused zone's own widget (e.g. PromptArea,
// wrapping bubbles/v2's textarea, which natively handles tea.PasteMsg) as
// literal data -- never re-interpreted through MapKey.
func HandlePaste() bool {
	return false
}
