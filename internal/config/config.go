// Package config loads herdr-draft's plugin configuration and persists its
// small amount of loss-tolerant UI state, per spec §12.
//
// Config is read from $HERDR_PLUGIN_CONFIG_DIR/config.toml. Every key is
// optional -- a missing file, or a file that omits some keys, falls back to
// this package's defaults for whatever it doesn't specify. Unknown keys
// (typos, future additions read by a newer herdr-draft) are silently
// ignored rather than treated as a parse error, so an older binary never
// breaks on a newer config file.
package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/ZviBaratz/herdr-draft/internal/gitx"
)

// configFileName is the config file's name inside $HERDR_PLUGIN_CONFIG_DIR
// (spec §12: "$HERDR_PLUGIN_CONFIG_DIR/config.toml").
const configFileName = "config.toml"

// LinearConfig is the optional `[linear]` table (spec §12).
type LinearConfig struct {
	// APIKeyCmd is a command (argv, no shell) whose stdout is the Linear
	// API key, e.g. ["pass", "show", "linear-api-key"].
	APIKeyCmd []string `toml:"api_key_cmd"`
	// APIKey is the Linear API key given directly in the config file.
	// Spec §12 discourages this in favor of APIKeyCmd, and the file's
	// permissions must be checked (0600) before this key is trusted --
	// that check is the loader's caller's responsibility, not this
	// package's.
	APIKey string `toml:"api_key"`
	// PromptTemplate overrides the built-in commit/PR prompt template
	// (spec §10); empty means "use the built-in default".
	PromptTemplate string `toml:"prompt_template"`
}

// Launch modes for `[clauth] launch`.
//
// ClauthLaunchStart is the DEFAULT, and that is a decision rather than an
// ordering: `clauth start <profile> --` means the same thing on every machine
// that has clauth. The wrapper mode types a bare `claude` into the pane and
// depends on the user's login shell resolving it to a function of their own
// -- where such a function exists it can register a session holder and launch
// through a team-lead helper, and where it does not (which is everywhere by
// default) `claude` is the plain binary: the credential is still isolated by
// CLAUDE_CONFIG_DIR, but everything the wrapper was for is silently gone. A
// default that quietly means something weaker on every machine but its
// author's is the wrong default.
const (
	ClauthLaunchStart   = "start"
	ClauthLaunchWrapper = "wrapper"
)

