// field_options.go is the `options` row (agent-options spec §7): the
// session options the selected agent kind declares -- Claude's model,
// effort and permission mode -- as one consequence row over a panel with
// one part per option.
//
// Written fresh for herdr-draft. Atrium had three Claude-only chip rows
// (ui/overlay/modelField.go, effortField.go, modeField.go) that every other
// agent saw as "n/a"; this is the declared-schema replacement Atrium's own
// #816 called for, so nothing here is ported. The part grammar is
// field_worktree.go's, and the chip line is widgets.ChipRow.
//
// This package knows nothing about agents. What an option is called, what
// it offers and what flag it becomes all arrive from the app as OptionSpec
// values (built from internal/agentopts, which this package does not
// import), the same direction every other candidate list crosses.
package form

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ZviBaratz/herdr-draft/internal/form/widgets"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

const (
	optionsRowLabel  = "options"
	optionsNameLabel = "name"
	// optionsNoFlagID and optionsOtherID are the two chips every line may
	// carry beyond its offered values: the no-flag chip, always first, and
	// other (type a value), last and only on a free-text line.
	//
	// The no-flag chip's ID is still the word `create --effort inherit` and
	// config.toml take, though the chip no longer shows it: noFlagChip
	// labels it with what sending no flag leads to (agent-options spec
	// §7.2, amended 2026-09-22). Only the label moved, so the chip's zone
	// ID, its position under ←→ and Values' nil for it are what they were.
	optionsNoFlagID = "inherit"
	optionsOtherID  = "other"
	// optionsPinBadge marks a no-flag chip whose label is a value
	// [agents.extra_args] passes rather than one this chip sends.
	optionsPinBadge = "•"
	// optionsExtraArgsLead introduces the read-only extra_args line.
	optionsExtraArgsLead = "[agents.extra_args] adds: "
)

// OptionChoice is one offered value of an OptionSpec: what is sent, and
// what its chip says.
type OptionChoice struct {
	Value string
	Label string
}

// OptionSpec is one session option as this form renders it -- a
// form-local copy of an agentopts.Option, pushed in by the app.
type OptionSpec struct {
	// Name is the key Values reports the option under.
	Name string
	// Label is the option's label in the panel.
	Label string
	// Flag is the agent's own flag, which the panel's hint names.
	Flag string
	// Choices are the offered values after the no-flag chip.
	Choices []OptionChoice
	// FreeText adds an `other` chip and, while it is selected, a name
	// part to type a value into.
	FreeText bool
	// Valid is the typed value's rule, consulted on every edit to the
	// name input: an edit after which it is false is refused. Only
	// consulted when FreeText is set.
	Valid func(string) bool
	// Row renders a value the way the row states it ("effort xhigh").
	Row func(string) string
}

// KindOptions is everything the form needs about one agent kind's options,
// handed to SetKind. Every field is optional: a kind with no Specs is inert.
type KindOptions struct {
	Specs []OptionSpec
	// Seed is the kind's starting values (config.toml's
	// [agents.options.<kind>]), and SeedSource the file they came from,
	// for the panel's provenance line.
	Seed       map[string]string
	SeedSource string
	// ExtraArgs is [agents.extra_args] for the kind, shown read-only, and
	// Pinned what it already passes for the declared flags -- what an
	// inherited option actually launches with, and so what its no-flag
	// chip is labelled.
	ExtraArgs []string
	Pinned    map[string]string
}

// optionLine is one option's live state: its chips, and for a free-text
// option the name input behind `other`.
type optionLine struct {
	spec  OptionSpec
	chips *widgets.ChipRow
	name  *lineInput
}

// optionsPart addresses one line of the panel: an option's chips, or its
// name input.
type optionsPart struct {
	line   int
	onName bool
}

// kindState is one kind's options for the life of the popup.
type kindState struct {
	opts  KindOptions
	lines []*optionLine
}

// OptionsField is the `options` Section.
type OptionsField struct {
	palette theme.Palette
	focused bool

	kind  string
	state *kindState
	// kinds keeps every kind this popup has shown, so going claude ->
	// codex -> claude restores the choices made (agent-options spec §7.2).
	kinds map[string]*kindState
	part  int

	notes []string
}

