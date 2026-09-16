// Package skill holds the agent-facing document `herdr-draft skill`
// prints, and nothing else. It performs no I/O of its own beyond the
// write Run is handed a writer for, and imports nothing from the rest of
// this module -- the document is prose about a CLI, so the package that
// holds it must not grow a dependency on the code that CLI drives. The
// tests that hold the prose to that code live in package create instead,
// which is the only place the usage string and the prompt-status
// constants are visible (spec 2026-09-16 §8.1).
package skill

import (
	_ "embed"
	"fmt"
	"io"
	"strings"
)

// spawnSkillDoc is the shipped document. Embedded rather than generated so
// the prose is reviewed as prose and reads like the rest of this
// repository's documentation.
//
//go:embed spawn_skill.md
var spawnSkillDoc string

// The two placeholders Render substitutes. Constants because each is
// written in the markdown and read here, and a typo in one of them is not
// a compile error -- it is a document that ships with `{{VERSION}}` in it.
// TestRenderSubstitutesBothPlaceholders' `{{` check is what catches that.
const (
	binPlaceholder     = "{{HERDR_DRAFT_BIN}}"
	versionPlaceholder = "{{VERSION}}"
)

// Render returns the skill document with binPath and version substituted.
//
// binPath is baked in rather than left as a bare command name because the
// binary is NOT on PATH: it lives inside the plugin's install root, which
// herdr owns. Finding that root takes a `herdr plugin list --json` lookup
// (its `plugin_root`; spec §2.2's claim that there was no such lookup is
// corrected there, #167), and the binary printing the document already
// knows the answer, so it says so and no agent has to repeat the lookup.
// The consequence is deliberate: the rendered file is machine-specific,
// and is regenerated after an upgrade, when its version is stale, or after
// a switch between a GitHub install and a linked one, which moves it.
func Render(binPath, version string) string {
	out := strings.ReplaceAll(spawnSkillDoc, binPlaceholder, binPath)
	return strings.ReplaceAll(out, versionPlaceholder, version)
}

// fallbackBinName is what the document says when os.Executable fails --
// rare (it needs a platform that cannot answer at all), and survivable:
// the reader gets a working document naming a command they can put on
// PATH themselves, rather than no document.
const fallbackBinName = "herdr-draft"

// Run is the `skill` verb. It writes the rendered document to stdout and
// returns 0.
//
// exe is os.Executable in production and a fake in tests. When it fails,
// the document is still written -- with fallbackBinName -- and the reason
// goes to STDERR, never stdout: the documented install is
// `herdr-draft skill > ~/.claude/skills/spawn/SKILL.md`, so a warning on
// stdout would land inside the skill file, above its frontmatter fence,
// and break the file this verb exists to produce.
func Run(stdout, stderr io.Writer, exe func() (string, error), version string) int {
	bin, err := exe()
	if err != nil {
		bin = fallbackBinName
		fmt.Fprintf(stderr,
			"herdr-draft skill: could not resolve this binary's own path (%v) -- the document names %q instead; put the binary on PATH under that name, or edit the path in the installed file\n",
			err, fallbackBinName)
	}
	fmt.Fprint(stdout, Render(bin, version))
	return 0
}
