package create

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/ZviBaratz/herdr-draft/internal/agentopts"
	"github.com/ZviBaratz/herdr-draft/internal/skill"
)

// This file lives in package create, and in no other, for one reason: the
// flag set and the status constants the spawn skill's prose describes are
// unexported. A document naming a flag `create` does not have is worse
// than no document, because an agent will run it and report the failure as
// the user's problem -- so the prose has to be held to the code, and
// internal/skill must not import internal/create to do it. The test goes
// the other way (spec 2026-09-16 §8.1).

// renderedSkill is the document as a user installs it.
func renderedSkill() string { return skill.Render("/x/bin/herdr-draft", "0.0.0-test") }

var flagPattern = regexp.MustCompile(`--[a-z][a-z-]+`)

// foreignFlags are the flags of OTHER commands that the document may name.
// It is an allow-list rather than a scoping rule, so that adding a fourth
// entry is a deliberate edit somebody reviews: the failure mode this
// guards is a plausible-looking flag the document invents, and an invented
// flag and a legitimately foreign one are indistinguishable except by
// someone deciding.
//
// --help is create's own, and accepted (errHelpRequested); it is here
// rather than derived because it is the one flag registerFlags does not
// register -- the flag package handles it before any registration is
// consulted.
var foreignFlags = map[string]bool{
	"--help":   true, // create's, but not in its FlagSet
	"--source": true, // herdr agent read
	"--format": true, // herdr agent read
}

// createFlags is every flag create really accepts, from the FlagSet
// itself rather than from createUsage's prose. That distinction is the
// point: a flag registered in parseArgs and left out of the help text is
// the likeliest way a new flag lands, and reading the prose would let it
// through on both sides.
func createFlags(t *testing.T) map[string]bool {
	t.Helper()

	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	var req request
	var worktreeOn, worktreeOff bool
	registerFlags(fs, &req, &worktreeOn, &worktreeOff)

	out := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) { out["--"+f.Name] = true })
	if len(out) == 0 {
		t.Fatal("registerFlags registered nothing -- every assertion below would pass vacuously")
	}
	return out
}

// TestSkillNamesEveryCreateFlag is the forward half of the drift guard: a
// flag the document omits is a flag the agent will never use and the user
// configured for nothing.
//
// Matched on a word boundary rather than with strings.Contains, so that a
// future --base/--base-ref pair cannot mask itself -- the shorter name is
// a prefix of the longer one, and a substring test would call the longer
// one's every mention a sighting of the shorter.
func TestSkillNamesEveryCreateFlag(t *testing.T) {
	doc := renderedSkill()
	seen := map[string]bool{}
	for _, f := range flagPattern.FindAllString(doc, -1) {
		seen[f] = true
	}
	for f := range createFlags(t) {
		if !seen[f] {
			t.Errorf("create has %s and the skill never mentions it", f)
		}
	}
}