// NewOptionsField returns an OptionsField with no kind yet, which is inert.
func NewOptionsField(palette theme.Palette) *OptionsField {
	return &OptionsField{palette: palette, kinds: map[string]*kindState{}, state: &kindState{}}
}

// SetPalette implements paletteSetter: repaint after the host terminal
// answered (host-colours spec §7.3, #347).
//
// The options row owns no sub-widget: it renders its chips itself from
// f.palette, so the stored value is the whole of it.
func (f *OptionsField) SetPalette(p theme.Palette) { f.palette = p }

// ID identifies this Section for form.go's zoneFor.
func (f *OptionsField) ID() string { return "options" }

// Label is the row label.
func (f *OptionsField) Label() string { return optionsRowLabel }

// Enabled reports whether the selected kind declares anything to choose.
// One that does not keeps its row (agent-options spec §7.1) but takes no
// focus stop.
func (f *OptionsField) Enabled() bool { return len(f.state.lines) > 0 }

// Kind is the agent kind the field currently shows.
func (f *OptionsField) Kind() string { return f.kind }

// SetKind shows kind's options. The first time a kind is shown, its lines
// are built from o and seeded; after that the field restores what the
// kind held when it was left, and o is ignored -- its values come from
// config, which does not change while the popup is open. Showing the
// current kind again is a no-op.
func (f *OptionsField) SetKind(kind string, o KindOptions) {
	if kind == f.kind && f.state != nil && f.kinds[kind] != nil {
		return
	}
	st, ok := f.kinds[kind]
	if !ok {
		st = &kindState{opts: o}
		for _, spec := range o.Specs {
			st.lines = append(st.lines, f.newLine(spec, o.Seed[spec.Name], noFlagChip(kind, spec, o.Pinned[spec.Name])))
		}
		f.kinds[kind] = st
	}
	f.kind, f.state = kind, st
	f.part = 0
	f.syncNameFocus()
}

// newLine builds one option's chips (noFlag, the offered values, then
// other for free text) and lands them on seed.
func (f *OptionsField) newLine(spec OptionSpec, seed string, noFlag widgets.Chip) *optionLine {
	chips := []widgets.Chip{noFlag}
	for _, c := range spec.Choices {
		chips = append(chips, widgets.Chip{ID: c.Value, Label: c.Label})
	}
	l := &optionLine{spec: spec, chips: widgets.NewChipRow(f.palette)}
	if spec.FreeText {
		chips = append(chips, widgets.Chip{ID: optionsOtherID, Label: optionsOtherID})
		l.name = newLineInput(f.palette, 0, f.palette.PanelBG)
		l.name.SetPlaceholder("type a " + spec.Label)
	}
	l.chips.SetChips(chips)
	if seed == "" {
		return l
	}
	// Matched against the OFFERED values only. The no-flag and other chips
	// carry form words as their IDs, and a free-text seed may spell one:
	// config.toml's `model = "other"` is a model id, which create sends as
	// `--model other`, so selecting the `other` chip with an empty name
	// here would send nothing and break the two paths' equivalence.
	for _, c := range spec.Choices {
		if c.Value == seed {
			l.chips.SelectID(seed)
			return l
		}
	}
	if l.name != nil {
		l.chips.SelectID(optionsOtherID)
		l.name.SetValue(seed)
	}
	return l
}

// SetNotes records config.toml's refused [agents.options] entries, already
// worded (config.AgentsConfig.OptionWarnings). They are about the file,
// not the kind, so they show whichever kind is selected.
func (f *OptionsField) SetNotes(notes []string) { f.notes = notes }

