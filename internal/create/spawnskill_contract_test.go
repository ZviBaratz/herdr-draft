package create

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/agentopts"
	"github.com/ZviBaratz/herdr-draft/internal/clauth"
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

// TestSkillNamesEveryExitCode pins the six a script branches on.
//
// Backticks are stripped first, so a markdown table cell written as
// "| `2` |" counts. The contract this states, and which the document must
// keep: each exit code appears either as "exit N" in prose or as its own
// leading table cell.
func TestSkillNamesEveryExitCode(t *testing.T) {
	doc := strings.ReplaceAll(renderedSkill(), "`", "")
	for _, c := range []string{"0", "1", "2", "3", "4", "5"} {
		if !strings.Contains(doc, "exit "+c) && !strings.Contains(doc, "| "+c+" ") {
			t.Errorf("the skill does not document exit code %s", c)
		}
	}
	// Guards the loop above against the constants being renumbered.
	if ExitOK != 0 || ExitFailed != 1 || ExitUsage != 2 || ExitUnreachable != 3 || ExitNothingCreated != 4 || ExitCheckTimedOut != 5 {
		t.Fatal("create's exit codes moved; this test's literals must move with them")
	}
}

// TestSkillNamesTheCheckBudget holds the budgets the document promises to
// the ones the binary keeps.
//
// Each number is a fact in several documents -- this one, README's
// exit-code section, the CHANGELOG entry, the v2 spec's amendments and
// docs/manual-smoke.md's recorded runs -- and was held to the constant by
// nothing: changing preflightCheckDeadline to 300s left the ENTIRE suite
// green. This file exists because a document naming a flag `create` does
// not have is worse than no document, and a document naming a budget it
// does not keep is the same class: an agent that reads "thirty seconds"
// decides how long to wait before concluding the create is stuck.
//
// SIXTY is checked too, since #141: exit 5 stopped being only about git,
// and api_key_cmd's budget is the one an agent waiting on a create is most
// likely to be sitting through.
//
// Only this document is asserted against, because only this one is
// rendered from the binary. The guard below is what sends a maintainer to
// the others -- and it covers only preflightCheckDeadline, because the two
// external budgets live in internal/linear and internal/clauth and are
// unexported. Their own packages hold them to the documents, in
// linear.TestTheBudgetsAreWhatTheDocumentsSay and clauth's sibling; a guard
// that could not read the number would be a comment pretending to be a
// test.
func TestSkillNamesTheCheckBudget(t *testing.T) {
	doc := renderedSkill()
	for _, word := range []string{"thirty seconds", "sixty"} {
		if !strings.Contains(doc, word) {
			t.Errorf("the skill never says %q -- a caller cannot tell how long a create waits before it gives up", word)
		}
	}
	if preflightCheckDeadline != 30*time.Second {
		t.Fatalf("the check budget is now %s. Four other documents name it and no test reads them: "+
			"README.md's exit-code section, the CHANGELOG entry, the v2 spec's #272 amendment, and "+
			"docs/manual-smoke.md's recorded run", preflightCheckDeadline)
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
// ever given. TestSkillRecommendsOnlyAcceptedValues is the other direction.
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
		if _, ok := flagLine(lines, o); !ok {
			var want []string
			for _, c := range o.Choices {
				want = append(want, "`"+c.Value+"`")
			}
			t.Errorf("no line introduces `--%s ...` with every value claude offers: %s", agentopts.FlagName(o.Name), strings.Join(want, ", "))
		}
	}
}

// flagLine finds the line that introduces o's flag and names every value o
// offers, in backticks.
func flagLine(lines []string, o agentopts.Option) (string, bool) {
	flag := "--" + agentopts.FlagName(o.Name)
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
			return line, true
		}
	}
	return "", false
}

// backticked is every `word` on a line, less the flag-shaped ones -- a
// flag's own name is not a value of it.
var backtickedWord = regexp.MustCompile("`([^`\\s]+)`")

