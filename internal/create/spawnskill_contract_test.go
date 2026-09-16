package create

import (
	"regexp"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/skill"
)

// This file lives in package create, and in no other, for one reason: the
// usage string and the status constants the spawn skill's prose describes
// are unexported. A document naming a flag `create` does not have is worse
// than no document, because an agent will run it and report the failure as
// the user's problem -- so the prose has to be held to the code, and
// internal/skill must not import internal/create to do it. The test goes
// the other way (spec 2026-09-16 §8.1).

// renderedSkill is the document as a user installs it.
func renderedSkill() string { return skill.Render("/x/bin/herdr-draft", "0.0.0-test") }

var flagPattern = regexp.MustCompile(`--[a-z][a-z-]+`)

// TestSkillCoversEveryCreateFlag is the drift guard in both directions. A
// flag the document invents does not exist; a flag it omits is one the
// agent will never use and the user configured for nothing.
//
// The reverse half is scoped to lines that mention `create`, because the
// document legitimately names OTHER commands' flags too -- `herdr agent
// read --source detection` in its last section, for one -- and those are
// not create's to accept. The cost of that scoping is that a create
// invocation split across a backslash continuation is only checked on its
// first line; keep the document's create examples on one line, or accept
// that the tail is unchecked.
func TestSkillCoversEveryCreateFlag(t *testing.T) {
	doc := renderedSkill()

	want := map[string]bool{
		// Accepted (errHelpRequested) but deliberately not listed in
		// createUsage's own flag block, so extraction cannot find it.
		"--help": true,
	}
	for _, f := range flagPattern.FindAllString(createUsage, -1) {
		want[f] = true
	}
	if len(want) < 2 {
		t.Fatal("extracted no flags from createUsage -- the pattern has drifted")
	}

	for f := range want {
		if f == "--help" {
			continue
		}
		if !strings.Contains(doc, f) {
			t.Errorf("create has %s and the skill never mentions it", f)
		}
	}
	for _, line := range strings.Split(doc, "\n") {
		if !strings.Contains(line, "create") {
			continue
		}
		for _, f := range flagPattern.FindAllString(line, -1) {
			if !want[f] {
				t.Errorf("the skill names %s beside `create`, which create does not accept:\n  %s", f, line)
			}
		}
	}
}

// TestSkillNamesEveryPromptStatus pins the three values an agent must
// branch on. Getting one wrong is how a caller resends a prompt that was
// already delivered (#108).
func TestSkillNamesEveryPromptStatus(t *testing.T) {
	doc := renderedSkill()
	for _, s := range []string{promptStatusSent, promptStatusUnsent, promptStatusUnconfirmed} {
		if !strings.Contains(doc, s) {
			t.Errorf("the skill never names prompt_status %q", s)
		}
	}
}

// TestSkillNamesEveryExitCode pins the four a script branches on.
//
// Backticks are stripped first, so a markdown table cell written as
// "| `2` |" counts. The contract this states, and which the document must
// keep: each exit code appears either as "exit N" in prose or as its own
// leading table cell.
func TestSkillNamesEveryExitCode(t *testing.T) {
	doc := strings.ReplaceAll(renderedSkill(), "`", "")
	for _, c := range []string{"0", "1", "2", "3"} {
		if !strings.Contains(doc, "exit "+c) && !strings.Contains(doc, "| "+c+" ") {
			t.Errorf("the skill does not document exit code %s", c)
		}
	}
	// Guards the loop above against the constants being renumbered.
	if ExitOK != 0 || ExitFailed != 1 || ExitUsage != 2 || ExitUnreachable != 3 {
		t.Fatal("create's exit codes moved; this test's literals must move with them")
	}
}

// TestSkillFrontmatter holds the document to the install path the README
// and the document itself tell the user to create. The directory name must
// equal the frontmatter name or the skill does not load.
func TestSkillFrontmatter(t *testing.T) {
	doc := renderedSkill()

	if !strings.HasPrefix(doc, "---\n") {
		t.Fatal("the document does not open with a frontmatter fence")
	}
	end := strings.Index(doc[4:], "\n---\n")
	if end < 0 {
		t.Fatal("the frontmatter fence is not closed")
	}
	fm := doc[4 : 4+end]

	if !strings.Contains(fm, "name: spawn") {
		t.Errorf("frontmatter name is not `spawn`:\n%s", fm)
	}
	desc := ""
	for _, line := range strings.Split(fm, "\n") {
		if strings.HasPrefix(line, "description:") {
			desc = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
	}
	if len(desc) < 80 {
		t.Errorf("description is %d chars; it is the only thing that makes the skill trigger", len(desc))
	}
	// The trigger must fire on the intent, not on the product name, and
	// must disambiguate from the Agent tool's in-process subagents.
	for _, needle := range []string{"hand", "herdr", "subagent"} {
		if !strings.Contains(strings.ToLower(desc), needle) {
			t.Errorf("description never mentions %q: %s", needle, desc)
		}
	}
}