// noFlagChip is the chip that sends no flag, labelled with what sending
// none leads to as far as this plugin knows it (agent-options spec §7.2,
// amended 2026-09-22): the value [agents.extra_args] passes for the flag,
// badged so it does not read as a value this chip sends, or `<kind>'s`
// when it passes none, since the agent's own settings then decide. What
// those settings hold is not read -- they differ per account directory,
// and with an `auto` account picker which directory is not known until
// the submit.
//
// An offered value takes its own chip's label, so a pinned acceptEdits
// reads `accept-edits•` beside the `accept-edits` chip, as the row
// already spells it.
func noFlagChip(kind string, spec OptionSpec, pinned string) widgets.Chip {
	if pinned == "" {
		return widgets.Chip{ID: optionsNoFlagID, Label: kindName(kind) + "'s"}
	}
	label := pinned
	for _, c := range spec.Choices {
		if c.Value == pinned {
			label = c.Label
			break
		}
	}
	return widgets.Chip{ID: optionsNoFlagID, Label: label, Badge: optionsPinBadge}
}

// value is what one line sends: "" for the no-flag chip, and for `other`
// the typed name when it is a valid one.
func (l *optionLine) value() string {
	switch id := l.chips.Selected().ID; id {
	case optionsNoFlagID:
		return ""
	case optionsOtherID:
		v := strings.TrimSpace(l.name.Value())
		if v == "" || l.spec.Valid == nil || !l.spec.Valid(v) {
			return ""
		}
		return v
	default:
		return id
	}
}

// onOther reports whether the line's name part is open.
func (l *optionLine) onOther() bool {
	return l.name != nil && l.chips.Selected().ID == optionsOtherID
}

// Values is the selected kind's chosen options, option name to value, nil
// when every option inherits -- the shape plan.Input.AgentOptions carries.
func (f *OptionsField) Values() map[string]string {
	var out map[string]string
	for _, l := range f.state.lines {
		if v := l.value(); v != "" {
			if out == nil {
				out = map[string]string{}
			}
			out[l.spec.Name] = v
		}
	}
	return out
}

// parts lists the panel's addressable lines in order: each option's chips,
// and the name input right under its chips while `other` is selected.
func (f *OptionsField) parts() []optionsPart {
	var out []optionsPart
	for i, l := range f.state.lines {
		out = append(out, optionsPart{line: i})
		if l.onOther() {
			out = append(out, optionsPart{line: i, onName: true})
		}
	}
	return out
}

// current is the part the cursor rests on.
func (f *OptionsField) current() (optionsPart, bool) {
	parts := f.parts()
	if f.part < 0 || f.part >= len(parts) {
		return optionsPart{}, false
	}
	return parts[f.part], true
}

// Focus parks the cursor on the first part, as the worktree panel does, so
// ↑↓ mean the same thing every time the ring lands here.
func (f *OptionsField) Focus() tea.Cmd {
	f.focused = true
	f.part = 0
	return f.syncNameFocus()
}

// Blur removes input focus.
func (f *OptionsField) Blur() {
	f.focused = false
	f.syncNameFocus()
}

// Update is the worktree panel's grammar (agent-options spec §7.2):
//
//   - ↑↓ move between parts, clamped.
//   - ←→ move the chips of a chip part. On a name part they, and every
//     other key, go to the input.
//   - A printable key on a free-text option's chips selects `other` and
//     starts a fresh name with it -- the obvious thing to try, and a
//     keystroke that did nothing would read as broken -- provided the key
//     is a valid start; any other key leaves the chips alone.
//   - A click on a chip selects it and moves the cursor there.
//
// A kind with nothing declared ignores everything: a click can still focus
// its row (form.go's FocusByID ignores Enabled), as PlacementField's
// inert row can.
func (f *OptionsField) Update(msg tea.Msg) tea.Cmd {
	if !f.Enabled() {
		return nil
	}
	if click, ok := msg.(tea.MouseClickMsg); ok {
		return f.handleClick(click)
	}
	part, ok := f.current()
	if !ok {
		return nil
	}
	l := f.state.lines[part.line]
	if km, ok := msg.(tea.KeyPressMsg); ok {
		switch km.String() {
		case "up":
			return f.setPart(f.part - 1)
		case "down":
			return f.setPart(f.part + 1)
		case "left", "right":
			if !part.onName {
				if km.String() == "left" {
					l.chips.Prev()
				} else {
					l.chips.Next()
				}
				return f.syncNameFocus()
			}
		default:
			// A key typed on the chips STARTS a name, seeded with that key
			// (Atrium's modelField did the same), and only when the key
			// alone is a valid start. It does not edit whatever an earlier
			// visit to `other` left in the input: that text's cursor can sit
			// anywhere, so where the key would land -- and whether the edit
			// would even be accepted -- is not knowable from here, and a
			// refused edit after the chip had already moved left `other`
			// launching the OLD name (review of the first version). A key
			// that cannot start a name leaves the chips alone.
			if !part.onName && l.name != nil && km.Text != "" && km.Mod&^tea.ModShift == 0 &&
				l.spec.Valid != nil && l.spec.Valid(km.Text) {
				l.chips.SelectID(optionsOtherID)
				l.name.SetValue(km.Text)
				return f.setPart(f.part + 1)
			}
		}
	}
	if part.onName {
		return f.updateName(l, msg)
	}
	return nil
}

