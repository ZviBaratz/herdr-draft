package gitx

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidateBranchName checks a complete branch name -- the value that
// becomes `herdr worktree create --branch <value>`, whether typed into the
// form's branch input, passed as `create --branch`, taken from a Linear
// issue, or derived by BranchSlug. It returns nil for a name herdr can
// create as asked; for any other it returns an error whose message is a
// short lower-case reason phrase meant to be shown to the user. The caller
// adds where the name came from (#199).
//
// It exists because the duplicate-branch refusal cannot see these names:
// `git show-ref --verify --quiet refs/heads/<name>` answers "absent" (exit
// 1) for a name git could never hold, not an error, so BranchExists passes
// them. Two things then happen inside herdr, both after the refusal that
// should have caught them. herdr trims the name before it does anything
// else (start_api_worktree_create,
// https://github.com/herdrdev/herdr/blob/v0.9.0/src/app/api/worktrees/deferred.rs#L100-L110),
// so "zvi/old " becomes "zvi/old" -- and if that branch exists, herdr checks
// it out and ignores the base, which is exactly the session on old work
// #147's refusal exists to stop. Every other name git refuses fails in
// `git worktree add -b`, at the create's first step, instead of being
// refused before anything is asked of herdr.
//
// The reference is `git check-ref-format --branch`, which is what `git
// branch` applies to a new branch's name and so what herdr's `git worktree
// add -b` meets. TestValidateBranchNameAgreesWithGit holds this
// reimplementation to the installed git; it is reimplemented rather than
// asked because the form checks on every keystroke and a pure function
// needs no round trip. The rules, with git help check-ref-format's
// numbering where it has one:
//
//   - Ours, not git's: no whitespace at either end. herdr's trim is Rust's
//     str::trim, which strips Unicode White_Space, so a trailing no-break
//     space is the same hazard as a trailing space even though git would
//     make a branch of it. unicode.IsSpace is the same set. Whitespace
//     INSIDE the name is not herdr's to change and is left to the rules
//     below, which refuse an ASCII space and let git decide the rest.
//   - --branch's own two: the name may not begin with "-" (git would read
//     it as an option) and may not be exactly "HEAD".
//   - Rules 3, 4, 5, 8 and 10 through checkRefChars, shared with
//     ValidateBranchPrefix, including its one deliberate widening: any
//     Unicode control character is refused, where git names only the C0
//     range and DEL.
//   - Rules 1 and 6: no "/"-separated component may be empty (a leading or
//     trailing "/", or a "//"), begin with ".", or end with ".lock".
//   - Rule 7: the name may not end with ".".
//
// Not refused, because git takes them: "@" on its own (measured on git
// 2.53.0, `git worktree add -b @` makes a branch named "@"), "HEAD" as one
// component of a longer name, and bytes above 0x7F.
//
// The empty name is refused, because git refuses it, and with its own
// error, ErrBranchNameEmpty, because callers word it as a missing name
// rather than a wrong one.
func ValidateBranchName(name string) error {
	if name == "" {
		return ErrBranchNameEmpty
	}
	if r, _ := utf8.DecodeRuneInString(name); unicode.IsSpace(r) {
		return fmt.Errorf("begins with %s", describeSpace(r))
	}
	if r, _ := utf8.DecodeLastRuneInString(name); unicode.IsSpace(r) {
		return fmt.Errorf("ends with %s", describeSpace(r))
	}

	if strings.HasPrefix(name, "-") {
		return errors.New(`begins with "-", which git would read as an option rather than a branch name`)
	}
	if name == "HEAD" {
		return errors.New(`is "HEAD", which git reserves`)
	}

	if err := checkRefChars(name); err != nil {
		return err
	}

	for _, c := range strings.Split(name, "/") {
		switch {
		case c == "":
			return errors.New(`contains an empty path component (a leading or trailing "/", or a "//")`)
		// The rule before the component: the form elides a reason at its
		// tail, and a component can be most of a long name.
		case strings.HasPrefix(c, "."):
			return fmt.Errorf(`a path component begins with "." (%q)`, c)
		case strings.HasSuffix(c, ".lock"):
			return fmt.Errorf(`a path component ends with ".lock" (%q)`, c)
		}
	}
	if strings.HasSuffix(name, ".") {
		return errors.New(`ends with "."`)
	}

	return nil
}

// ErrBranchNameEmpty is ValidateBranchName's answer for "": no name at all,
// which callers say differently from a wrong one ("--branch is empty",
// "branch name required").
var ErrBranchNameEmpty = errors.New("is empty")

// describeSpace names a whitespace rune for a reason phrase: "a space" for
// the one everybody can picture, and the code point for the rest, because a
// no-break space looks exactly like a space and a reason that said "a
// space" about it would send the user looking for something they cannot
// see.
func describeSpace(r rune) string {
	if r == ' ' {
		return "a space"
	}
	return fmt.Sprintf("whitespace (%U)", r)
}