// TestSkillInventsNoFlag is the reverse half, and the one that catches a
// document drifting ahead of the binary: every flag token anywhere in the
// prose must be one create accepts, or an explicitly allow-listed flag of
// another command.
//
// It deliberately scans the WHOLE document rather than only the lines
// naming `create`. Almost every flag in the document is mentioned in a
// prose bullet or a table cell, not in an example, so a scoped scan waves
// through exactly the mentions a reader is most likely to act on.
func TestSkillInventsNoFlag(t *testing.T) {
	accepted := createFlags(t)

	for i, line := range strings.Split(renderedSkill(), "\n") {
		for _, f := range flagPattern.FindAllString(line, -1) {
			if accepted[f] || foreignFlags[f] {
				continue
			}
			t.Errorf("line %d names %s, which create does not accept and foreignFlags does not allow:\n  %s", i+1, f, line)
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

// TestSkillNamesEveryExitCode pins the five a script branches on.
//
// Backticks are stripped first, so a markdown table cell written as
// "| `2` |" counts. The contract this states, and which the document must
// keep: each exit code appears either as "exit N" in prose or as its own
// leading table cell.
func TestSkillNamesEveryExitCode(t *testing.T) {
	doc := strings.ReplaceAll(renderedSkill(), "`", "")
	for _, c := range []string{"0", "1", "2", "3", "4"} {
		if !strings.Contains(doc, "exit "+c) && !strings.Contains(doc, "| "+c+" ") {
			t.Errorf("the skill does not document exit code %s", c)
		}
	}
	// Guards the loop above against the constants being renumbered.
	if ExitOK != 0 || ExitFailed != 1 || ExitUsage != 2 || ExitUnreachable != 3 || ExitNothingCreated != 4 {
		t.Fatal("create's exit codes moved; this test's literals must move with them")
	}
}

// frontmatter splits the rendered document into its parsed YAML-ish
// frontmatter and the body.
//
// A hand-rolled `key: value` split rather than a YAML decode, because
// adding a YAML dependency to this module for one test is a worse trade
// than writing eight lines -- and because the properties under test are
// exactly the ones a flat split can check: the fences are there, the keys
// are single-line scalars, and nothing nests.
func frontmatter(t *testing.T, doc string) map[string]string {
	t.Helper()

	if !strings.HasPrefix(doc, "---\n") {
		t.Fatal("the document does not open with a frontmatter fence")
	}
	end := strings.Index(doc[4:], "\n---\n")
	if end < 0 {
		t.Fatal("the frontmatter fence is not closed")
	}

	out := map[string]string{}
	for _, line := range strings.Split(doc[4:4+end], "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("frontmatter line is not `key: value`, so it does not parse:\n  %s", line)
		}
		if strings.TrimSpace(key) != key {
			t.Fatalf("frontmatter key is indented, which nests it under something:\n  %s", line)
		}
		out[key] = strings.TrimSpace(value)
	}
	return out
}

// TestSkillFrontmatter holds the two fields that decide whether the skill
// loads at all and whether it ever fires.
func TestSkillFrontmatter(t *testing.T) {
	fm := frontmatter(t, renderedSkill())

	if fm["name"] != skillName {
		t.Errorf("frontmatter name is %q, want %q", fm["name"], skillName)
	}
	desc := fm["description"]
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

// skillName is the frontmatter name, the install directory, and -- because
// Claude Code requires those two to be equal -- the single fact the three
// tests below hold three files to.
const skillName = "spawn"

// installDirPattern extracts the <dir> from a
// ~/.claude/skills/<dir>/SKILL.md install path.
var installDirPattern = regexp.MustCompile(`~/\.claude/skills/([A-Za-z0-9_-]+)/SKILL\.md`)

// installDirs returns every install directory named in text.
func installDirs(text string) []string {
	var out []string
	for _, m := range installDirPattern.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	return out
}

// TestSkillInstallDirMatchesItsName is §8.1's "equals the install
// directory the README and the document itself tell the user to create".
//
// Claude Code loads a skill from <dir>/SKILL.md and takes its identity
// from the frontmatter `name`; when the two disagree the skill does not
// load, and nothing says so -- there is no error, the skill is simply
// never there. So a rename that touches only one of the three places is a
// silent break, which is the shape this test exists for. The README is
// read from disk rather than embedded for the same reason
// internal/picker/readme_test.go reads it: it is the copy a user follows.
func TestSkillInstallDirMatchesItsName(t *testing.T) {
	doc := renderedSkill()

	if got := frontmatter(t, doc)["name"]; got != skillName {
		t.Errorf("frontmatter name = %q, want %q", got, skillName)
	}

	dirs := installDirs(doc)
	if len(dirs) == 0 {
		t.Fatal("the document never tells the reader where to install it -- if the recipe moved, move this test with it")
	}
	for _, d := range dirs {
		if d != skillName {
			t.Errorf("the document installs into ~/.claude/skills/%s/ but is named %q", d, skillName)
		}
	}

	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	readmeDirs := installDirs(string(readme))
	if len(readmeDirs) == 0 {
		t.Fatal("the README never gives the install path -- if that section moved, move this test with it")
	}
	for _, d := range readmeDirs {
		if d != skillName {
			t.Errorf("the README installs into ~/.claude/skills/%s/ but the skill is named %q", d, skillName)
		}
	}
}

// TestRenderedSkillHasNoFormattingError covers spec §8.2's "no %!-shaped
// formatting error". Render substitutes with ReplaceAll and Run writes
// with Fprint, so there is no format verb to get wrong today -- this is
// what would notice the day somebody reaches for Fprintf and hands it a
// document full of literal % signs.
func TestRenderedSkillHasNoFormattingError(t *testing.T) {
	if i := strings.Index(renderedSkill(), "%!"); i >= 0 {
		t.Errorf("the rendered skill carries a formatting error at byte %d", i)
	}
}

// TestSkillListsEveryDeclaredValue holds the skill's option list to the
// declaration (#209): for each option claude declares, some line of the
// document names its flag and every value it offers, each in backticks. The
// skill tells an agent to CHOOSE among these for every session, so a value
// the declaration gains and the document omits is one no spawned session is
// ever given -- and a value the document keeps after the declaration drops it
// is a flag create refuses with exit 2.
//
// One line, not the whole document, because the whole document names `plan`
// and `auto` in other senses; the line that introduces the flag is where a
// reader looks for what it takes.
func TestSkillListsEveryDeclaredValue(t *testing.T) {
	lines := strings.Split(renderedSkill(), "\n")
	opts := agentopts.For("claude")
	if len(opts) == 0 {
		t.Fatal("claude declares no options -- every assertion below would pass vacuously")
	}
	for _, o := range opts {
		flag := "--" + agentopts.FlagName(o.Name)
		found := false
		for _, line := range lines {
			if !strings.Contains(line, "`"+flag+" ") {
				continue
			}
			all := true
			for _, c := range o.Choices {
				if !strings.Contains(line, "`"+c.Value+"`") {
					all = false
				}
			}
			if all {
				found = true
				break
			}
		}
		if !found {
			var want []string
			for _, c := range o.Choices {
				want = append(want, "`"+c.Value+"`")
			}
			t.Errorf("no line introduces `%s ...` with every value claude offers: %s", flag, strings.Join(want, ", "))
		}
	}
}

// snakeToken is a backticked snake_case word -- the shape of every --json
// key, and of nothing else the document writes in backticks.
var snakeToken = regexp.MustCompile("`([a-z][a-z0-9]*(?:_[a-z0-9]+)+)`")

// jsonKeys is every key --json can print, from jsonReport's own tags.
func jsonKeys() map[string]bool {
	keys := map[string]bool{}
	rt := reflect.TypeOf(jsonReport{})
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			keys[name] = true
		}
	}
	return keys
}

// TestSkillNamesOnlyRealJSONKeys is TestSkillInventsNoFlag's counterpart for
// the report: every backticked snake_case word in the document is a key
// --json prints, a session option's name (the keys inside agent_options and
// launch_options), or a provenance value no tier name covers. The skill's
// §7 and §8 tell an agent which keys to read before and after a create; a
// key the report does not have is one the agent reads as absent, and
// absent already means something for most of them.
func TestSkillNamesOnlyRealJSONKeys(t *testing.T) {
	allowed := jsonKeys()
	if !allowed["prompt_status"] {
		t.Fatal("jsonKeys() did not find prompt_status -- the reflection is wrong, and every assertion below with it")
	}
	for _, name := range agentopts.Names() {
		allowed[name] = true
	}
	for _, v := range []string{provenanceExtraArgs} {
		allowed[v] = true
	}

	for i, line := range strings.Split(renderedSkill(), "\n") {
		for _, m := range snakeToken.FindAllStringSubmatch(line, -1) {
			if !allowed[m[1]] {
				t.Errorf("line %d names `%s`, which is not a --json key, an option name, or a provenance value:\n  %s", i+1, m[1], line)
			}
		}
	}
}
