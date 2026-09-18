package agentopts

import (
	"reflect"
	"strings"
	"testing"
)

func TestFor_ClaudeDeclaresModelEffortAndModeInThatOrder(t *testing.T) {
	var names []string
	for _, o := range For("claude") {
		names = append(names, o.Name)
	}
	want := []string{"model", "effort", "permission_mode"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("claude options = %v, want %v", names, want)
	}
}

func TestFor_AnUndeclaredKindHasNoOptions(t *testing.T) {
	if got := For("codex"); len(got) != 0 {
		t.Fatalf("For(codex) = %v, want none", got)
	}
	if got := For(""); len(got) != 0 {
		t.Fatalf("For(\"\") = %v, want none", got)
	}
}

// For hands out a copy: a caller editing what it got back must not change
// what the next caller sees, or one package could silently re-declare an
// agent for every other.
func TestFor_ReturnsACopy(t *testing.T) {
	got := For("claude")
	got[0].Flag = "--mangled"
	got[0].Choices[0].Value = "mangled"
	again := For("claude")
	if again[0].Flag != "--model" || again[0].Choices[0].Value == "mangled" {
		t.Fatalf("For returned shared storage: %+v", again[0])
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		name, option, in, want string
		wantErr                string
	}{
		{"alias", "model", "opus", "opus", ""},
		{"typed id with brackets", "model", "claude-opus-5[1m]", "claude-opus-5[1m]", ""},
		{"surrounding space trimmed", "model", "  sonnet ", "sonnet", ""},
		{"inherit means unset", "model", "inherit", "", ""},
		{"empty means unset", "effort", "", "", ""},
		{"leading dash would read as a flag", "model", "--dangerously-skip-permissions", "", "starts with a letter or digit"},
		{"space inside an id", "model", "opus 5", "", "starts with a letter or digit"},
		{"too long", "model", strings.Repeat("a", 65), "", "at most 64"},
		{"effort level", "effort", "xhigh", "xhigh", ""},
		{"unknown effort", "effort", "ultra", "", "expected one of low, medium, high, xhigh, max, or inherit"},
		{"effort is not free text", "effort", "Medium", "", "expected one of"},
		{"mode", "permission_mode", "acceptEdits", "acceptEdits", ""},
		{"mode spelled as its chip", "permission_mode", "accept-edits", "", "expected one of manual, plan, acceptEdits, auto, or inherit"},
		{"bypass is refused with its reason", "permission_mode", "bypassPermissions", "", "confirmation dialog"},
		{"dontAsk is refused with its reason", "permission_mode", "dontAsk", "", "[agents.extra_args]"},
		{"undeclared option", "sandbox", "on", "", "claude declares no sandbox option"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Normalize("claude", tc.option, tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Normalize(%q, %q) error = %v, want one containing %q", tc.option, tc.in, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Normalize(%q, %q): %v", tc.option, tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("Normalize(%q, %q) = %q, want %q", tc.option, tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalize_AKindWithNothingDeclared(t *testing.T) {
	_, err := Normalize("codex", "model", "o3")
	if err == nil || !strings.Contains(err.Error(), "codex declares no model option") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidate(t *testing.T) {
	if err := Validate("claude", nil); err != nil {
		t.Fatalf("nil values: %v", err)
	}
	if err := Validate("codex", nil); err != nil {
		t.Fatalf("nil values for an undeclared kind: %v", err)
	}
	if err := Validate("claude", Values{"model": "opus", "effort": "high"}); err != nil {
		t.Fatalf("valid values: %v", err)
	}
	for name, vals := range map[string]Values{
		"undeclared kind":    {"model": "opus"},
		"invalid value":      {"effort": "ultra"},
		"non-canonical":      {"model": " opus"},
		"empty stored value": {"model": ""},
	} {
		kind := "claude"
		if name == "undeclared kind" {
			kind = "codex"
		}
		if err := Validate(kind, vals); err == nil {
			t.Errorf("%s: Validate(%s, %v) = nil, want an error", name, kind, vals)
		}
	}
}

func TestLaunch_AppendsSetOptionsInDeclarationOrder(t *testing.T) {
	got := Launch("claude", nil, Values{"permission_mode": "plan", "model": "opus"})
	want := []string{"--model", "opus", "--permission-mode", "plan"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Launch = %q, want %q", got, want)
	}
}

// Nothing set leaves extra_args exactly as configured -- the same slice,
// nil included, so a plan built before this package existed is unchanged.
func TestLaunch_NothingSetLeavesExtraArgsAlone(t *testing.T) {
	if got := Launch("claude", nil, nil); got != nil {
		t.Fatalf("Launch(nil, nil) = %q, want nil", got)
	}
	extra := []string{"--model", "opus", "--verbose"}
	if got := Launch("claude", extra, nil); !reflect.DeepEqual(got, extra) {
		t.Fatalf("Launch = %q, want %q", got, extra)
	}
}

// Agent-options spec §5.2: an option that is set displaces the same flag in
// extra_args, in both spellings, rather than relying on the agent taking
// the last of two.
func TestLaunch_ASetOptionDisplacesItsFlagInExtraArgs(t *testing.T) {
	extra := []string{"--model", "opus", "--verbose", "--effort=high", "--add-dir", "/tmp"}
	got := Launch("claude", extra, Values{"model": "fable", "effort": "max"})
	want := []string{"--verbose", "--add-dir", "/tmp", "--model", "fable", "--effort", "max"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Launch = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(extra, []string{"--model", "opus", "--verbose", "--effort=high", "--add-dir", "/tmp"}) {
		t.Fatalf("Launch modified the caller's extra_args: %q", extra)
	}
}

// Only a flag whose option is SET is displaced: an inherited option leaves
// extra_args' own pin standing, which is what the panel tells the user.
func TestLaunch_AnInheritedOptionLeavesItsFlagInExtraArgs(t *testing.T) {
	extra := []string{"--model", "opus"}
	got := Launch("claude", extra, Values{"effort": "low"})
	want := []string{"--model", "opus", "--effort", "low"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Launch = %q, want %q", got, want)
	}
}

// A dangling flag at the end of extra_args (no value after it) is removed
// on its own rather than taking nothing -- or, worse, running off the end.
func TestLaunch_DanglingFlagAtTheEnd(t *testing.T) {
	got := Launch("claude", []string{"--verbose", "--model"}, Values{"model": "haiku"})
	want := []string{"--verbose", "--model", "haiku"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Launch = %q, want %q", got, want)
	}
}

func TestPinned(t *testing.T) {
	got := Pinned("claude", []string{"--model", "opus", "--verbose", "--effort=high", "--model=sonnet"})
	want := Values{"model": "sonnet", "effort": "high"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Pinned = %v, want %v", got, want)
	}
	if got := Pinned("claude", []string{"--verbose"}); got != nil {
		t.Fatalf("Pinned with no declared flag = %v, want nil", got)
	}
	if got := Pinned("codex", []string{"--model", "o3"}); got != nil {
		t.Fatalf("Pinned for an undeclared kind = %v, want nil", got)
	}
}

func TestOptionRow(t *testing.T) {
	opts := For("claude")
	cases := []struct {
		opt  Option
		v    string
		want string
	}{
		{opts[0], "opus", "opus"},
		{opts[0], "claude-opus-5[1m]", "claude-opus-5[1m]"},
		{opts[1], "xhigh", "effort xhigh"},
		{opts[2], "plan", "plan mode"},
		{opts[2], "acceptEdits", "accept-edits mode"},
	}
	for _, tc := range cases {
		if got := tc.opt.Row(tc.v); got != tc.want {
			t.Errorf("%s.Row(%q) = %q, want %q", tc.opt.Name, tc.v, got, tc.want)
		}
	}
}

func TestNamesAndFlagNames(t *testing.T) {
	if got, want := Names(), []string{"model", "effort", "permission_mode"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Names = %v, want %v", got, want)
	}
	if got := FlagName("permission_mode"); got != "permission-mode" {
		t.Fatalf("FlagName = %q", got)
	}
}

// Every offered value has to be one the agent's own flag accepts, and the
// free-text rule has to admit every offered model alias -- otherwise a chip
// could produce a value Normalize itself would refuse.
func TestDeclaration_IsSelfConsistent(t *testing.T) {
	for kind, opts := range declarations {
		seen := map[string]bool{}
		for _, o := range opts {
			if seen[o.Name] {
				t.Errorf("%s declares %s twice", kind, o.Name)
			}
			seen[o.Name] = true
			if !strings.HasPrefix(o.Flag, "--") {
				t.Errorf("%s.%s flag %q is not a long flag", kind, o.Name, o.Flag)
			}
			for _, c := range o.Choices {
				if got, err := Normalize(kind, o.Name, c.Value); err != nil || got != c.Value {
					t.Errorf("%s.%s offers %q, which Normalize gives back as %q, %v", kind, o.Name, c.Value, got, err)
				}
				if c.Value == Inherit {
					t.Errorf("%s.%s offers %q as a value", kind, o.Name, Inherit)
				}
			}
			for v := range o.refused {
				for _, c := range o.Choices {
					if c.Value == v {
						t.Errorf("%s.%s both offers and refuses %q", kind, o.Name, v)
					}
				}
			}
		}
	}
}
