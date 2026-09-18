// Package agentopts declares, per agent kind, the session options a person
// may choose for one session -- Claude's model, effort level and permission
// mode today -- and turns a choice into the agent's own command-line flags
// (agent-options spec §4).
//
// It is the declaration Atrium's #816 asked for instead of a form shaped
// around one agent: the form renders an `options` row FROM this, `create`
// registers its flags FROM this, and config.toml's `[agents.options.<kind>]`
// is validated AGAINST this. A kind with no entry here takes no options, and
// adding one is a change to this file alone.
//
// Pure: no I/O, and it imports nothing from this module, so internal/plan
// (which must stay pure) and internal/config can both depend on it.
// internal/form deliberately does not -- the app turns an Option into the
// form's own OptionSpec, the way every other candidate list reaches the
// form.
package agentopts

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Inherit is the value that sends no flag at all, so the agent's own
// settings decide. It is the word a person types and the chip they see; a
// Values map never stores it -- an inherited option is simply absent.
//
// "inherit" and not "default", following Atrium, which tried "default" and
// went back: `default` was a real --permission-mode value, so a chip named
// "default" said the opposite of what it did.
const Inherit = "inherit"

// Choice is one offered value: what the flag carries, and what a chip
// shows. They differ only where the agent's own spelling reads badly as a
// chip (acceptEdits).
type Choice struct {
	Value string
	Label string
}

// Option is one declared session option.
type Option struct {
	// Name is the option's key everywhere this plugin writes it down: the
	// config.toml key, the `create --json` key, and the provenance suffix
	// ("option.<name>"). The create flag is FlagName(Name).
	Name string
	// Label is the option's label in the form's panel.
	Label string
	// Flag is the agent's own flag, e.g. "--model".
	Flag string
	// Choices are the offered values, in the order a chip row shows them.
	// Inherit is not one of them: every option has it, first.
	Choices []Choice
	// FreeText admits a typed value outside Choices, checked against
	// freeTextPattern. Only a model id is open-ended.
	FreeText bool
	// RowFormat renders a chosen value in the form's row, e.g. "effort %s".
	// The chip label (or a typed value) is what fills the verb.
	RowFormat string

	// refused is a value the agent accepts but this plugin will not offer,
	// with the reason a refusal gives. Kept apart from "unknown" so that
	// asking for bypassPermissions is told why, not told it does not exist.
	refused map[string]string
}

// Values is one session's chosen options, option name to value. An
// inherited option is absent, and a Values with nothing in it is nil -- the
// shape plan.Input carries, which equivalence_test.go compares with
// reflect.DeepEqual.
type Values map[string]string

