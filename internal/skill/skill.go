// Package skill holds the agent-facing document `herdr-draft skill`
// prints, and nothing else. It imports nothing from the rest of this
// module -- the document is prose about a CLI, so the package that
// holds it must not grow a dependency on the code that CLI drives. The
// tests that hold the prose to that code live in package create instead,
// which is the only place the usage string and the prompt-status
// constants are visible (spec 2026-09-16 §8.1).
//
// It performs no I/O beyond what Run is handed: the writers, os.Executable
// and, since `--check` (#311), the one read of the installed file. Each
// arrives as a parameter from package main, which is what keeps every
// path here testable without a filesystem.
package skill

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// spawnSkillDoc is the shipped document. Embedded rather than generated so
// the prose is reviewed as prose and reads like the rest of this
// repository's documentation.
//
//go:embed spawn_skill.md
var spawnSkillDoc string

// The three placeholders Render substitutes. Constants because each is
// written in the markdown and read here, and a typo in one of them is not
// a compile error -- it is a document that ships with `{{VERSION}}` in it.
// TestRenderSubstitutesEveryPlaceholder's `{{` check is what catches that.
const (
	binPlaceholder     = "{{HERDR_DRAFT_BIN}}"
	versionPlaceholder = "{{VERSION}}"
	digestPlaceholder  = "{{DIGEST}}"
)

// Digest identifies the skill's content, which the version cannot (#311):
// within one unreleased version the document changed 26 times in six days
// while every copy of it, current or not, said the same version.
//
// It hashes the embedded SOURCE -- the template, placeholders and all --
// not a render. That is what breaks the circularity of a document
// carrying its own hash: the source holds the literal `{{DIGEST}}`, never
// the digest, so hashing it is well defined, and the footer says what the
// hash covers. It also makes the digest one value per build rather than
// one per machine, which is what lets `herdr-draft version` print it
// without knowing where the reader installed their copy. What it
// deliberately does not cover -- the binary path and the version, the two
// values a render fills in -- `--check` compares directly.
func Digest() string { return digestOf(spawnSkillDoc) }

// digestOf is Digest over any text, so a test can show the digest moves
// with the content without editing the embedded document.
func digestOf(doc string) string {
	sum := sha256.Sum256([]byte(doc))
	return hex.EncodeToString(sum[:])[:12]
}

// Render returns the skill document with binPath and version substituted.
//
// binPath is baked in rather than left as a bare command name because the
// binary is NOT on PATH: it lives inside the plugin's install root, which
// herdr owns. Finding that root takes a `herdr plugin list --json` lookup
// (its `plugin_root`; spawn-skill spec §2.2's claim that there was no
// such lookup is corrected there, #167), and the binary printing the
// document already knows the answer, so it says so and no agent has to
// repeat the lookup. The consequence is deliberate: the rendered file is
// machine-specific, and is regenerated after an upgrade, when its version
// is stale, or after a switch between a GitHub install and a linked one,
// which moves it.
func Render(binPath, version string) string {
	return strings.NewReplacer(
		binPlaceholder, binPath,
		versionPlaceholder, version,
		digestPlaceholder, Digest(),
	).Replace(spawnSkillDoc)
}

// fallbackBinName is what the document says when os.Executable fails --
// rare (it needs a platform that cannot answer at all), and survivable:
// the reader gets a working document naming a command they can put on
// PATH themselves, rather than no document.
const fallbackBinName = "herdr-draft"

// usage is the verb's own, printed on a usage error. Short because the
// verb is: one form prints, one form checks.
const usage = "usage: herdr-draft skill                   print the spawn skill\n" +
	"       herdr-draft skill --check <path>    exit 0 if <path> is what this binary would print,\n" +
	"                                           1 if it is stale, 2 if that cannot be answered\n"