// updateName forwards msg to l's name input and refuses the edit if it
// leaves an invalid value behind (agent-options spec §7.2): a paste of
// `--foo` or a leading dash never lands. An empty input is always allowed
// -- it is how a name is cleared -- and sends nothing.
func (f *OptionsField) updateName(l *optionLine, msg tea.Msg) tea.Cmd {
	before := l.name.Value()
	cmd := l.name.Update(msg)
	if after := l.name.Value(); after != "" && after != before && (l.spec.Valid == nil || !l.spec.Valid(after)) {
		l.name.SetValue(before)
	}
	return cmd
}

// handleClick selects the clicked chip and moves the cursor to its line.
func (f *OptionsField) handleClick(msg tea.MouseClickMsg) tea.Cmd {
	for i, l := range f.state.lines {
		if _, ok := l.chips.SelectAt(msg, f.chipZonePrefix(l)); ok {
			for j, p := range f.parts() {
				if p.line == i && !p.onName {
					f.part = j
				}
			}
			return f.syncNameFocus()
		}
	}
	return nil
}

// chipZonePrefix is a line's mouse-zone prefix, "chip:options:<name>:".
func (f *OptionsField) chipZonePrefix(l *optionLine) string {
	return "chip:" + f.ID() + ":" + l.spec.Name + ":"
}

// setPart moves the cursor, clamped to the parts that exist.
func (f *OptionsField) setPart(p int) tea.Cmd {
	if n := len(f.parts()); p >= n {
		p = n - 1
	}
	if p < 0 {
		p = 0
	}
	f.part = p
	return f.syncNameFocus()
}

// syncNameFocus keeps a name input focused exactly while the cursor rests
// on it and the field holds focus, which is what makes its cursor blink
// there and nowhere else. It also re-clamps the cursor: leaving `other`
// closes a name part the cursor might have been counting past.
func (f *OptionsField) syncNameFocus() tea.Cmd {
	if n := len(f.parts()); f.part >= n {
		f.part = max(n-1, 0)
	}
	part, ok := f.current()
	var cmd tea.Cmd
	for i, l := range f.state.lines {
		if l.name == nil {
			continue
		}
		if f.focused && ok && part.onName && part.line == i {
			cmd = l.name.Focus()
		} else {
			l.name.Blur()
		}
	}
	return cmd
}

// --- the row and its panel ------------------------------------------------

// rowValue is what one line contributes to the row: its own value, or
// extra_args' pin for an inherited option (dim), or nothing.
func (f *OptionsField) rowValue(l *optionLine) (text string, dim bool) {
	if v := l.value(); v != "" {
		return l.spec.Row(v), false
	}
	if p := f.state.opts.Pinned[l.spec.Name]; p != "" {
		return l.spec.Row(p), true
	}
	return "", false
}

