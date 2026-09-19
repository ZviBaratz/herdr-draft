package gitx

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// forbiddenRefRunes are the characters `git check-ref-format` rejects
// anywhere in a refname that are not control characters (those are checked
// separately, so the reason can name the offender): space, "~", "^", ":",
// "?", "*", "[" and "\".
const forbiddenRefRunes = " ~^:?*[\\"

// ValidateBranchPrefix checks a configured branch-name prefix -- the value
// BranchSlug prepends raw, which ends up as the argv element following
// `herdr worktree create --branch`. It returns nil for an acceptable
// prefix; for a rejected one it returns an error whose message is a short
// lower-case reason phrase meant to be shown to the user. The caller adds
// the provenance ("config.toml", ".herdr-draft.toml") and decides what to
// fall back to, so the same rule set serves every source of the value.
//
// It is a free function on a plain string, and lives here rather than in
// internal/config, precisely so it is not welded to one loader: a
// repo-committed .herdr-draft.toml will carry this key too, and there the
// value arrives with `git clone` rather than from the user's own hand.
//
// The reference is `git check-ref-format` (git help check-ref-format,
// whose numbered rules the lists below cite). A *prefix* is not a complete
// refname, though: a slug is appended to it, and the built-in default ends
// in "/". So the rule set is that document's subset which still holds for
// a leading portion of a branch name, plus one rule of our own.
//
// Implemented:
//
//   - Ours, not git's: the prefix may not start with "-". Git accepts a
//     leading dash in a refname, and herdr's own parser is not the hazard
//     either -- it takes the next argv element as --branch's value whatever
//     that value starts with (herdr:src/cli/worktree.rs at v0.9.0). The
//     hazard is what herdr builds next, `git worktree add -b <branch> <path>
//     <base>` (herdr:src/worktree.rs:238-256 at v0.9.0): worktree add
//     accepts the value as -b's argument, then hands it on to a `git branch`
//     child that reads it as an option (measured on git 2.53.0). A `--`
//     would not help; the child parses its own argv. That is the
//     argument-injection surface; the git rules below only bound the rest.
//   - Ours, not git's: the prefix may not begin with whitespace. A prefix
//     begins the branch name, and herdr trims the name it is handed (see
//     ValidateBranchName), so " zvi/" or a leading no-break space -- which
//     git would accept -- would make a branch other than the one derived.
//     Whitespace anywhere else is left to the rules below: a slug always
//     follows, so a prefix never ends the name.
//   - Rule 4: no ASCII control character (NUL included), space, "~", "^"
//     or ":" anywhere. Widened slightly: any Unicode control character
//     (category Cc, so DEL and the C1 block too), not just the C0 range
//     git names.
//   - Rule 5: no "?", "*" or "[" anywhere. herdr-draft never passes a
//     refspec pattern, so git's --refspec-pattern exception cannot apply.
//   - Rule 3: no ".." anywhere.
//   - Rule 8: no "@{" anywhere.
//   - Rule 10: no "\" anywhere.
//   - Rule 1, adapted: no "/"-separated component may begin with "." or
//     end with ".lock". The prefix's own trailing component is merely the
//     start of the branch name's last component, so ending it in ".lock"
//     is not literally what git rejects -- the appended slug would
//     dissolve the suffix. It is rejected anyway: there is no reading of
//     it that is not a mistake, and the branch it would produce is
//     baffling rather than wrong-looking.
//   - Rule 6, in part: no empty component, i.e. no leading "/" and no "//"
//     anywhere. The trailing "/" the next item describes is the sole
//     exception.
//
// Deliberately skipped:
//
//   - Rule 6's "cannot end with /". The built-in default prefix is the
//     lowercased OS username plus "/", so a single trailing "/" is the
//     normal shape; the appended slug fills that component in.
//   - Rule 2, "must contain at least one /". This value becomes a branch
//     under refs/heads/, which git itself checks with --allow-onelevel; an
//     empty prefix (a bare slug) is a legal configuration meaning "no
//     prefix", and a one-level prefix is fine.
//   - Rule 7, "cannot end with a dot". It applies to a complete refname's
//     last component, which a prefix never is: BranchSlug always appends a
//     non-empty slug of [a-z0-9-] beginning with an alphanumeric
//     (SanitizeBranch guarantees it, falling back to "session-xxxxxxxx"
//     when a title sanitizes away to nothing). `git check-ref-format
//     --allow-onelevel 'zvi./foo'` is accepted, and so is this.
//   - Rule 9, "cannot be the single character @", for the same reason: the
//     slug always follows, so the whole refname can never be just "@".
//   - Bytes above 0x7F are not policed. Git refnames are byte strings and
//     check-ref-format accepts non-ASCII (`héllo/x` passes), so a prefix
//     like "zoë/" stays the user's own call.
//
// The empty prefix is valid and means "no prefix": config.Config's default
// supplies the username prefix when the key is absent, so an explicit
// empty value is the only way to ask for an unprefixed branch, and it
// yields a bare slug, which is a legal one-level branch name.
func ValidateBranchPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}

	if r, _ := utf8.DecodeRuneInString(prefix); unicode.IsSpace(r) {
		return fmt.Errorf("begins with %s", describeSpace(r))
	}

	if strings.HasPrefix(prefix, "-") {
		return errors.New(`starts with "-", which git would read as an option rather than a branch name`)
	}

	if err := checkRefChars(prefix); err != nil {
		return err
	}

	components := strings.Split(prefix, "/")
	// A single trailing "/" -- the default prefix's own shape -- leaves an
	// empty last component that the appended slug fills in. Every other
	// empty component is a leading "/" or a "//", which git rejects.
	if last := len(components) - 1; components[last] == "" {
		components = components[:last]
	}
	for _, c := range components {
		switch {
		case c == "":
			return errors.New(`contains an empty path component (a leading "/" or a "//")`)
		case strings.HasPrefix(c, "."):
			return fmt.Errorf(`path component %q begins with "."`, c)
		case strings.HasSuffix(c, ".lock"):
			return fmt.Errorf(`path component %q ends with ".lock"`, c)
		}
	}

	return nil
}

// checkRefChars applies the rules of `git check-ref-format` that hold for
// any stretch of a refname, whole or not: rules 4, 5 and 10's characters,
// anywhere, then rule 3's ".." and rule 8's "@{". ValidateBranchPrefix and
// ValidateBranchName both start from it, so a prefix that fails here fails
// as a branch name too, for the same reason in the same words.
//
// Rule 4 is widened slightly: any Unicode control character (category Cc,
// so the C1 block too), not just the C0 range and DEL git names.
func checkRefChars(s string) error {
	for _, r := range s {
		switch {
		case unicode.IsControl(r):
			return fmt.Errorf("contains the control character %q", r)
		case r == ' ':
			return errors.New("contains a space")
		case strings.ContainsRune(forbiddenRefRunes, r):
			return fmt.Errorf("contains %q, which git forbids in a ref name", r)
		}
	}
	if strings.Contains(s, "..") {
		return errors.New(`contains ".."`)
	}
	if strings.Contains(s, "@{") {
		return errors.New(`contains "@{"`)
	}
	return nil
}