// ClauthConfig is the optional `[clauth]` table (spec §12).
type ClauthConfig struct {
	// Enabled is a pointer because omitting this key means "auto-detect",
	// which is distinct from explicitly disabling clauth integration.
	Enabled *bool  `toml:"enabled"`
	Default string `toml:"default"`

	// Picker names an executable implementing herdr-draft's account-picker
	// protocol (see internal/picker) -- a command resolved on PATH, or an
	// absolute path. Empty means no picker, which is the default and the
	// normal case.
	//
	// It must be NAMED. herdr-draft never goes looking for one, because
	// finding a program with the expected name is not the same as finding the
	// program that was meant, and a plugin that silently adopted a same-named
	// stranger to route account credentials through would be worse than a
	// plugin with no picker at all. Even a named one is probed once before it
	// is trusted (app.Bootstrap).
	Picker string `toml:"picker"`

	// Launch selects how a pinned account is launched: ClauthLaunchStart (the
	// default) or ClauthLaunchWrapper. See the constants.
	//
	// An unrecognised value falls back to the default with the reason on
	// Config.ClauthLaunchWarning; it never makes Load fail.
	//
	// TWO KEYS, ONE DECISION -- read this with Launcher below, because the
	// pair is the thing that is easy to get wrong. They arrived in separate
	// pull requests (#121 and #122) answering the same question -- how does a
	// pinned account get launched? -- and the reconciliation is deliberate
	// rather than an accident of merge order:
	//
	//	launch   picks the MECHANISM.
	//	launcher configures the argv mechanism, and only that one.
	//
	// They are not one key because the two mechanisms cannot share a
	// representation, which was established by measurement rather than taste.
	// The argv mechanism conveys the account by NAME, as a word in a command
	// line (`clauth start <acct> --`, `claude-as <acct>`). Wrapper mode
	// conveys it as a CREDENTIAL DIRECTORY in the environment, so its argv is
	// the bare word `claude` with the account named nowhere in it -- which
	// validateClauthLauncher rejects, correctly, since a template with no
	// {account} on the argv path would bill the session to whatever profile
	// was already live. And the environment half cannot move into the argv
	// template either: CLIRunner.PaneRun shell-quotes every element (#72), and
	// a quoted `NAME=value` is not an assignment to any POSIX shell -- it is a
	// command name. That is why plan.Op carries RunEnv as a channel of its
	// own.
	//
	// So the two coexist, and Load makes sure only one is ever in effect: in
	// wrapper mode a non-default Launcher is reset to the default and reported
	// on LauncherIgnoredWarning. A user who sets both is told which one lost,
	// which is the whole point -- two keys that both appear to work, where one
	// silently wins, is the failure this arrangement exists to prevent.
	Launch string `toml:"launch"`

	// Launcher is the argv that starts a pinned claude account, with
	// `{account}` standing for the pin. It is a TEMPLATE rather than a
	// prefix to swap because the two useful values do not share a shape:
	// `clauth start <account> --` needs the trailing separator and
	// `claude-as <account>` must not have one. Appending anything on the
	// caller's behalf would corrupt one of them, so the whole argv is the
	// unit and only ExtraArgs follows it.
	//
	// The default is byte-identical to what the plugin built before this
	// key existed. It costs the session its herdmates team lead, because
	// `clauth start` bypasses the `claude()` shell function that sets
	// CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS and launches through
	// herdmates' teammux-launch; `claude-as` is the dotfiles function
	// that keeps both. That is a local choice, not a shareable one --
	// `claude-as` is a shell function, so it resolves only in a pane whose
	// shell sourced those dotfiles, and herdr TYPES this argv into the
	// pane's shell (CLIRunner.PaneRun) rather than exec'ing it, which is
	// the only reason a shell function can be named here at all.
	Launcher []string `toml:"launcher"`

	// LauncherWarning is non-empty when the file's own launcher was
	// rejected and Launcher therefore holds the built-in default instead:
	// the short reason, naming the rejected value. Never decoded from the
	// file (`toml:"-"`) -- it is Load's own output, the same
	// degrade-with-a-reason shape as BranchPrefixWarning.
	LauncherWarning string `toml:"-"`

	// LauncherIgnoredWarning is non-empty when a launcher was set alongside
	// `launch = "wrapper"`, which does not use one. Distinct from
	// LauncherWarning because the causes and the remedies are different: that
	// one means "your template is malformed, fix it", this one means "your
	// template is fine and this mechanism does not take one, pick one of the
	// two". Both are Load's own output, never decoded from the file.
	LauncherIgnoredWarning string `toml:"-"`
}