// Row states what the session will launch with (agent-options spec §7.1):
// the chosen options joined by " · ", extra_args' pins dim where an
// option inherits, `<kind>'s own settings` when nothing is set anywhere,
// and `none for <kind>` for a kind that declares nothing.
//
// Segments are laid down whole while they fit; the first that does not is
// cut at its tail and ends the row, so what survives a narrow window is
// the leading options, legibly, rather than a smear of all three.
func (f *OptionsField) Row(w int) string {
	if w < 1 {
		w = 1
	}
	dim := dimHint(f.palette)
	if !f.Enabled() {
		return fitLine(dim.Render(keepHead("none for "+kindName(f.kind), w)), w)
	}
	text := lipgloss.NewStyle().Foreground(f.palette.Text)
	var out strings.Builder
	room := w
	sep := " · "
	any := false
	for _, l := range f.state.lines {
		v, isDim := f.rowValue(l)
		if v == "" {
			continue
		}
		style := text
		if isDim {
			style = dim
		}
		if any {
			if room <= lipgloss.Width(sep) {
				break
			}
			out.WriteString(dim.Render(sep))
			room -= lipgloss.Width(sep)
		}
		any = true
		if lipgloss.Width(v) > room {
			out.WriteString(style.Render(keepHead(v, room)))
			break
		}
		out.WriteString(style.Render(v))
		room -= lipgloss.Width(v)
	}
	if !any {
		return fitLine(dim.Render(keepHead(kindName(f.kind)+"'s own settings", w)), w)
	}
	return fitLine(out.String(), w)
}

// kindName is a kind for a sentence, never an empty word.
func kindName(kind string) string {
	if kind == "" {
		return "this agent"
	}
	return kind
}

// hint is the dim sentence under the parts: what the focused part will
// send (agent-options spec §7.2).
func (f *OptionsField) hint() string {
	part, ok := f.current()
	if !ok {
		return ""
	}
	l := f.state.lines[part.line]
	flag := l.spec.Flag
	pinned := f.state.opts.Pinned[l.spec.Name]
	v := l.value()
	switch {
	case l.onOther() && v == "" && pinned != "":
		// What launches is extra_args' pin, and the row already says so;
		// "sends no --model" would contradict it.
		return fmt.Sprintf("type a %s; while it is empty, [agents.extra_args] passes %s %s", l.spec.Label, flag, pinned)
	case l.onOther() && v == "":
		return fmt.Sprintf("type a %s; an empty one sends no %s", l.spec.Label, flag)
	case v == "" && pinned != "":
		return fmt.Sprintf("sends no %s of its own; [agents.extra_args] passes %s", flag, pinned)
	case v == "":
		return fmt.Sprintf("sends no %s, so %s's own settings decide", flag, kindName(f.kind))
	case pinned != "" && pinned != v:
		return fmt.Sprintf("sends %s %s instead of [agents.extra_args]'s %s", flag, v, pinned)
	default:
		return fmt.Sprintf("sends %s %s", flag, v)
	}
}

// showsProvenance reports whether the values on screen are still exactly
// the config file's: only then may the panel say where they came from.
func (f *OptionsField) showsProvenance() bool {
	o := f.state.opts
	if o.SeedSource == "" || len(o.Seed) == 0 {
		return false
	}
	got := f.Values()
	if len(got) != len(o.Seed) {
		return false
	}
	for k, v := range o.Seed {
		if got[k] != v {
			return false
		}
	}
	return true
}

// extraArgsLine is the read-only extra_args line's text, "" when the kind
// has none configured.
func (f *OptionsField) extraArgsLine() string {
	if len(f.state.opts.ExtraArgs) == 0 {
		return ""
	}
	return optionsExtraArgsLead + strings.Join(f.state.opts.ExtraArgs, " ")
}