func backticked(line string) []string {
	var out []string
	for _, m := range backtickedWord.FindAllStringSubmatch(line, -1) {
		if !strings.HasPrefix(m[1], "-") {
			out = append(out, m[1])
		}
	}
	return out
}

// linesBetween returns the lines from the one starting with from, up to
// and not including the next one starting with to, failing the test when
// either is missing -- the section this test reads has moved, and the test
// must move with it rather than pass on nothing.
func linesBetween(t *testing.T, lines []string, from, to string) []string {
	t.Helper()
	for i, line := range lines {
		if !strings.HasPrefix(line, from) {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], to) {
				return lines[i:j]
			}
		}
		t.Fatalf("no line starting %q after %q -- if the section moved, move this test with it", to, from)
	}
	t.Fatalf("no line starting %q -- if the section moved, move this test with it", from)
	return nil
}

// TestSkillRecommendsOnlyAcceptedValues is the reverse of the test above:
// every value the skill tells an agent to pass must be one create accepts.
// The places it recommends values are the flag lines, the task table's model
// and effort columns, and the permission-mode guidance. A value the
// declaration drops while the document keeps it is a recommendation create
// refuses with exit 2 -- for `xhigh`, every "judgement" spawn (#209's
// review).
//
// Checked with agentopts.Normalize, which is create's own check, so an alias
// the declaration stops offering still passes for the model: create accepts
// it as a typed model id.
func TestSkillRecommendsOnlyAcceptedValues(t *testing.T) {
	lines := strings.Split(renderedSkill(), "\n")
	check := func(option, value, where string) {
		if v, err := agentopts.Normalize("claude", option, value); err != nil || v == "" {
			t.Errorf("%s recommends %s `%s`, which create does not accept: %v", where, option, value, err)
		}
	}

	for _, o := range agentopts.For("claude") {
		line, ok := flagLine(lines, o)
		if !ok {
			continue // TestSkillListsEveryDeclaredValue reports it
		}
		for _, v := range backticked(line) {
			check(o.Name, v, "the --"+agentopts.FlagName(o.Name)+" line")
		}
	}

	rows := 0
	for _, row := range linesBetween(t, lines, "| the task | model | effort |", "Each row down the table")[2:] {
		cells := strings.Split(row, "|")
		if len(cells) < 5 {
			continue
		}
		rows++
		for _, v := range backticked(cells[2]) {
			check("model", v, "the task table")
		}
		for _, v := range backticked(cells[3]) {
			check("effort", v, "the task table")
		}
	}
	if rows == 0 {
		t.Fatal("the task table has no rows -- every assertion on it passed vacuously")
	}

	for _, line := range linesBetween(t, lines, "**Permission mode follows who decides first:**", "These options are claude's.") {
		for _, v := range backticked(line) {
			check("permission_mode", v, "the permission-mode guidance")
		}
	}
}

// snakeToken is a backticked snake_case word -- the shape of every --json
// key, and of nothing else the document writes in backticks.
var snakeToken = regexp.MustCompile("`([a-z][a-z0-9]*(?:_[a-z0-9]+)+)`")

// jsonKeys is every key --json can print, from jsonReport's own tags and
// those of every object nested in it -- account_usage's windows carry
// utilization_pct and resets_at (#215), and a key inside an object is one
// an agent reads as surely as a top-level one.
func jsonKeys() map[string]bool {
	keys := map[string]bool{}
	var walk func(rt reflect.Type)
	walk = func(rt reflect.Type) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice {
			rt = rt.Elem()
		}
		// time.Time is a struct, but it marshals as a string.
		if rt.Kind() != reflect.Struct || rt == reflect.TypeOf(time.Time{}) {
			return
		}
		for i := 0; i < rt.NumField(); i++ {
			name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
			if name != "" && name != "-" {
				keys[name] = true
				walk(rt.Field(i).Type)
			}
		}
	}
	walk(reflect.TypeOf(jsonReport{}))
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