// sameArgv reports whether two launcher argvs are element-for-element equal.
// Used to tell a launcher the USER set from the built-in default, which is the
// only signal available after decode: toml.Decode overwrites the defaults()
// value in place, so an absent key and a key set to exactly the default are
// indistinguishable -- and rightly so, since ignoring a launcher identical to
// the default changes nothing and is not worth a warning.
func sameArgv(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// AccountPlaceholder is the token ClauthConfig.Launcher substitutes for the
// pinned account. Exported because plan builds the argv and this package
// validates it, and a second spelling of it in either place would be a
// template that silently launches the wrong account.
const AccountPlaceholder = "{account}"

// DefaultClauthLauncher returns the built-in launcher argv. It is a
// function rather than a package-level slice so a caller cannot mutate the
// default for every later call.
func DefaultClauthLauncher() []string {
	return []string{"clauth", "start", AccountPlaceholder, "--"}
}

// validateClauthLauncher returns why argv is unusable as a launcher
// template, or "" when it is fine.
//
// Exactly one AccountPlaceholder is required, and that is the load-bearing
// rule: none would launch the session on whatever account happened to be
// active -- the mis-billing this key exists to fix, arriving silently --
// and more than one would name the account twice in a single command line,
// which no launcher takes. An empty element is rejected because argv[0]
// blank is not a command and a blank element mid-argv is an empty word the
// pane's shell would drop.
func validateClauthLauncher(argv []string) string {
	if len(argv) == 0 {
		return "it is empty"
	}
	n := 0
	for i, a := range argv {
		if strings.TrimSpace(a) == "" {
			return fmt.Sprintf("element %d is empty", i)
		}
		n += strings.Count(a, AccountPlaceholder)
	}
	if n != 1 {
		return fmt.Sprintf("it contains %s %d times, want exactly 1", AccountPlaceholder, n)
	}
	return ""
}

// AgentsConfig is the optional `[agents]` table (spec §12).
type AgentsConfig struct {
	Favorites []string `toml:"favorites"`
	Default   string   `toml:"default"`
	// ExtraArgs is the optional `[agents.extra_args]` sub-table: extra CLI
	// args per agent kind, keyed by agent name.
	ExtraArgs map[string][]string `toml:"extra_args"`
}

// WorktreeConfig is the optional `[worktree]` table.
//
// It is deliberately a table of its own rather than another top-level key
// beside default_worktree: every key here is a herdr worktree-creation
// FLAG, not a form default, and `.herdr-draft.toml`'s allow-list is flat
// by construction so that "a table header in the file is always, by
// construction, something to reject" (repo.go). Putting trust_repository
// in a table therefore makes it unreachable from a cloned repository by
// the same mechanism that already rejects [clauth] and [palette], instead
// of relying on someone remembering to keep it off the allow-list.
type WorktreeConfig struct {
	// TrustRepository adds `--trust-repository` to `herdr worktree
	// create`, which trusts a verified repository for that ONE request
	// rather than changing global git configuration -- herdr 0.9.0
	// (#3044). Absent means false: this relaxes a git safety check, so it
	// is opt-in, and the user opting in is the whole point of the key.
	//
	// A POINTER, unlike default_worktree beside it, because nil and false
	// must stay distinguishable. defaults() gives default_worktree a real
	// `true`, so config.toml genuinely supplies one for every file and
	// attributing it to config.toml is honest. Nothing supplies this key,
	// so a plain bool would make "the file never mentioned it" and "the
	// file set it to false" the same value -- and the resolver would then
	// report `from config.toml` for a file with no [worktree] table at
	// all. Provenance the user can see has to be earned, not assumed.
	//
	// A repository can never set this. See repo.go's repoDeniedKeys.
	TrustRepository *bool `toml:"trust_repository"`
}

// TimeoutsConfig is the optional `[timeouts]` table (spec §12).
type TimeoutsConfig struct {
	DetectionMS  int `toml:"detection_ms"`
	PromptWaitMS int `toml:"prompt_wait_ms"`
	// TrustWaitMS is how long the popup waits for a PERSON to answer a
	// blocking dialog that stopped the launch -- Claude Code's first-run
	// trust prompt, in practice, which every worktree hits the first time
	// (#115).
	//
	// A separate number from DetectionMS on purpose, and not a bigger
	// DetectionMS: that one is tuned for a machine painting a status
	// transition in tens of seconds, and raising it to a human's timescale
	// would make every ordinary detection failure take five minutes to
	// report. Only the popup consults it; headless `create` has nobody at
	// the keyboard and keeps failing fast.
	TrustWaitMS int `toml:"trust_wait_ms"`
}

// Config is herdr-draft's plugin configuration, mirroring spec §12's
// config.toml example field-for-field.
type Config struct {
	// BranchPrefix defaults to the lowercased current user's username plus
	// "/" (spec §12's config.toml comment: `default: lowercased $USER +
	// "/"`). A value Load reads from the file is always one
	// gitx.ValidateBranchPrefix accepted -- see BranchPrefixWarning.
	BranchPrefix     string `toml:"branch_prefix"`
	DefaultWorktree  bool   `toml:"default_worktree"`
	DefaultPlacement string `toml:"default_placement"`

	// BranchPrefixWarning is non-empty when the file's own branch_prefix
	// was rejected by gitx.ValidateBranchPrefix and BranchPrefix therefore
	// holds the built-in default instead: it is the short reason, naming
	// both the rejected value and its replacement. Never decoded from the
	// file (`toml:"-"`) -- it is Load's own output.
	//
	// This is the degrade-with-a-reason shape the rest of the plugin uses
	// for an optional input it cannot trust (app.Setup.LinearUnavailable is
	// the other): the plugin still opens, the feature still works off the
	// default, and the reason travels with the value instead of vanishing.
	// A bad branch_prefix is a typo, not a reason to refuse startup.
	BranchPrefixWarning string `toml:"-"`

	// ClauthLaunchWarning is non-empty when `[clauth] launch` named a mode
	// this binary does not know and ClauthLaunchStart was used instead: the
	// short reason, naming both. Never decoded from the file -- Load's own
	// output, in the same degrade-with-a-reason shape BranchPrefixWarning
	// documents above.
	ClauthLaunchWarning string `toml:"-"`

	Linear   LinearConfig   `toml:"linear"`
	Clauth   ClauthConfig   `toml:"clauth"`
	Agents   AgentsConfig   `toml:"agents"`
	Timeouts TimeoutsConfig `toml:"timeouts"`
	Worktree WorktreeConfig `toml:"worktree"`

	// Palette is the optional `[palette]` table: an escape hatch for
	// overriding herdr theme colors when detection is wrong (spec §7,
	// §12). Nil when the config file omits the table entirely.
	Palette map[string]string `toml:"palette"`
}

// ClauthWarnings is every warning Load wrote about the `[clauth]` table, in
// the order a reader should meet them, or nil when there are none.
//
// It is the ONE list both surfaces read -- `herdr-draft create` prints each on
// stderr and the popup puts them on the account row's panel -- because the
// defect it replaces had exactly the shape a list prevents: #122 set
// ClauthLaunchWarning on a field nothing read, and after that fix the popup
// still read none of the three while create read all of them (#123). A new
// [clauth] warning goes in here, and so reaches both. BranchPrefixWarning is
// not one: it shapes the branch rather than the launch, and lands on a
// different row.
//
// The ignored-launcher warning comes before the malformed-launcher one when
// both fire: whether a template is used at all is the first thing to know
// about what is wrong with it, and a panel short of rows keeps its first note.
func (c Config) ClauthWarnings() []string {
	var out []string
	for _, w := range []string{c.ClauthLaunchWarning, c.Clauth.LauncherIgnoredWarning, c.Clauth.LauncherWarning} {
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}

// defaults returns Config's built-in defaults (spec §12), used for any key
// a config.toml omits -- including the case where the file itself is
// missing entirely.
func defaults() Config {
	return Config{
		Clauth:           ClauthConfig{Launch: ClauthLaunchStart, Launcher: DefaultClauthLauncher()},
		BranchPrefix:     defaultBranchPrefix(),
		DefaultWorktree:  true,
		DefaultPlacement: "new-space",
		Agents: AgentsConfig{
			Favorites: []string{"claude"},
		},
		Timeouts: TimeoutsConfig{
			DetectionMS:  30000,
			PromptWaitMS: 120000,
			TrustWaitMS:  300000,
		},
	}
}

// fallbackBranchPrefix is the last-resort prefix: used when the current OS
// user can't be determined, and when their username makes a prefix even
// gitx.ValidateBranchPrefix won't take.
const fallbackBranchPrefix = "user/"

// defaultBranchPrefix returns the lowercased current OS user's username
// plus "/", or "user/" when the current user can't be determined (e.g. no
// passwd entry in a minimal container).
//
// The username is run through gitx.ValidateBranchPrefix too, because it is
// not guaranteed to be ref-safe either -- a Windows "DOMAIN\user" is the
// obvious case, and this value is the very thing Load falls back TO, so it
// had better not be the one broken prefix nothing checks. Ordinary
// usernames containing "." or "-" pass unchanged.
func defaultBranchPrefix() string {
	u, err := user.Current()
	if err != nil || u.Username == "" {
		return fallbackBranchPrefix
	}
	prefix := strings.ToLower(u.Username) + "/"
	if gitx.ValidateBranchPrefix(prefix) != nil {
		return fallbackBranchPrefix
	}
	return prefix
}

// Load reads $configDir/config.toml and returns the resulting Config, with
// this package's defaults filled in for every key the file omits. A
// missing config file is not an error -- it returns all defaults. Unknown
// keys in the file are ignored rather than rejected (no strict/disallow-
// unknown-fields mode), so an older herdr-draft binary tolerates a newer
// config file.
//
// An unusable branch_prefix falls back to the default the same way an
// omitted one does, with the reason left on BranchPrefixWarning; it never
// makes Load fail. Only a file that cannot be read or parsed does, which
// is what app.Bootstrap turns into spec §9's pre-open refusal.
func Load(configDir string) (Config, error) {
	cfg := defaults()
	defaultPrefix := cfg.BranchPrefix

	// An empty configDir means THERE IS NO CONFIG DIRECTORY -- never the
	// caller's working directory. filepath.Join("", "config.toml") is
	// "config.toml", a relative path, so without this the plugin would
	// read whatever config.toml happens to sit in the directory it was
	// started from: some other project's, parsed as this plugin's, with a
	// parse failure in it refusing to open at all.
	//
	// herdr exports HERDR_PLUGIN_CONFIG_DIR only to a launched PLUGIN, so
	// an unset one is the normal case for anything else -- `just smoke`,
	// the headless verb outside a plugin pane, and a curious person
	// running the binary by hand. This is also the defaults-only entry
	// point this function used to lack, which internal/create had to fake
	// with an empty temporary directory.
	if configDir == "" {
		return cfg, nil
	}

	path := filepath.Join(configDir, configFileName)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("load config: read %s: %w", path, err)
	}

	if _, err := toml.Decode(string(b), &cfg); err != nil {
		return Config{}, fmt.Errorf("load config: parse %s: %w", path, err)
	}

	// The prefix reaches `herdr worktree create --branch <value>` as an
	// argv element by way of gitx.BranchSlug, so it is validated at the
	// point it is first trusted rather than at the point it is used. The
	// same gitx.ValidateBranchPrefix call is what a repo-supplied
	// .herdr-draft.toml will need; only the fallback differs there.
	if verr := gitx.ValidateBranchPrefix(cfg.BranchPrefix); verr != nil {
		cfg.BranchPrefixWarning = fmt.Sprintf("ignoring branch_prefix %q: %v; using %q",
			cfg.BranchPrefix, verr, defaultPrefix)
		cfg.BranchPrefix = defaultPrefix
	}

	// `[clauth] launch` is validated here, at the point it is first trusted,
	// for the same reason branch_prefix is: it decides what gets TYPED into a
	// pane's shell, and an unknown mode must resolve to the conservative one
	// rather than to whatever the zero value happens to be. An empty value is
	// the file omitting the key -- toml.Decode leaves a key the file does not
	// mention untouched, so this arm also covers a [clauth] table with no
	// launch in it -- not an error.
	switch cfg.Clauth.Launch {
	case "":
		cfg.Clauth.Launch = ClauthLaunchStart
	case ClauthLaunchStart, ClauthLaunchWrapper:
	default:
		cfg.ClauthLaunchWarning = fmt.Sprintf("ignoring [clauth] launch %q: expected %q or %q; using %q",
			cfg.Clauth.Launch, ClauthLaunchStart, ClauthLaunchWrapper, ClauthLaunchStart)
		cfg.Clauth.Launch = ClauthLaunchStart
	}

	// Validated here, beside branch_prefix, for the same reason: this argv
	// reaches a command line herdr types into a pane, so it is checked at
	// the point it is first trusted. A bad SHAPE degrades with a reason
	// rather than failing Load, and must never degrade SILENTLY either,
	// since a template with no {account} would bill a session to whichever
	// account was already active.
	//
	// A bad TYPE is different and is NOT handled here: `launcher = "clauth
	// start {account} --"` -- a string, which is what a command line looks
	// like, and the most plausible way to mistype an argv key -- fails in
	// toml.Decode above, so Load returns an error and app.Bootstrap turns
	// it into spec §9's pre-open refusal. That is this loader's behaviour
	// for a wrong type on EVERY key (`branch_prefix = 5` does the same),
	// not something this key introduces, and the refusal names the line
	// and the key. An earlier version of this comment claimed a malformed
	// optional key can never stop the form opening; that is true of a bad
	// value and false of a bad type, and the over-claim is recorded here
	// rather than quietly corrected. Making this one key tolerate a wrong
	// type (decode through `any`, as config/repo.go does for the
	// repository file) would be a real improvement and is deliberately
	// left as its own change, since it belongs to the loader, not to this
	// key.
	//
	// The launcher the file SET is kept aside before the reset: the wrapper
	// check below asks about it too.
	launcher := cfg.Clauth.Launcher
	if verr := validateClauthLauncher(launcher); verr != "" {
		def := DefaultClauthLauncher()
		cfg.Clauth.LauncherWarning = fmt.Sprintf("ignoring [clauth] launcher %v: %s; using %v",
			launcher, verr, def)
		cfg.Clauth.Launcher = def
	}

	// The two keys are ONE decision, and this is where that is enforced --
	// see ClauthConfig.Launch for why they are two keys rather than one.
	// `launch` picks the mechanism; `launcher` configures the argv one. In
	// wrapper mode there is no argv template to configure, so a launcher set
	// alongside it is reset to the default AND reported.
	//
	// Resetting rather than merely warning is what keeps the report true
	// everywhere: wrapper mode falls back to the argv mechanism when the pick
	// answers no config_dir (plan.clauthLaunchCommand), and if the rejected
	// launcher were still in Launcher that fallback would quietly run the very
	// template the user was just told is ignored.
	//
	// It asks about the launcher the file SET, not the one validation left
	// behind. A malformed launcher in wrapper mode is two facts and both are
	// reported: that it is malformed, and that wrapper mode would not have
	// used it anyway -- the second being the one that says which key to
	// delete. Asking the already-reset value found the default, compared it
	// equal to the default, and reported only the first (#123).
	if cfg.Clauth.Launch == ClauthLaunchWrapper && !sameArgv(launcher, DefaultClauthLauncher()) {
		def := DefaultClauthLauncher()
		cfg.Clauth.LauncherIgnoredWarning = fmt.Sprintf(
			"ignoring [clauth] launcher %v: [clauth] launch = %q launches claude under an isolated credential directory rather than through an argv template; using %v",
			launcher, ClauthLaunchWrapper, def)
		cfg.Clauth.Launcher = def
	}
	return cfg, nil
}