// Panel is one part per option, then -- as room allows, and given up
// bottom-first -- the hint, the extra_args line, the notes and the
// provenance line (agent-options spec §7.2). The parts go last, and when
// even they outnumber the rows, the ones kept are a window around the
// cursor, so the part being changed is never the one cut.
func (f *OptionsField) Panel(width, h int) string {
	if h < 1 {
		h = 1
	}
	dim := dimHint(f.palette)
	extras := []string{}
	if !f.Enabled() {
		extras = append(extras, panelText(dim.Render(keepHead(kindName(f.kind)+" takes no session options here", panelInner(width))), width))
		if e := f.extraArgsLine(); e != "" {
			extras = append(extras, panelText(dim.Render(keepHead(e, panelInner(width))), width))
		}
		for _, n := range f.notes {
			extras = append(extras, noteLine(n, width, f.palette))
		}
		return panelBlock(width, h, extras...)
	}

	inner := panelInner(width)
	labelW, valueW := labelCol(inner)
	// A live chip row supplies its own leading space, so it takes one cell
	// off the label column -- the worktree panel's off-by-one, for the same
	// alignment reason (field_worktree.go's Panel).
	chipLabelW, chipValueW := labelW, valueW
	if labelW > 0 {
		chipLabelW, chipValueW = labelW-1, valueW+1
	}

	lines := make([]string, 0, h)
	for i, p := range f.parts() {
		l := f.state.lines[p.line]
		active := f.part == i
		if !p.onName {
			v := l.chips.MarkedView(chipValueW, f.chipZonePrefix(l))
			if idx := strings.IndexByte(v, '\n'); idx >= 0 {
				v = v[:idx]
			}
			lines = append(lines, f.panelPart(active, l.spec.Label, v, chipLabelW))
			continue
		}
		var content string
		if f.focused && active {
			content = l.name.View(valueW)
		} else if v := l.name.Value(); v != "" {
			content = fitLine(lipgloss.NewStyle().Foreground(f.palette.Text).Render(keepHead(v, valueW)), valueW)
		} else {
			content = fitLine(dim.Render("type a "+l.spec.Label), valueW)
		}
		lines = append(lines, f.panelPart(active, optionsNameLabel, content, labelW))
	}
	// A region shorter than the parts -- the three-row floor with `other`
	// open is four -- keeps a window of them around the cursor rather than
	// letting panelBlock cut from the bottom, which would hide the very
	// line ←→ is changing (review of the first version).
	if len(lines) > h {
		start := 0
		if f.part >= h {
			start = f.part - h + 1
		}
		lines = lines[start : start+h]
	}

	if hint := f.hint(); hint != "" {
		extras = append(extras, panelText(dim.Render(keepHead(hint, inner)), width))
	}
	if e := f.extraArgsLine(); e != "" {
		extras = append(extras, panelText(dim.Render(keepHead(e, inner)), width))
	}
	for _, n := range f.notes {
		extras = append(extras, noteLine(n, width, f.palette))
	}
	if f.showsProvenance() {
		extras = append(extras, provenanceLine(f.state.opts.SeedSource, width, f.palette))
	}
	return panelBlock(width, h, append(lines, extras...)...)
}

// panelPart composes one part line: the gutter marker, the dim label in
// the shared label column, then the part's content.
func (f *OptionsField) panelPart(active bool, label, content string, labelW int) string {
	padded := ""
	if labelW > 0 {
		padded = dimText(f.palette).Width(labelW).MaxWidth(labelW).Inline(true).Render(label)
	}
	return panelGutter(active, f.palette) + padded + content
}

// PanelRows is every part plus every extra the panel would show given the
// room: the hint, the extra_args line, one per note, and the provenance
// line. An inert kind asks for its one sentence and the same extras.
func (f *OptionsField) PanelRows() int {
	extra := len(f.notes)
	if f.extraArgsLine() != "" {
		extra++
	}
	if !f.Enabled() {
		return 1 + extra
	}
	if f.showsProvenance() {
		extra += provenanceRows(f.state.opts.SeedSource)
	}
	return len(f.parts()) + 1 + extra
}

// FooterRungs implements footerHinter: a name part takes typing, a chip
// part takes ←→, and an inert kind takes nothing, which the zone table
// cannot tell apart.
func (f *OptionsField) FooterRungs() []string {
	if !f.Enabled() {
		return []string{"nothing to set here"}
	}
	if part, ok := f.current(); ok && part.onName {
		return []string{"type to edit · ↑↓ option", "↑↓ option"}
	}
	return []string{"↑↓ option · ←→ value", "←→ value"}
}