// TestSkillShowsTheWholeArgumentList holds the skill to #219's rule: the
// owner sees every argument the agent will start with before approving.
// TestSkillNamesOnlyRealJSONKeys proves only that a key the skill names is
// real. This proves the skill says to read agent_args and show it at each
// point where it matters: the first dry run's reading list, the preview
// itself, the question when an argument changes permissions, the path where
// the user named everything and no question is asked, and the check after
// the create. Without any one of them, an argument that skips permission
// prompts can be read and never shown, or never read at all.
func TestSkillShowsTheWholeArgumentList(t *testing.T) {
	lines := strings.Split(renderedSkill(), "\n")
	for _, where := range []struct {
		name, from, to string
		want           []string
	}{
		{"the first dry run's reading list", "**1. Dry-run it without the option flags.**", "**2. Choose the three options**", []string{"`agent_args`"}},
		{"the preview", "- in its **preview**", "- in its **description**", []string{"`agent_args`"}},
		{"the rule for an argument that changes permissions", "**Then ask.**", "A label is not reviewable.", []string{"permission", "in the question itself"}},
		{"the path where the user already answered", "Then honour whichever they pick.", "## 8. Read the result", []string{"dry run", "`agent_args`"}},
		{"the check after the create", "## 8. Read the result", "**The ids name the agent, not the space.**", []string{"`agent_args`"}},
	} {
		text := strings.Join(linesBetween(t, lines, where.from, where.to), "\n")
		for _, w := range where.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s never says %q", where.name, w)
			}
		}
	}
}

// thresholdPattern is the skill stating a usage threshold: "at or above
// 95%", or a bare "above 95%" that has lost its "at or".
var thresholdPattern = regexp.MustCompile(`(at or )?above (\d+(?:\.\d+)?%)`)

// TestSkillStatesTheUsageThreshold holds the skill's "nearly out" to the
// popup's (#215): clauth.WarnThreshold, the one constant the account row
// warns at, and warns AT -- the row's comparison is >=. So every threshold
// the skill states is "at or above" that number: a skill that tells a
// spawning agent to worry at 90% while the popup says nothing until 95%,
// or that lets a window sitting at exactly 95% pass, fails here instead
// of in front of a user.
func TestSkillStatesTheUsageThreshold(t *testing.T) {
	want := fmt.Sprintf("%g%%", clauth.WarnThreshold)
	found := thresholdPattern.FindAllStringSubmatch(renderedSkill(), -1)
	if len(found) == 0 {
		t.Fatalf("the skill never states the usage threshold (at or above %s)", want)
	}
	for _, m := range found {
		if m[1] == "" || m[2] != want {
			t.Errorf("the skill says %q; the popup warns at or above %s", m[0], want)
		}
	}
}

