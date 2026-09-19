package gitx

import (
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// nbsp and ideographicSpace are spelled as expressions, not as escapes or
// literals, for the reason prefix_test.go spells its C1 control that way:
// the source has to show them.
var (
	nbsp             = string(rune(0xa0))
	ideographicSpace = string(rune(0x3000))
)

// branchNameAccepts are full branch names that must keep working. Each is a
// name git itself takes as a new branch (TestValidateBranchNameAgreesWithGit
// holds that to the installed git), and several are here because a stricter
// reading of git's rules would wrongly refuse them.
var branchNameAccepts = []struct{ name, branch string }{
	{"the issue's own valid row", "zvi/old"},
	{"one level", "fix-login"},
	{"a derived shape", "zvi/fix-login-redirect-loop"},
	{"a dot inside a component", "first.last/fix"},
	{"a component ending in a dot, not the last", "zvi./fix"},
	{"a trailing dash", "fix-"},
	{"an underscore", "_fix"},
	{"@ without {", "zvi@home/fix"},
	{"{ without @ before it", "zvi/a{b"},
	{"a lone @ component", "zvi/@"},
	// Measured on git 2.53.0: `git worktree add -b @` makes a branch named
	// "@". The name is odd, and it is git's to allow.
	{"a lone @", "@"},
	{"HEAD as a component", "HEAD/fix"},
	{"HEAD as the last component", "zvi/HEAD"},
	{".lock not at the end", "zvi.lock-1"},
	{"non-ascii is git's business", "zoë/héllo"},
	// herdr trims only the ENDS of the name (see the rejects), so whitespace
	// inside it reaches git intact, and git takes it.
	{"inner no-break space", "zvi/a" + nbsp + "b"},
}

// branchNameRejects covers one name per rule, and checks the reason names
// the rule: the reason is all a user is told about why the form will not
// submit, or why `create` exited 2.
var branchNameRejects = []struct{ name, branch, wantReason string }{
	// The issue's own table (#199), measured with git 2.53.
	{"trailing space", "zvi/old ", "ends with a space"},
	{"leading space", " zvi/old", "begins with a space"},
	{"double dot", "zvi/a..b", `contains ".."`},
	{"tilde", "zvi/a~1", "git forbids"},
	{"colon", "zvi/a:b", "git forbids"},
	{"inner space", "zvi/a b", "contains a space"},
	{"lock suffix", "zvi/old.lock", `ends with ".lock"`},

	// herdr trims the name before anything else reads it (Rust's
	// str::trim, which strips Unicode White_Space), so whitespace at either
	// end is the issue's hazard whether or not git would take it.
	{"trailing no-break space", "zvi/old" + nbsp, "ends with whitespace (U+00A0)"},
	{"leading no-break space", nbsp + "zvi/old", "begins with whitespace (U+00A0)"},
	{"trailing ideographic space", "zvi/old" + ideographicSpace, "ends with whitespace (U+3000)"},
	{"trailing tab", "zvi/old\t", "ends with whitespace (U+0009)"},
	{"trailing newline", "zvi/old\n", "ends with whitespace (U+000A)"},

	// What `git check-ref-format --branch` adds to a refname's rules.
	{"empty", "", "empty"},
	{"leading dash", "-fix", `begins with "-"`},
	{"leading dash that is a real flag", "--focus", `begins with "-"`},
	{"HEAD", "HEAD", `"HEAD"`},

	// Rule 4: control characters, space, ~ ^ :
	{"NUL", "zvi\x00x", "control character"},
	{"C0 control", "zvi\x01x", "control character"},
	{"inner tab", "zvi\tx", "control character"},
	{"DEL", "zvi\x7fx", "control character"},
	// U+0085 is category Cc outside the C0 range git names: the same
	// deliberate widening ValidateBranchPrefix makes.
	{"C1 control", "zvi" + string(rune(0x85)) + "x", "control character"},
	{"caret", "zvi^x", "git forbids"},

	// Rule 5: ? * [
	{"question mark", "zvi?x", "git forbids"},
	{"asterisk", "zvi*x", "git forbids"},
	{"open bracket", "zvi[x", "git forbids"},

	// Rule 10: backslash
	{"backslash", "DOMAIN\\zvi", "git forbids"},

	// Rule 8: "@{"
	{"reflog syntax", "zvi@{0}", `contains "@{"`},
	{"previous branch syntax", "@{-1}", `contains "@{"`},

	// Rule 6: empty components
	{"trailing slash", "zvi/", "empty path component"},
	{"leading slash", "/zvi", "empty path component"},
	{"double slash", "zvi//x", "empty path component"},

	// Rule 1: a component beginning with "." or ending in ".lock"
	{"hidden first component", ".zvi/x", `begins with "."`},
	{"hidden later component", "zvi/.x", `begins with "."`},
	{"dot component", "zvi/./x", `begins with "."`},
	{"lock component", "zvi.lock/x", `ends with ".lock"`},

	// Rule 7: the name may not end with "."
	{"trailing dot", "zvi/x.", `ends with "."`},
	{"trailing dot, one level", "zvi.", `ends with "."`},
}

func TestValidateBranchName_Accepts(t *testing.T) {
	for _, c := range branchNameAccepts {
		if err := ValidateBranchName(c.branch); err != nil {
			t.Errorf("%s: ValidateBranchName(%q) = %v, want nil", c.name, c.branch, err)
		}
	}
}

func TestValidateBranchName_Rejects(t *testing.T) {
	for _, c := range branchNameRejects {
		err := ValidateBranchName(c.branch)
		if err == nil {
			t.Errorf("%s: ValidateBranchName(%q) = nil, want an error", c.name, c.branch)
			continue
		}
		if !strings.Contains(err.Error(), c.wantReason) {
			t.Errorf("%s: reason for %q = %q, want it to mention %q",
				c.name, c.branch, err.Error(), c.wantReason)
		}
	}
}

// TestValidateBranchName_ComponentReasonsLeadWithTheRule: the form draws the
// reason on one panel line and elides it at the tail, and a component can be
// most of a long branch name. Named after the component, the rule was the
// part cut off (#199's review). So the rule comes first.
func TestValidateBranchName_ComponentReasonsLeadWithTheRule(t *testing.T) {
	const long = "eng-1234-fix-the-login-redirect-loop-on-expiry"
	for _, c := range []struct{ branch, lead string }{
		{"zvi/" + long + ".lock", `a path component ends with ".lock"`},
		{"zvi/." + long, `a path component begins with "."`},
	} {
		err := ValidateBranchName(c.branch)
		if err == nil || !strings.HasPrefix(err.Error(), c.lead) {
			t.Errorf("ValidateBranchName(%q) = %v, want a reason beginning %q", c.branch, err, c.lead)
		}
	}
}

// refusedThoughGitAccepts are the names ValidateBranchName refuses on
// purpose although git would make the branch. Each is refused for a reason
// of its own, recorded here so the agreement test below cannot be passed by
// quietly widening this list.
var refusedThoughGitAccepts = map[string]string{
	"zvi/old" + nbsp:                 "herdr trims it to zvi/old",
	nbsp + "zvi/old":                 "herdr trims it to zvi/old",
	"zvi/old" + ideographicSpace:     "herdr trims it to zvi/old",
	"zvi" + string(rune(0x85)) + "x": "the Cc widening ValidateBranchPrefix also makes",
}

// TestValidateBranchNameAgreesWithGit holds the reimplementation to the
// authority it reimplements: `git check-ref-format --branch`, asked inside a
// repository, which is the check `git branch` -- and so herdr's `git
// worktree add -b` -- applies to a new branch's name. Asked outside one it
// would skip the step that reads "@" and "@{-N}" as shorthand, which is not
// the question herdr's git answers.
//
// It runs every name in the two tables above, then every string of one to
// three tokens drawn from the characters the rules turn on, so a rule this
// package got subtly wrong -- an order of checks, an edge of a component --
// shows up as a disagreement rather than waiting for someone to think of
// the case.
func TestValidateBranchNameAgreesWithGit(t *testing.T) {
	repo := mkRepo(t)

	names := map[string]bool{}
	for _, c := range branchNameAccepts {
		names[c.branch] = true
	}
	for _, c := range branchNameRejects {
		names[c.branch] = true
	}
	tokens := []string{"a", ".", "/", "-", "@", "{", "lock", " ", "HEAD"}
	var build func(prefix string, depth int)
	build = func(prefix string, depth int) {
		if depth == 0 {
			return
		}
		for _, tok := range tokens {
			names[prefix+tok] = true
			build(prefix+tok, depth-1)
		}
	}
	build("", 3)
	// An argv element cannot carry a NUL, so git cannot be asked about one;
	// the rejects table pins it on this side alone.
	delete(names, "zvi\x00x")

	type verdict struct {
		name     string
		accepted bool
		err      error
	}
	jobs := make(chan string)
	results := make(chan verdict)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range jobs {
				ok, err := gitAcceptsBranch(repo, name)
				results <- verdict{name, ok, err}
			}
		}()
	}
	go func() {
		for name := range names {
			jobs <- name
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	for v := range results {
		if v.err != nil {
			t.Errorf("git check-ref-format --branch %q: %v", v.name, v.err)
			continue
		}
		ours := ValidateBranchName(v.name)
		if why, deliberate := refusedThoughGitAccepts[v.name]; deliberate {
			if !v.accepted || ours == nil {
				t.Errorf("%q is listed as refused though git accepts it (%s), but git accepted = %v and ValidateBranchName = %v",
					v.name, why, v.accepted, ours)
			}
			continue
		}
		if v.accepted != (ours == nil) {
			t.Errorf("%q: git accepts = %v, ValidateBranchName = %v", v.name, v.accepted, ours)
		}
	}
}

// gitAcceptsBranch asks the installed git whether name can be a new branch
// in repo. Exit 128 is git's "not a valid branch name"; anything else is a
// failure to ask.
func gitAcceptsBranch(repo, name string) (bool, error) {
	cmd := exec.Command("git", "check-ref-format", "--branch", name)
	cmd.Dir = repo
	cmd.Env = gitTestEnv()
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 128 {
		return false, nil
	}
	return false, err
}