// Run is the `skill` verb. With no arguments it writes the rendered
// document to stdout and returns 0. With `--check <path>` it compares the
// file at path against that same render instead (#311), and returns 0 when
// they match, 1 when they do not, and 2 for a usage error or a file it
// could not read.
//
// exe is os.Executable in production and a fake in tests. When it fails,
// the document is still written -- with fallbackBinName -- and the reason
// goes to STDERR, never stdout: the documented install is
// `herdr-draft skill > ~/.claude/skills/spawn/SKILL.md`, so a warning on
// stdout would land inside the skill file, above its frontmatter fence,
// and break the file this verb exists to produce.
//
// readFile is os.ReadFile in production, and is only called for --check.
func Run(args []string, stdout, stderr io.Writer, exe func() (string, error), readFile func(string) ([]byte, error), version string) int {
	var checkPath string
	switch {
	case len(args) == 0:
	case len(args) == 2 && args[0] == "--check":
		checkPath = args[1]
	case len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help"):
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprint(stderr, usage)
		return 2
	}

	bin, err := exe()
	if err != nil && checkPath != "" {
		// Every render names the binary's path, so without it there is
		// nothing to compare against: checking the fallback name would call
		// a correct copy stale and offer to replace its working path with a
		// bare command name. Unknown is not stale.
		fmt.Fprintf(stderr, "herdr-draft skill --check: could not resolve this binary's own path (%v), which the document names -- no verdict\n", err)
		return 2
	}
	if err != nil {
		bin = fallbackBinName
		fmt.Fprintf(stderr,
			"herdr-draft skill: could not resolve this binary's own path (%v) -- the document names %q instead; put the binary on PATH under that name, or edit the path in the installed file\n",
			err, fallbackBinName)
	}
	if checkPath == "" {
		fmt.Fprint(stdout, Render(bin, version))
		return 0
	}
	return check(stdout, stderr, readFile, checkPath, bin, version)
}

// stampPattern reads the footer's "Generated from" line back out of an
// installed copy, so a stale verdict can say which part moved. A copy
// that predates #311 has the version and no digest, and does not match.
var stampPattern = regexp.MustCompile(`Generated from herdr-draft (\S+), skill ([0-9a-f]+)`)

// check is `--check`. The verdict itself is a byte comparison with the
// render, so nothing the stamp misses can make a stale copy pass; the stamp
// is read only to explain a mismatch. Both verdicts go to stdout, because
// a stale copy is the answer to the question asked, not an error; stderr
// is for the cases with no answer at all.
func check(stdout, stderr io.Writer, readFile func(string) ([]byte, error), path, bin, version string) int {
	installed, err := readFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "herdr-draft skill --check: %v\n", err)
		return 2
	}
	if string(installed) == Render(bin, version) {
		fmt.Fprintf(stdout, "%s: current -- it is what %s prints (herdr-draft %s, skill %s)\n",
			path, bin, version, Digest())
		return 0
	}

	var why []string
	if m := stampPattern.FindStringSubmatch(string(installed)); m == nil {
		why = append(why, "it carries no skill digest, so it was generated before herdr-draft stamped one")
	} else {
		if m[1] != version {
			why = append(why, fmt.Sprintf("it was generated by version %s, and this binary is %s", m[1], version))
		}
		if m[2] != Digest() {
			why = append(why, fmt.Sprintf("it was generated from a different skill source (skill %s, and this binary's is %s)", m[2], Digest()))
		}
	}
	if !strings.Contains(string(installed), `"`+bin+`"`) {
		why = append(why, fmt.Sprintf("it names a different binary path (this binary is %s)", bin))
	}
	if len(why) == 0 {
		why = append(why, "it has been edited since it was generated")
	}
	fmt.Fprintf(stdout, "%s: stale -- %s. Regenerate it:\n    %s skill > %s\n",
		path, strings.Join(why, "; "), shellQuote(bin), shellQuote(path))
	return 1
}

// shellQuote single-quotes s for a POSIX shell. The regenerate command is
// printed to be pasted, and inside double quotes $ and ` still expand.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
