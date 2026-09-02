package gitx

import (
	"strings"
	"testing"
)

// TestValidateBranchPrefix_Accepts pins the shapes that must keep working.
// The first three are load-bearing: config's built-in default is the
// lowercased OS username plus "/", so a rule set that rejected a trailing
// "/", or an ordinary username containing "." or "-", would reject the
// default itself and leave the plugin with nothing to fall back to.
//
// The last few pin `git check-ref-format` rules deliberately NOT applied to
// a prefix -- see ValidateBranchPrefix's own doc comment for why each is
// skipped rather than merely unimplemented.
func TestValidateBranchPrefix_Accepts(t *testing.T) {
	cases := []struct{ name, prefix string }{
		{"the built-in default shape", "zvi/"},
		{"username with a dot", "first.last/"},
		{"username with a dash", "first-last/"},
		{"username with both", "j.doe-2/"},
		{"the last-resort default", "user/"},
		{"empty means no prefix", ""},
		{"nested", "team/zvi/"},
		{"digits and underscores", "zvi_2/"},
		{"no trailing slash, prefix abuts the slug", "wip-"},
		{"rule 2 waived: one level", "zvi"},
		{"rule 7 waived: component ending in a dot", "zvi./"},
		{"rule 9 waived: a lone @ component", "zvi/@/"},
		{"non-ascii is git's business, not ours", "zoë/"},
	}
	for _, c := range cases {
		if err := ValidateBranchPrefix(c.prefix); err != nil {
			t.Errorf("%s: ValidateBranchPrefix(%q) = %v, want nil", c.name, c.prefix, err)
		}
	}
}

// TestValidateBranchPrefix_Rejects covers one prefix per rule the validator
// implements, and checks the reason names the rule it broke -- the reason is
// the user's only clue why their configured prefix silently became the
// default, so an unhelpful one is a real defect.
func TestValidateBranchPrefix_Rejects(t *testing.T) {
	cases := []struct{ name, prefix, wantReason string }{
		// Ours, not git's: the argv hazard the whole exercise is about.
		{"leading dash", "-zvi/", `starts with "-"`},
		{"leading dash that is a real flag", "--focus/", `starts with "-"`},

		// Rule 4: control characters, space, ~ ^ :
		{"NUL", "zvi\x00/", "control character"},
		{"tab", "zvi\t/", "control character"},
		{"newline", "zvi\n/", "control character"},
		{"DEL", "zvi\x7f/", "control character"},
		// U+0085 NEXT LINE is category Cc but sits outside the C0 range
		// git's rule 4 names -- this pins the deliberate widening. Spelled
		// as an expression rather than an escape so it is visible in the
		// source.
		{"C1 control", "zvi" + string(rune(0x85)) + "/", "control character"},
		{"space", "zvi x/", "contains a space"},
		{"tilde", "zvi~/", "git forbids"},
		{"caret", "zvi^/", "git forbids"},
		{"colon", "zvi:/", "git forbids"},

		// Rule 5: ? * [
		{"question mark", "zvi?/", "git forbids"},
		{"asterisk", "zvi*/", "git forbids"},
		{"open bracket", "zvi[/", "git forbids"},

		// Rule 10: backslash
		{"backslash", "DOMAIN\\zvi/", "git forbids"},

		// Rule 3: ".."
		{"double dot", "zvi../", `contains ".."`},
		{"parent traversal", "../zvi/", `contains ".."`},

		// Rule 8: "@{"
		{"reflog syntax", "zvi@{0}/", `contains "@{"`},

		// Rule 6 in part: empty components
		{"leading slash", "/zvi/", "empty path component"},
		{"double slash", "zvi//x/", "empty path component"},
		{"slash only", "/", "empty path component"},

		// Rule 1: component beginning with "." or ending in ".lock"
		{"hidden first component", ".git/", `begins with "."`},
		{"hidden later component", "zvi/.x", `begins with "."`},
		{"dot component", "zvi/./", `begins with "."`},
		{"lock component", "zvi.lock/", `ends with ".lock"`},
		{"lock trailing partial component", "zvi/x.lock", `ends with ".lock"`},
	}
	for _, c := range cases {
		err := ValidateBranchPrefix(c.prefix)
		if err == nil {
			t.Errorf("%s: ValidateBranchPrefix(%q) = nil, want an error", c.name, c.prefix)
			continue
		}
		if !strings.Contains(err.Error(), c.wantReason) {
			t.Errorf("%s: reason for %q = %q, want it to mention %q",
				c.name, c.prefix, err.Error(), c.wantReason)
		}
	}
}
