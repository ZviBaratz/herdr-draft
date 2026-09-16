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
// binary is NOT on PATH and cannot be discovered: it lives inside the
// plugin's install root, which for a marketplace install is
// ~/.config/herdr/plugins/github/<plugin_id>-<hash>/ and carries a hash
// that is not derivable from the plugin id (spec §2.2). The binary
// printing the document is the one thing that reliably knows where it is,
// so it says so. The consequence is deliberate: the rendered file is
// machine-specific and is regenerated after an upgrade or a reinstall
// that moves it.
func Render(binPath, version string) string {
	out := strings.ReplaceAll(spawnSkillDoc, binPlaceholder, binPath)
	return strings.ReplaceAll(out, versionPlaceholder, version)
}