// TestSkillWeighsTheAccountUsage holds the skill to #215's rule as #250
// corrected it: the first dry run's account_usage is read, it feeds
// section 5's cost judgement, and a window at the threshold reaches the
// question the user answers -- with the right remedy for the window it is.
//
// The rule's two branches are pinned in ORDER as well as by their words. A
// bare list of phrases pins vocabulary only: #250's review swapped the two
// branches wholesale -- offering another account while the window still had
// room, and a cheaper model once it was spent -- and every phrase the test
// required was still present, so it stayed green.
func TestSkillWeighsTheAccountUsage(t *testing.T) {
	lines := strings.Split(renderedSkill(), "\n")
	for _, where := range []struct {
		name, from, to string
		want           []string
		// order is phrases that must appear, each after the one before it.
		order []string
	}{
		{name: "the first dry run's reading list", from: "**1. Dry-run it without the option flags.**", to: "**2. Choose the three options**", want: []string{"`account_usage`"}},
		{
			name: "section 5's cost judgement",
			from: "**Model and effort follow the shape of the task:**",
			to:   "**Keep the user's own model id.**",
			// Section 5 states the ranking itself. Pointing at section 7 sent
			// the agent to a rule nested under a window branch it is not
			// necessarily in, and "take the lower" here means the lower ROW,
			// which is the move section 7 ranks second (#258).
			want:  []string{"`account_usage`", "limits the model"},
			order: []string{"take the lower", "step the effort down", "before taking a row up"},
		},
		// Its own paragraph, not the whole of "Then ask": that section also
		// says "once, in the question" about the exports.
		{
			name: "the rule for a window at the threshold",
			from: "**If a window in `account_usage`",
			to:   "A label is not reviewable.",
			want: []string{
				"say so in the question", "when it resets",
				// While a label-less window has room, cheaper spends what is
				// left more slowly; at 100% only another account or the reset
				// changes anything, and waiting is not something create does.
				"what you would have picked otherwise",
				"creates nothing",
				// A move the table does not offer is not a move: the mechanical
				// row at `low` has neither, and `haiku` is not an escape from
				// that (#258). Pinned as the negation, because a bare mention
				// of the model survived inverting the rule.
				"`haiku` is in no row of it", "keep that step inside the row's own two efforts",
				"not yours to take lower",
				// An agent told to offer another account needs a way to find
				// one, and a way to tell a candidate from an unknown.
				"`clauth status --json`", "not a candidate", "unknown rather than free",
			},
			// Each phrase occurs once, so the first match is the right one.
			order: []string{
				// A model-labelled window is escaped by another model, whatever
				// its level -- the case #250's first draft broke.
				"label names a model", "escaped by not picking that model",
				// Only a window that caps every model reaches the step-down
				// question at all, and only at 100% does the step down go off.
				"no model in its label", "does not raise the cap",
				"While there is room", "put that cheaper configuration first",
				// The ranking belongs to the has-room branch, between the sentences
				// above and the 100% one below: #258's review moved the whole
				// passage into the 100% branch -- where the document says the step
				// down is off -- and every required phrase was still present.
				"Step the effort down before taking a row up",
				"only when it names a smaller model", "Some configurations have neither move",
				"At 100%", "another account and waiting for the reset",
			},
		},
	} {
		// One line, so a phrase the prose wraps still matches.
		text := strings.Join(strings.Fields(strings.Join(linesBetween(t, lines, where.from, where.to), " ")), " ")
		for _, w := range where.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s never says %q", where.name, w)
			}
		}
		for i, w := range where.order {
			at := strings.Index(text, w)
			if at < 0 {
				t.Errorf("%s never says %q", where.name, w)
				break
			}
			if i > 0 && at < strings.Index(text, where.order[i-1]) {
				t.Errorf("%s says %q before %q; the two branches have swapped", where.name, w, where.order[i-1])
			}
		}
	}
}

// unquotedBracketModel is a --model value with a `[` in it that no quote
// protects: the shape #72 was, typed into a shell.
var unquotedBracketModel = regexp.MustCompile(`--model\s+[^'"\s]\S*\[`)

// TestSkillQuotesBracketedModelIDs holds the one piece of shell the skill
// asks an agent to type that a shell will mangle: a model id carrying a
// variant in brackets, `claude-opus-5[1m]`, which section 5 tells an agent
// to pass as it stands. Unquoted, zsh refuses the whole command ("no matches
// found") and bash replaces it with any file in the working directory the
// pattern matches (#209's tabletop). So the document must show it quoted,
// and must never show it unquoted after --model.
func TestSkillQuotesBracketedModelIDs(t *testing.T) {
	doc := renderedSkill()
	if !strings.Contains(doc, "--model 'claude-opus-5[1m]'") {
		t.Error("the skill never shows a bracketed model id quoted for the shell: --model 'claude-opus-5[1m]'")
	}
	for i, line := range strings.Split(doc, "\n") {
		if unquotedBracketModel.MatchString(line) {
			t.Errorf("line %d passes a bracketed model id unquoted:\n  %s", i+1, line)
		}
	}
}