// freeTextPattern is what a typed model id may look like (agent-options
// spec §4.2). The leading alphanumeric is the load-bearing part: a value
// can never be read as a flag of its own. Brackets are allowed because the
// 1M-context ids carry them (`claude-opus-5[1m]`), and no shell ever sees
// the value unquoted -- Path A is an argv vector, Path B's PaneRun quotes.
var freeTextPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/\[\]-]{0,63}$`)

// freeTextRule is freeTextPattern, said for a person.
const freeTextRule = "a model id starts with a letter or digit and uses only letters, digits and . _ : / [ ] -, at most 64 characters"

// extraArgsEscape ends every refusal: the value is not forbidden, it is
// merely not offered, and the user's own config.toml can still pass it.
const extraArgsEscape = "[agents.extra_args] can still pass it"

// declarations is every declared kind. Claude's values are copied from
// `claude --help` of claude 2.1.276 (2026-09-18); the spec's §9 says how a
// stale copy shows up, and docs/manual-smoke.md carries the check.
var declarations = map[string][]Option{
	"claude": {
		{
			Name:  "model",
			Label: "model",
			Flag:  "--model",
			Choices: []Choice{
				{"fable", "fable"},
				{"opus", "opus"},
				{"sonnet", "sonnet"},
				{"haiku", "haiku"},
			},
			FreeText:  true,
			RowFormat: "%s",
		},
		{
			Name:  "effort",
			Label: "effort",
			Flag:  "--effort",
			Choices: []Choice{
				{"low", "low"},
				{"medium", "medium"},
				{"high", "high"},
				{"xhigh", "xhigh"},
				{"max", "max"},
			},
			RowFormat: "effort %s",
		},
		{
			Name:  "permission_mode",
			Label: "mode",
			Flag:  "--permission-mode",
			Choices: []Choice{
				{"manual", "manual"},
				{"plan", "plan"},
				{"acceptEdits", "accept-edits"},
				{"auto", "auto"},
			},
			RowFormat: "%s mode",
			// Agent-options spec §4.3.
			refused: map[string]string{
				"bypassPermissions": "it opens with a confirmation dialog, and the session's prompt would be typed into it",
				"dontAsk":           "it refuses every action not already allowed without asking, which suits a script rather than a pane",
			},
		},
	},
}

// For returns the options kind declares, in declaration order, or nil for
// a kind that declares none. The result is a copy.
func For(kind string) []Option {
	opts := declarations[kind]
	if len(opts) == 0 {
		return nil
	}
	out := make([]Option, len(opts))
	for i, o := range opts {
		o.Choices = append([]Choice(nil), o.Choices...)
		out[i] = o
	}
	return out
}

// Names is every option name any kind declares, each once, in declaration
// order -- the set `create` registers a flag for. Kinds are visited in a
// fixed order so the answer is stable.
func Names() []string {
	var out []string
	seen := map[string]bool{}
	for _, kind := range declaredKinds() {
		for _, o := range declarations[kind] {
			if !seen[o.Name] {
				seen[o.Name] = true
				out = append(out, o.Name)
			}
		}
	}
	return out
}

// declaredKinds lists the declared kinds, claude first and the rest in name
// order. A map has no order of its own, and Names must not reorder the
// flags between two runs.
func declaredKinds() []string {
	kinds := make([]string, 0, len(declarations))
	for k := range declarations {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool {
		a, b := kinds[i], kinds[j]
		if a == "claude" || b == "claude" {
			return a == "claude"
		}
		return a < b
	})
	return kinds
}

// FlagName is the `create` flag for an option name: its underscores
// spelled as dashes, which is how every other create flag is written.
func FlagName(name string) string { return strings.ReplaceAll(name, "_", "-") }

// lookup finds one option of one kind.
func lookup(kind, name string) (Option, bool) {
	for _, o := range declarations[kind] {
		if o.Name == name {
			return o, true
		}
	}
	return Option{}, false
}

// Normalize turns what a person wrote for one option into the value a
// Values map stores: "" for Inherit (or nothing at all), the canonical
// value otherwise. The error is a reason, phrased to follow whatever the
// caller prefixes it with ("invalid --effort \"ultra\": ...").
func Normalize(kind, name, v string) (string, error) {
	o, ok := lookup(kind, name)
	if !ok {
		return "", fmt.Errorf("%s declares no %s option", kind, name)
	}
	v = strings.TrimSpace(v)
	if v == "" || v == Inherit {
		return "", nil
	}
	for _, c := range o.Choices {
		if c.Value == v {
			return v, nil
		}
	}
	if why, refused := o.refused[v]; refused {
		return "", fmt.Errorf("not offered, because %s; %s", why, extraArgsEscape)
	}
	if o.FreeText {
		if !freeTextPattern.MatchString(v) {
			return "", fmt.Errorf("%s", freeTextRule)
		}
		return v, nil
	}
	return "", fmt.Errorf("expected one of %s, or %s", o.valueList(), Inherit)
}

// ValidFree reports whether v is a value this option accepts as typed text.
// It is the form's keystroke filter for the model id input: the input
// refuses any edit after which this is false, so a paste of `--foo` never
// lands.
func (o Option) ValidFree(v string) bool {
	return o.FreeText && freeTextPattern.MatchString(v) && v != Inherit
}

// valueList is the offered values, comma-separated, for an error message.
func (o Option) valueList() string {
	vs := make([]string, len(o.Choices))
	for i, c := range o.Choices {
		vs[i] = c.Value
	}
	return strings.Join(vs, ", ")
}

// Row renders v the way the form's row states it: RowFormat, filled with
// the chip label for an offered value or the value itself for a typed one.
func (o Option) Row(v string) string {
	label := v
	for _, c := range o.Choices {
		if c.Value == v {
			label = c.Label
			break
		}
	}
	return fmt.Sprintf(o.RowFormat, label)
}

// Validate reports whether vals is a set of options kind declares, each
// holding a canonical, non-inherited value -- the precondition plan.Build
// checks before it turns them into argv. Every caller that builds a Values
// goes through Normalize, so this failing means a caller built one by hand.
func Validate(kind string, vals Values) error {
	for _, o := range declarations[kind] {
		if v, ok := vals[o.Name]; ok {
			if got, err := Normalize(kind, o.Name, v); err != nil {
				return fmt.Errorf("%s %q: %v", o.Name, v, err)
			} else if got != v || got == "" {
				return fmt.Errorf("%s %q is not a canonical value", o.Name, v)
			}
		}
	}
	for name := range vals {
		if _, ok := lookup(kind, name); !ok {
			return fmt.Errorf("%s declares no %s option", kind, name)
		}
	}
	return nil
}

// Launch is the argument list an agent of this kind starts with:
// extra_args, less the flags a set option displaces, then the set options
// in declaration order (agent-options spec §5). Both launch paths call it,
// which is what keeps them agreeing.
//
// Displacement removes both spellings -- `--model opus` as two elements and
// `--model=opus` as one -- so nothing depends on how the agent treats a
// repeated flag. With nothing set, extra is returned as it is, nil
// included, so a plan with no options is the plan this package predates.
func Launch(kind string, extra []string, vals Values) []string {
	if len(vals) == 0 {
		return extra
	}
	var set []Option
	for _, o := range declarations[kind] {
		if vals[o.Name] != "" {
			set = append(set, o)
		}
	}
	out := make([]string, 0, len(extra)+2*len(set))
	for i := 0; i < len(extra); i++ {
		a := extra[i]
		if o, ok := flagOf(set, a); ok {
			if a == o.Flag {
				i++ // the flag's value goes with it
			}
			continue
		}
		out = append(out, a)
	}
	for _, o := range set {
		out = append(out, o.Flag, vals[o.Name])
	}
	return out
}

// flagOf reports which of opts argument a is the flag of, in either
// spelling.
func flagOf(opts []Option, a string) (Option, bool) {
	for _, o := range opts {
		if a == o.Flag || strings.HasPrefix(a, o.Flag+"=") {
			return o, true
		}
	}
	return Option{}, false
}

// Pinned is what extra_args already passes for this kind's declared flags,
// the last occurrence winning -- what an INHERITED option will actually
// launch with, which the form says so the row does not claim "claude's own
// settings" over a config file that decided otherwise. nil when it passes
// none of them.
func Pinned(kind string, extra []string) Values {
	opts := declarations[kind]
	var out Values
	for i := 0; i < len(extra); i++ {
		o, ok := flagOf(opts, extra[i])
		if !ok {
			continue
		}
		v := ""
		if extra[i] == o.Flag {
			if i+1 >= len(extra) {
				continue
			}
			i++
			v = extra[i]
		} else {
			v = strings.TrimPrefix(extra[i], o.Flag+"=")
		}
		if out == nil {
			out = Values{}
		}
		out[o.Name] = v
	}
	return out
}
