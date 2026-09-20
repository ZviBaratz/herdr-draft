// async.go is the mechanical half of the app layer's async model (spec
// §8): every debounced/versioned reaction shares the same (version, key)
// staleness-guard shape (request, below), ported as a PATTERN -- not a
// literal line-for-line port -- from Atrium's app/app_branchsearch.go
// (github.com/ZviBaratz/atrium, on this task's audited clean list):
//
//   - Atrium's scheduleBranchSearch/scheduleValidityCheck/
//     scheduleTitleCheck each sleep branchSearchDebounce then emit a
//     "debounce fired" msg carrying the version that was current when
//     scheduling happened; this file's scheduleDirCheck/scheduleBaseCheck/
//     scheduleTitleCheck do the same over debounceDelay, using an
//     injectable Clock.sleep instead of a bare time.Sleep so tests never
//     wait out a real 150ms.
//   - Atrium's own per-source (debounce-msg, result-msg) pairs each carry
//     their own ad hoc versioning/keying fields (branchSearchDebounceMsg's
//     version, titleCheckDebounceMsg's (title, path)); this file collapses
//     that shared shape into one reusable request{version, key} struct,
//     used identically by every source below, per the task brief's own
//     "keep async.go mechanical" guidance.
//   - Atrium's runBranchFetch/branchFetchDoneMsg (a background `git fetch`
//     that unconditionally re-triggers a fresh search on completion,
//     regardless of success -- FetchBranches is best-effort) is ported the
//     same way as runFetchPrune/fetchPruneDoneMsg/handleFetchPruneDone
//     below, feeding gitx.FetchPrune (added in this task) instead of
//     Atrium's own git.FetchBranches.
//
// The staleness rule itself, at both ends of every pipeline, is Atrium's:
// a debounce message whose version no longer matches the source's own
// latest-issued counter means a newer request has already superseded it
// (drop, do no work); a result message whose version no longer matches
// means a newer request landed while this one was in flight (drop, apply
// nothing) -- so a slow subprocess can never overwrite a fresher answer
// with a stale one, and rapid edits within one debounce window coalesce
// into a single fetch.
package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/agentopts"
	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/defaults"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/pathx"
	"github.com/ZviBaratz/herdr-draft/internal/picker"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

// debounceDelay is spec §8's shared 150ms debounce window every versioned
// source in this file waits out before running its real check.
const debounceDelay = 150 * time.Millisecond

// fetchPruneTimeout bounds the background `git fetch --prune` that fires
// once per repository per form open (runFetchPrune, below). It is a safety
// bound rather than a tuning knob, and deliberately not a [timeouts] key:
// gitx's non-interactive environment stops git prompting on the popup's
// own terminal, but it does not stop a configured GIT_ASKPASS/SSH_ASKPASS
// helper, which git still calls and which may be a GUI dialog that never
// answers. 30s is long enough for a slow remote on a slow link, and short
// enough that such a helper does not hold this tea.Cmd's goroutine and its
// git process for the life of the popup.
//
// Read that scope literally: it bounds the goroutine and git. It does NOT
// bound what git spawned. The kill goes to git alone, so an askpass dialog
// or a git-remote-https blocked on a socket is orphaned and keeps running,
// outliving the popup -- measured in review. Reaping those needs a process
// group, which is a change to how gitx starts git and is not this.
const fetchPruneTimeout = 30 * time.Second

// blockingCheckDeadline bounds every check a submit waits for (#202): the
// project row's directory check, the title-duplicate check, the base
// settle and the lane's commit read. Nothing in any of them could time out
// before -- DirExists is os.Stat, gitx.IsGitRepo runs git with no context,
// and the rest were handed context.Background() -- so on a stalled mount
// ⌃S waited silently and for good, with nothing on screen to say why.
//
// Five seconds. Every one of these is an os.Stat plus one or two `git
// rev-parse` calls against the selected project: single-digit milliseconds
// on a filesystem that is working, and seconds only on one that is not, so
// the bound sits far above the working case and still inside the time a
// person will hold a key combination and wonder.
//
// A constant rather than a [timeouts] key, for fetchPruneTimeout's reason:
// it is a safety bound, not a tuning knob, and nothing a user could set it
// to would make a hung mount answer. A key would also inherit #143 --
// [timeouts] values are not validated at all -- which would let a typo
// turn the bound into "immediately" for every check at once.
const blockingCheckDeadline = 5 * time.Second

// maxBaseRefs is spec §6 field 4's "capped at 50" bound on the base-ref
// picker's candidate list.
const maxBaseRefs = 50

// maxBrowseEntries bounds one path-mode directory listing (spec §6 field
// 2), matching atrium's own maxDirEntries. A directory with more
// subdirectories than this stays reachable by typing the target out --
// DirField's literal-path fallback row.
const maxBrowseEntries = 500

// errCheckTimedOut is what a check that did not answer reports where the
// answer is an error rather than a flag on a message -- the lane's base
// read, whose refusal path already existed and now has two reasons to
// take. It says the check did not finish, never that the thing checked
// was bad.
var errCheckTimedOut = errors.New("the check did not answer in time")

// checkBudget is what every check a submit waits for runs under: the
// popup's own Lifetime, so quitting the popup ends it (#249), and the
// deadline on top of that (#202). Read on the update loop and handed to
// awaitCheck, which is where both are applied.
//
// Model.checkDeadline overrides blockingCheckDeadline, which is what lets a
// test wait out a deadline it would otherwise have to sit through; a Model
// that never went through New has none, and gets the constant rather than
// a zero that would time every check out at once.
func (m Model) checkBudget() (*Lifetime, time.Duration) {
	d := m.checkDeadline
	if d <= 0 {
		d = blockingCheckDeadline
	}
	return m.deps.Lifetime, d
}

// AwaitCheck runs answer on its own goroutine and returns what it
// answered, or (zero, false) when ctx runs out first -- the deadline, or
// the caller's own context being cancelled.
//
// It bounds the ANSWER, not the calls, and that is the half that matters:
// the deadline on AwaitCheck's context reaches only the calls that take
// one, and the two that start every directory check -- os.Stat and
// gitx.IsGitRepo -- take none. Nor would giving them one help on the hang
// #202 is about: a process blocked on a stalled network mount is in
// uninterruptible sleep, where a cancelled context (and the KILL behind
// it) returns nothing any sooner than no context at all. Bounding what the
// caller waits for is the only bound that releases the caller.
//
// The price is deliberate and stated: the goroutine is left running on a
// call that may never come back, holding whatever that call holds for the
// life of the process. It answers into a buffered channel nobody reads, so
// it costs one goroutine and no late answer -- the caller has already
// produced the one result it will ever produce, and a stale answer can
// therefore not reach handleDirResult behind the guard's back.
//
// release, when non-nil, is called by the ANSWER's goroutine once the call
// has come back -- never by this one, and that is the difference between
// bounding a check and breaking #211. The popup passes Lifetime.Begin's own
// done: Shutdown's wait exists so a cancelled child is actually dead before
// the process exits -- exec.CommandContext kills it from a watcher
// goroutine that has to be scheduled first -- and this call returns the
// moment the deadline fires, with the child still there. Releasing the
// region here would leave Shutdown nothing to wait for, which is that bug
// with an extra step, for all four checks at once. Found in review.
//
// Exported for internal/create, which has the same questions to bound in
// its headless pre-flight and no Lifetime to bound them on -- it has no
// tea.Program to outlive -- so it passes its own context and a nil release
// (#272). One copy rather than two: each half of the select below is a
// shipped defect's fix, and a duplicated body can drift.
func AwaitCheck[T any](ctx context.Context, release func(), deadline time.Duration, answer func(context.Context) T) (T, bool) {
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	answered := make(chan T, 1)
	go func() {
		if release != nil {
			defer release()
		}
		answered <- answer(ctx)
	}()
	select {
	case v := <-answered:
		return v, true
	case <-ctx.Done():
		// An answer in hand beats a context that is done, and this peek is
		// what says so. `select` chooses uniformly at random among the
		// cases that are ready, so an answer sitting in the channel while
		// the context is also done is a coin flip -- and a check that
		// reports a deadline over an answer it already has refuses a
		// submit for nothing.
		//
		// The peek is what makes the property hold, and it holds
		// unconditionally: the send above happens before anything that
		// could cancel this context from the same goroutine, so a
		// cancellation that has happened implies a value already buffered.
		// `defer cancel()` sitting on THIS function rather than in the
		// goroutine is hygiene on top -- it stops every answer expiring
		// its own context on the way out, which is what turned the
		// coin flip from a boundary case into the common one and shipped
		// once: three unrelated tests failed in their SETUP, on the Linux
		// runner and not the macOS one, at about one run in four. Note
		// what could not find it. `go test -race` is blind to two ready
		// channel operations, and each half of the fix masks the other, so
		// neither is observable alone -- the mutation that reproduces it
		// is both at once, which is what
		// TestAwaitCheck_AnAnswerThatArrivedIsNeverCalledATimeout pins.
		select {
		case v := <-answered:
			return v, true
		default:
		}
		var zero T
		return zero, false
	}
}

// awaitCheck is AwaitCheck over the popup's own Lifetime: quitting the
// popup ends the check with it (#249), and Shutdown still waits for the
// call the quit cancelled (#211), because the region is released by the
// answer's goroutine. Every check a submit waits for goes through it, with
// both halves read on the update loop by checkBudget.
func awaitCheck[T any](lt *Lifetime, deadline time.Duration, answer func(context.Context) T) (T, bool) {
	ctx, done := lt.Begin()
	return AwaitCheck(ctx, done, deadline, answer)
}

// request is the single (version, key) staleness guard every debounced
// source in this file shares -- see the file doc comment. version is what
// the staleness comparison actually uses (compared against the source's
// own monotonically increasing counter, a field on Model); key is the
// input the request was issued for, carried for the message's own
// self-description.
type request struct {
	version int
	key     string
}

// --- directory validity (spec §6 field 2) --------------------------------

type dirDebounceMsg struct{ req request }

type dirResultMsg struct {
	req       request
	dirExists bool
	isGitRepo bool
	// memoryKey is the projects.json key for this directory (v2 spec §10):
	// the repository root when it is a repo, so a linked worktree and its
	// origin share one memory, and the canonical absolute path otherwise.
	// "" when the directory does not exist -- there is nothing to remember
	// about a path that is not there. Resolved in the background alongside
	// the validity check (projectMemoryKey), since it costs a `git
	// rev-parse` and a symlink walk.
	memoryKey string
	// repoConfig is the selected project's committed .herdr-draft.toml
	// (v2 spec §11), read in the same background call for the same reason the
	// memory key is: both hang off the repository root this check already
	// resolves, and both are inputs to the SINGLE defaults.Resolve that
	// applyProjectDefaults runs when this lands.
	//
	// It rides on the dir check's own request/version guard rather than
	// having a source of its own, deliberately: "which repository the form
	// points at" is one question with one answer, and two independently
	// versioned answers to it could land in either order and resolve the
	// defaults twice from a half-updated set of tiers.
	//
	// Zero value for a project that is not a repository, whose root could
	// not be resolved, or that has no such file.
	repoConfig config.RepoConfig
	// timedOut is a check that did not answer inside the deadline (#202):
	// unknown, not a verdict. Every other field is then zero and means
	// nothing -- the check never finished to fill them.
	timedOut bool
	// linkedRoot is the repository's primary checkout when the directory is
	// inside a linked worktree checkout -- a lane -- and "" otherwise (#171). A
	// worktree session from one is created from this root, since herdr
	// refuses the lane itself as the source. It rides the same request for
	// repoConfig's reason: it is one more fact about the same repository.
	linkedRoot string
}

// scheduleDirCheck bumps the directory-validity source's own request
// counter and returns a tea.Cmd that sleeps out debounceDelay before
// reporting the fire -- see runDirCheck for the actual check this leads to.
func (m *Model) scheduleDirCheck(path string) tea.Cmd {
	m.dirReqVersion++
	v := m.dirReqVersion
	// The row says so from here rather than from where the check actually
	// starts (handleDirDebounce), because the hold starts here too: a ⌃S
	// inside the debounce window is held by dirCheckPending just as one
	// during the check itself is, and a row that went quiet for the first
	// 150ms of every wait would be saying the wrong thing about it (#202).
	m.dir.SetValidity(path, form.ValidityChecking)
	clock := m.deps.Clock
	return func() tea.Msg {
		clock.sleep(debounceDelay)
		return dirDebounceMsg{req: request{version: v, key: path}}
	}
}

// runDirCheck performs the actual directory-existence/git-repo check in
// the background, mirroring Atrium's own targetValidity (existence then
// IsGitRepo, in that order -- a non-existent path is never even asked
// whether it's a git repo).
func (m Model) runDirCheck(req request) tea.Cmd {
	git := m.deps.Git
	loadRepoConfig := m.repoConfigLoader()
	// req.key stays the RAW typed text -- DirField.SetValidity keys its own
	// inline marker on it and only renders while it still equals Value().
	// Only the path actually handed to the filesystem is expanded (finding
	// I3): without this, typing "~/Projects/foo" was reported invalid, and
	// on submit `herdr workspace create --cwd` (which, unlike `worktree
	// create`, does no tilde expansion of its own) would have rooted a
	// workspace at a directory literally named "~".
	path := pathx.ExpandTilde(req.key)
	lt, deadline := m.checkBudget()
	return func() tea.Msg {
		res, ok := awaitCheck(lt, deadline, func(ctx context.Context) dirResultMsg {
			exists := git.DirExists(path)
			isRepo := exists && git.IsGitRepo(path)
			// Resolved ONCE and used twice: it is a `git rev-parse`, and
			// both v2 spec §10's memory key and v2 spec §11's committed
			// config are questions about the same repository.
			root := projectRepoRoot(ctx, git, path, exists, isRepo)
			return dirResultMsg{
				req:        req,
				dirExists:  exists,
				isGitRepo:  isRepo,
				memoryKey:  projectMemoryKey(path, exists, root),
				repoConfig: loadRepoConfig(root),
				linkedRoot: projectLinkedRoot(ctx, git, path, root),
			}
		})
		if !ok {
			// Nothing else is filled in: the check never got far enough to
			// know any of it, and an unknown that carried a zeroed verdict
			// would be indistinguishable from a settled one.
			return dirResultMsg{req: req, timedOut: true}
		}
		return res
	}
}

// projectLinkedRoot is the repository's primary checkout when path is inside
// a linked worktree checkout, and "" otherwise -- including when the
// question could not be answered, or has no answer (gitx.PrimaryCheckout),
// which leaves the checkout as the source: herdr then refuses it by name
// (linked_worktree_source), loud, and no worse than before #171. root gates
// it only as "this is a repository at all".
func projectLinkedRoot(ctx context.Context, git gitSource, path, root string) string {
	if root == "" {
		return ""
	}
	primary, err := git.PrimaryCheckout(ctx, path)
	if err != nil {
		return ""
	}
	return primary
}

// projectRepoRoot resolves the ORIGIN repository root behind path, which
// must already be tilde-expanded -- gitx.RepoRoot's answer, derived from
// `--git-common-dir` rather than `--show-toplevel`, so every worktree of
// one repository resolves to the same root.
//
// "" for a directory that does not exist, for a plain non-repository
// (which RepoRoot itself reports as ("", nil) rather than as a failure),
// and for a repository whose root could not be read at all.
func projectRepoRoot(ctx context.Context, git gitSource, path string, exists, isRepo bool) string {
	if !exists || !isRepo {
		return ""
	}
	root, err := git.RepoRoot(ctx, path)
	if err != nil {
		return ""
	}
	return root
}

// projectMemoryKey resolves v2 spec §10's per-project memory key for path,
// which must already be tilde-expanded: the ORIGIN repository root when
// one resolved (projectRepoRoot -- so every worktree of one repository
// shares a single entry rather than accumulating one each), the canonical
// absolute path otherwise.
//
// Both branches end in pathx.CanonicalKey, deliberately: one normalization
// rule for both means a directory cannot acquire two keys by being reached
// once as a repo and once not, or through differently-symlinked parents.
//
// A directory that does not exist gets no key. A repo whose root cannot be
// resolved falls back to the path key rather than losing its memory
// entirely -- a slightly wrong key still remembers something.
func projectMemoryKey(path string, exists bool, repoRoot string) string {
	if !exists {
		return ""
	}
	if repoRoot != "" {
		return pathx.CanonicalKey(repoRoot)
	}
	return pathx.CanonicalKey(path)
}

func (m Model) handleDirDebounce(msg dirDebounceMsg) (Model, tea.Cmd) {
	if msg.req.version != m.dirReqVersion {
		return m, nil // superseded by a newer directory selection
	}
	return m, m.runDirCheck(msg.req)
}

// handleDirResult applies a directory-validity result: DirField's own
// inline (invalid)/(direct) marker, WorktreeField's git-target gate, and
// v2 spec §10's layered defaults re-resolved for the project this directory
// belongs to (applyProjectDefaults, which also owns the worktree on/off
// toggle -- see WorktreeField.SetOn's own doc comment on why that can only
// be applied once the target is known to be a usable git repo).
//
// The defaults are re-applied on EVERY project change, not once per form
// open: the top two tiers are per-project (projects.json and v2 spec §11's
// committed .herdr-draft.toml), so a new project genuinely has a new
// answer. What keeps that from fighting the user is the touched rule
// (Model.worktreeTouched and friends), which replaced the one-shot flag
// this handler used to carry.
func (m Model) handleDirResult(msg dirResultMsg) (Model, tea.Cmd) {
	if msg.req.version != m.dirReqVersion {
		return m, nil // a newer request landed while this one was in flight
	}
	m.dirLandedVersion = msg.req.version

	if msg.timedOut {
		// Unknown, not invalid (#202). Nothing else here runs: every
		// answer this handler applies -- the worktree row's git target,
		// the lane, v2 spec §10's per-project defaults -- would be applying
		// a zero value as though it were a verdict, and the point of the
		// refusal below is that nothing is decided on a guess. The
		// PREVIOUS project's answers stay where they are for the same
		// reason they would after any refusal: they are not used, because
		// checkSubmitValidation stops the submit before they can be.
		m.dirUnknown = true
		m.dir.SetValidity(msg.req.key, form.ValidityUnknown)
		if m.submitHeld {
			// It counts as landed, so the wait is over -- and what the
			// submit finds when it comes back through is its own refusal.
			// Holding on for an answer that already gave up is the
			// forever-wait this issue is about, one layer in.
			return m.handleSubmit()
		}
		return m, nil
	}
	m.dirUnknown = false

	validity := form.ValidityRepo
	switch {
	case !msg.dirExists:
		validity = form.ValidityInvalid
	case !msg.isGitRepo:
		validity = form.ValidityDirect
	}
	m.dir.SetValidity(msg.req.key, validity)
	// dirInvalid mirrors the marker DirField itself is now showing --
	// checkSubmitValidation (app.go, spec §9) reads this directly rather
	// than DirField exposing its own Validity() getter back out.
	m.dirInvalid = validity == form.ValidityInvalid
	m.worktree.SetGitTarget(msg.isGitRepo)
	m.linked = linkedProject{dir: msg.req.key, root: msg.linkedRoot}

	worktreeOnBefore := m.worktree.On()
	// The branch is watched alongside the toggle because the repo tier can
	// move it on its own: a project whose .herdr-draft.toml sets a
	// different branch_prefix, or turns linear_branch_name off, produces a
	// different branch for the same title (see applyProjectDefaults).
	branchBefore := m.worktree.Branch()
	settle := m.applyProjectDefaults(msg.memoryKey, msg.isGitRepo, msg.repoConfig)

	var cmd tea.Cmd
	if m.worktree.On() != worktreeOnBefore || m.worktree.Branch() != branchBefore {
		// A default just flipped the toggle or rewrote the branch --
		// runTitleCheck's own branch-exists half depends on both (it is
		// skipped entirely while worktree is off), so re-run the
		// title-duplicate check now rather than waiting for the next
		// title/branch/dir edit to happen to notice: applyProjectDefaults'
		// own syncDerivedInertness already resynced lastWorktreeOn to the
		// NEW value, and this handler never runs reactToChanges, so its
		// diff would otherwise never see either transition.
		cmd = m.scheduleTitleCheck(m.title.Value(), m.worktree.Branch(), msg.req.key, m.worktree.On())
		// ...and the title panel's resting note follows both for the same
		// reason: it names the branch, and is true only while a worktree
		// is going to be created (see Model.titleNote).
		m.title.SetVerdict(m.title.Value(), m.titleNote(""))
	}
	cmd = tea.Batch(cmd, settle)
	if m.submitHeld {
		// The submit this check was holding goes on from the top, so the
		// validation it waited for runs on these answers (#195) -- or holds
		// again for the base check just scheduled, which is why settle stays
		// in the batch: without it that check never runs and the submit
		// waits for good.
		var submit tea.Cmd
		m, submit = m.handleSubmit()
		cmd = tea.Batch(cmd, submit)
	}
	return m, cmd
}

// --- path-mode directory browsing (spec §6 field 2) ----------------------

type browseDebounceMsg struct{ req request }

type browseResultMsg struct {
	req     request
	entries []string
}

// scheduleBrowse bumps the browse source's own request counter and
// returns a tea.Cmd that sleeps out debounceDelay before reporting the
// fire. dirRaw is the RAW directory portion of the typed text -- expanded
// only in runBrowse, at the moment it actually reaches the filesystem, so
// what is compared against Model.browseDir stays what the user typed.
//
// Bumping here is also what invalidates a listing already in flight, and
// Model.reactToTypedDir bumps this same counter directly (without
// scheduling anything) when the user leaves path mode.
func (m *Model) scheduleBrowse(dirRaw string) tea.Cmd {
	m.browseReqVersion++
	v := m.browseReqVersion
	clock := m.deps.Clock
	return func() tea.Msg {
		clock.sleep(debounceDelay)
		return browseDebounceMsg{req: request{version: v, key: dirRaw}}
	}
}

// runBrowse lists the browsed directory's immediate subdirectories in the
// background. Both halves -- resolving "~/Projects/" to an absolute path
// and reading it -- happen inside the returned Cmd, off the update loop.
func (m Model) runBrowse(req request) tea.Cmd {
	git := m.deps.Git
	return func() tea.Msg {
		dir := git.ResolvePath(req.key)
		return browseResultMsg{req: req, entries: git.ListSubdirs(dir, maxBrowseEntries)}
	}
}

func (m Model) handleBrowseDebounce(msg browseDebounceMsg) (Model, tea.Cmd) {
	if msg.req.version != m.browseReqVersion {
		return m, nil // superseded by a newer directory, or by leaving path mode
	}
	return m, m.runBrowse(msg.req)
}

// handleBrowseResult installs a listing as DirField's candidate pool. An
// EMPTY listing is installed too, not skipped: an unreadable or empty
// directory must clear the previous one's children rather than leave them
// on screen under a path they do not belong to -- DirField's own
// literal-path fallback row is what remains selectable.
//
// It then runs reactToChanges, which every OTHER value-mutating handler in
// this package also does (handleIssueChosen explicitly, handleDirResult by
// hand for the one field it moves). This is the only source that moves
// DirField's SELECTION without a keystroke behind it: installing a pool
// goes through widgets.Picker.SetItems, which re-anchors by item ID and
// falls back to the numeric cursor position when the previous selection is
// no longer on offer -- so Value() can change here. Messages handled in
// this switch bypass routeToForm, so without this call nothing would
// notice: the (invalid)/(direct) marker, WorktreeField's git-target gate,
// the base-ref list and the submit-blocking dirInvalid flag would all keep
// describing the previous selection, and a submit in that window would
// hand herdr a directory nothing had ever checked.
func (m Model) handleBrowseResult(msg browseResultMsg) (Model, tea.Cmd) {
	if msg.req.version != m.browseReqVersion {
		return m, nil // a newer request landed while this one was in flight
	}
	m.supplyDirCandidates(msg.entries)
	return m, tea.Batch(m.reactToChanges()...)
}

// --- base-ref list + once-per-repo git fetch --prune (spec §6 field 4) --

type baseDebounceMsg struct{ req request }

type baseResultMsg struct {
	req  request
	refs []string
	// head is the branch currently checked out in the target repo, for
	// spec §6 field 4's own "row 0 `HEAD (<current branch>)`" (minor M4).
	// "" for a detached HEAD -- gitx.CurrentBranch reports that as an
	// empty name rather than an error -- or when the lookup failed, which
	// is not worth failing the whole base list over: the row simply reads
	// a bare "HEAD", exactly as it always did.
	head string
	err  bool
}

// fetchPruneDoneMsg reports a background `git fetch --prune` finishing --
// keyed by the repo path it fetched, not by request/version: it is
// deliberately not itself debounced or staleness-gated (see
// handleFetchPruneDone), mirroring Atrium's own branchFetchDoneMsg.
type fetchPruneDoneMsg struct{ path string }

func (m *Model) scheduleBaseCheck(path string) tea.Cmd {
	m.baseReqVersion++
	v := m.baseReqVersion
	clock := m.deps.Clock
	return func() tea.Msg {
		clock.sleep(debounceDelay)
		return baseDebounceMsg{req: request{version: v, key: path}}
	}
}

func (m Model) runBaseCheck(req request) tea.Cmd {
	git := m.deps.Git
	path := pathx.ExpandTilde(req.key) // see runDirCheck on why req.key itself stays raw
	return func() tea.Msg {
		if !git.IsGitRepo(path) {
			return baseResultMsg{req: req, err: true}
		}
		refs, err := git.ListBranches(context.Background(), path, maxBaseRefs)
		if err != nil {
			return baseResultMsg{req: req, err: true}
		}
		// Best-effort, in the same subprocess round trip as the ref list:
		// a failure here only costs the HEAD row its parenthetical.
		head, _ := git.CurrentBranch(context.Background(), path)
		return baseResultMsg{req: req, refs: refs, head: head}
	}
}

func (m Model) handleBaseDebounce(msg baseDebounceMsg) (Model, tea.Cmd) {
	if msg.req.version != m.baseReqVersion {
		return m, nil
	}
	return m, m.runBaseCheck(msg.req)
}

// handleBaseResult applies a base-ref list result to WorktreeField, and --
// the first time this particular repo path resolves successfully during
// this form-open -- fires the one-per-repo background `git fetch --prune`
// spec §6 field 4 calls for (fetchedRepos is never reset, so a repo that
// already fetched once this form-open never fetches again even if the
// user navigates away and back).
//
// It is also where the header's context line is refreshed (v2 spec §4:
// "repository name and its current branch"), because this handler is the
// one place the checked-out branch is ever learned. Both outcomes update
// it: a failure means the selected project is not a repository (or its
// refs could not be read), and the previous project's branch must not go
// on being displayed beside the new project's name.
func (m Model) handleBaseResult(msg baseResultMsg) (Model, tea.Cmd) {
	if msg.req.version != m.baseReqVersion {
		return m, nil
	}
	// Claimed here rather than read below, so that a result which takes the
	// error path spends it too: this request is answered either way, and a
	// flag left set for a request that is over would be claimed by whatever
	// list landed next -- which after a project change is the one path that
	// must NOT settle a chosen base (#197).
	relist := msg.req.version == m.relistAfterFetch
	if relist {
		m.relistAfterFetch = 0
	}
	if msg.err {
		m.baseListNote = "couldn't list"
		m.refreshBaseStatus()
		m.worktree.SetHeadBranch("")
		m.refreshFormContext()
		return m, nil
	}

	m.baseItemsVersion++
	m.worktree.SetHeadBranch(msg.head)
	// #212, and it goes immediately before the list is replaced rather than
	// back where the fetch finished. Those are two moments with three
	// subprocess calls between them (runBaseCheck's IsGitRepo, ListBranches
	// and CurrentBranch), and a base picked in between would reach
	// SetBaseItems with no offer under it -- the same hole in a narrower
	// window. "Before the list is applied" is the property that matters;
	// "before the re-list is scheduled" does not imply it.
	var settle tea.Cmd
	if relist {
		settle = m.keepChosenBaseAcrossRelist()
	}
	m.worktree.SetBaseItems(m.baseItemsVersion, msg.refs)
	m.baseListNote = ""
	m.refreshBaseStatus()
	m.refreshFormContext()

	path := msg.req.key
	var cmd tea.Cmd
	if !m.fetchedRepos[path] {
		m.fetchedRepos[path] = true
		cmd = m.runFetchPrune(path)
	}
	return m, tea.Batch(settle, cmd)
}

func (m Model) runFetchPrune(path string) tea.Cmd {
	git := m.deps.Git
	return func() tea.Msg {
		// Best-effort, per gitx.FetchPrune's own doc comment (a remoteless
		// or offline repo simply keeps its local view) -- the error is
		// deliberately discarded here rather than surfaced to the base
		// picker's own status line, matching Atrium's own FetchBranches/
		// branchFetchDoneMsg, which carries no success/failure signal at
		// all: completion alone is what matters, to re-list with whatever
		// the fetch did or didn't change.
		ctx, cancel := context.WithTimeout(context.Background(), fetchPruneTimeout)
		defer cancel()
		_ = git.FetchPrune(ctx, pathx.ExpandTilde(path))
		return fetchPruneDoneMsg{path: path}
	}
}

// handleFetchPruneDone re-runs the base-list check for the SAME path so a
// successful fetch's newly learned remote refs show up, mirroring Atrium's
// own branchFetchDoneMsg handler ("completion always re-triggers a
// search"). Deliberately NOT staleness-gated by request/version -- a path
// the user has since navigated away from (the directory field no longer
// selects it) is instead detected directly by comparing against the
// CURRENT selection, and simply ignored: re-listing a repo that is no
// longer the selected target would be wasted work with nowhere to apply
// the result. No fresh debounce wait is applied here either -- the
// background fetch itself already took real time, so re-listing
// immediately (not queued behind another 150ms) is the responsive choice.
func (m Model) handleFetchPruneDone(msg fetchPruneDoneMsg) (Model, tea.Cmd) {
	if m.dir.Value() != msg.path {
		return m, nil
	}
	m.baseReqVersion++
	// Which re-list is the fetch's, for handleBaseResult to recognise. The
	// version rather than a bool, and it needs no clearing: every later
	// request bumps baseReqVersion past it, so a flag the form navigates
	// away from can never match again.
	m.relistAfterFetch = m.baseReqVersion
	return m, m.runBaseCheck(request{version: m.baseReqVersion, key: msg.path})
}

// keepChosenBaseAcrossRelist is #212, and it runs BEFORE the re-list above
// is even scheduled, for the reason applyProjectDefaults offers a touched
// base across a project change (#197): widgets.Picker keeps a vanished row's
// INDEX, so a list that no longer names the base the user chose hands them
// whichever branch sits there instead -- the worktree row naming it, the
// plan built from it, and nothing saying so.
//
// The offer is what keeps it selected through the re-list, and it is only
// half an answer: a ref can leave the list for two reasons that look
// identical from here. It may have been pruned, and name nothing any more,
// or it may simply have fallen out of the 50 newest branches (maxBaseRefs)
// and be as good a start point as ever. So the offer is followed by the
// settle that tells them apart, and the settle -- not the list -- is what
// decides. A base already on the HEAD row asks nothing: there is no row to
// lose.
//
// This is the one path on which a base the user chose is settled at all.
// A project CHANGE deliberately still does not settle one (#197 keeps it,
// full stop, and applyProjectDefaults' own offer is what carries it over);
// what is settled here is not a tier's default re-applying over the user's
// choice but the single question their choice raised, "does the ref you
// picked out of this list still name a commit in this project".
func (m *Model) keepChosenBaseAcrossRelist() tea.Cmd {
	chosen := m.worktree.Base()
	if !m.baseTouched || chosen == "" {
		return nil
	}
	m.worktree.OfferBase(chosen)
	// Bumped here rather than in a scheduler of its own, and shared with
	// scheduleBaseSettle deliberately: the two ask about the same value, so
	// the later question is the live one, a project change retires an answer
	// to either, and a submit waits for either check (#197) -- which is
	// wanted, since the plan must not be built from a base being re-checked.
	m.baseSettleVersion++
	v := m.baseSettleVersion
	git, dir := m.deps.Git, pathx.ExpandTilde(m.dir.Value())
	lt, deadline := m.checkBudget()
	return func() tea.Msg {
		msg, ok := awaitCheck(lt, deadline, func(ctx context.Context) baseSettledMsg {
			return baseSettledMsg{version: v, chosen: chosen, note: settleChosenBase(ctx, git, dir, chosen)}
		})
		if !ok {
			return baseSettledMsg{version: v, chosen: chosen, timedOut: true}
		}
		return msg
	}
}

// --- a tier's base: does it still name a commit? (#194) ------------------

// baseSettledMsg is SettleBase's answer about the base one resolution
// supplied: that resolution back, with the base dropped to HEAD when it
// does not resolve, and the note saying so. version is baseSettleVersion at
// scheduling time.
type baseSettledMsg struct {
	version  int
	resolved defaults.Resolved
	note     string

	// chosen is the ref when this answer is about the base the USER picked
	// rather than the one a tier supplied (#212, settleChosenBase). The
	// resolution is then not the subject and is left zero: nothing about it
	// changed, and a tier's base must not re-apply over the user's choice.
	// It is kept when note is "" and falls back to the HEAD row when it is
	// not, which is the same rule read off the same field.
	chosen string

	// timedOut is the `git rev-parse` behind either question failing to
	// answer inside the deadline (#202). resolved and note are then zero
	// and mean nothing: the base is neither kept nor dropped, because
	// nobody found out which.
	timedOut bool
}

// scheduleBaseSettle asks, off the update loop, whether the base the current
// resolution supplies names a commit in the project, and returns nil when
// there is nothing to ask: no base, or one the user has already replaced
// with their own. It is not debounced: applyProjectDefaults calls it once
// per dir check, which already was.
//
// The baseTouched half of that is about a TIER's base, which is what this
// asks about and which a user who has chosen for themselves has overridden;
// it is not "the user's base is never checked". Their own base raises its own
// question on its own path -- scheduleChosenBaseSettle, once, when the fetch
// re-lists the branches under it (#212).
//
// Bumping the version here is what retires an answer about an earlier
// resolution, so every call site must come through this rather than build
// the Cmd itself -- and with nothing to ask, the check counts as landed at
// once, so a submit is never held for an answer that is not coming.
func (m *Model) scheduleBaseSettle() tea.Cmd {
	m.baseSettleVersion++
	if m.resolved.BaseRef == "" || m.baseTouched {
		m.baseSettleLanded = m.baseSettleVersion
		// Nothing to ask is also nothing outstanding, so any unknown from
		// the last project's base is over (#202). This decline is the one
		// that latched it: a project change comes through here, and a
		// project whose resolution supplies no base -- or a user who has
		// picked their own -- reaches neither an answer nor a comparison
		// that moves. Found in review.
		m.uncheckedBaseRef = ""
		m.refreshBaseStatus()
		return nil
	}
	v := m.baseSettleVersion
	git, dir, res := m.deps.Git, pathx.ExpandTilde(m.dir.Value()), m.resolved
	lt, deadline := m.checkBudget()
	return func() tea.Msg {
		msg, ok := awaitCheck(lt, deadline, func(ctx context.Context) baseSettledMsg {
			settled, note := SettleBase(ctx, git, dir, res)
			return baseSettledMsg{version: v, resolved: settled, note: note}
		})
		if !ok {
			// No resolution and no note, deliberately: both would say the
			// base was dropped for a reason nobody established (#202).
			return baseSettledMsg{version: v, timedOut: true}
		}
		return msg
	}
}

// handleBaseSettled applies SettleBase's answer: a base that resolves is
// offered in the picker whether or not the branch list names it (rule 1),
// and one that does not becomes the HEAD row, with its note on the worktree
// panel (rule 3). The resolution itself is replaced too, so the provenance
// line stops crediting a tier with a base it no longer supplies.
//
// It also applies settleChosenBase's answer, about the base the USER picked
// (#212, msg.chosen), which is the same two outcomes reached from the other
// side -- and the resolution is deliberately left alone there, since nothing
// about it changed and a tier's base re-applying over the user's choice is
// the one thing that path must not do.
//
// A stale answer -- the project has changed since, and with it the
// resolution -- moves nothing, and neither does any answer about a TIER's
// base once the user has chosen one of their own: noteUserEdits runs before
// this, so their choice has already been recorded and stands. Either way a
// current answer has landed, and a submit held for it goes on (handleSubmit).
func (m Model) handleBaseSettled(msg baseSettledMsg) (Model, tea.Cmd) {
	if msg.version != m.baseSettleVersion {
		return m, nil
	}
	m.baseSettleLanded = msg.version
	if m.baseUnknown() {
		// Any answer that lands for the current question ends whatever
		// unknown the last one left (#202) -- including an answer this
		// switch then drops, since what makes it droppable is that the
		// base it was about is no longer the one on screen.
		//
		// The status line goes with it, and only from here: it is shared
		// with the base LIST check ("couldn't list"), so clearing it on
		// every landing answer would take back a refusal this one knows
		// nothing about. Only the answer that replaces our own unknown
		// owns the line we wrote.
		m.uncheckedBaseRef = ""
		m.refreshBaseStatus()
	}
	switch {
	case msg.chosen != "" && msg.chosen != m.worktree.Base():
		// The user moved the base while the question about the old one was
		// out, and the question they raised went with it. Applying this
		// would take away the ref they have on the strength of a ref they
		// dropped -- #212's own symptom from the other side -- and its note
		// says HEAD is in use, which would be false twice over. This is the
		// tier branch's !baseTouched guard said about the ref instead of the
		// flag, and it is needed separately because that flag is already
		// true on every path that reaches here.
		//
		// The window is a `git rev-parse` wide and #195's hold parks the
		// user inside it, which is what makes this the likely case rather
		// than the rare one.
	case msg.chosen == "" && m.baseTouched:
		// A tier's answer reaching a user who has since picked their own
		// base: dropped, exactly as the !m.baseTouched guard below always
		// dropped it. It is spelled out as its own case only so the
		// timed-out one underneath inherits it -- an unknown reported
		// about a base the form no longer offers would refuse a submit
		// over a ref nobody can see.
	case msg.timedOut:
		// Unknown, not dropped. The base stays exactly as it is and the
		// submit is refused (checkSubmitValidation) rather than sent to
		// branch from a ref that may name nothing.
		m.uncheckedBaseRef = cmp.Or(msg.chosen, m.resolved.BaseRef)
		m.refreshBaseStatus()
	case msg.chosen != "":
		// Their own base, so the resolution and its provenance are left
		// exactly as they are: no tier supplies what is on screen either
		// way. Only the ref itself is at stake, and only when it lost.
		m.baseNote = msg.note
		if msg.note != "" {
			m.worktree.SetBase("")
			// And the row goes with it: the ref names nothing, so there is
			// nothing left for that row to select. Withdrawn after the
			// selection has moved rather than before, which is a tidiness
			// and measured not to matter -- SetBase("") already holds the
			// HEAD sentinel and every refresh keeps that by ID -- but the
			// reverse order passes through widgets.Picker's index fallback
			// on its way, and this file should not be the one leaning on
			// where that lands.
			m.worktree.OfferBase("")
		}
		m.showRepoConfig()
		// The app moved the base, not the user -- see snapshotAppliedDefaults.
		m.snapshotAppliedDefaults()
	default:
		// A tier's base, with the user's own choice not in play -- the
		// case the two above have already excluded between them.
		m.resolved = msg.resolved
		m.baseNote = msg.note
		m.worktree.OfferBase(m.resolved.BaseRef)
		m.worktree.SetBase(m.resolved.BaseRef)
		m.showRepoConfig()
		// The app moved the base, not the user -- see snapshotAppliedDefaults.
		m.snapshotAppliedDefaults()
	}
	if m.submitHeld {
		return m.handleSubmit()
	}
	return m, nil
}

// baseUnknown reports whether the base a submit would use is one whose
// check gave up (#202).
//
// A REF compared against the base in play, rather than a flag someone
// clears, and that is the whole of the fix for what an independent review
// found: the base is the one check of the four that can decline to ask.
// scheduleBaseSettle returns no message at all when the resolution
// supplies no base or the user has chosen their own, so an unknown cleared
// only where an ANSWER lands had no way back -- and the line it writes
// says "pick a base", which sets baseTouched, which is exactly the
// condition under which no answer is ever coming. A submit was then
// refused for the life of the popup, which is #202's own defect one layer
// up. Read as a comparison instead, the verdict simply stops applying the
// moment the value moves, which is DirField.SetValidity's own rule for the
// same problem (validityPath against Value()).
//
// RequestedBase, not Base: the app's own answer to "which base", which
// reads "" while a deferred selection is held (#253) and would make an
// unknown about the ref being held look like an unknown about HEAD.
func (m Model) baseUnknown() bool {
	return m.uncheckedBaseRef != "" && m.uncheckedBaseRef == m.worktree.RequestedBase()
}

// refreshBaseStatus recomposes the worktree panel's base status line: the
// ONE place it is written, because it has two sources that know nothing of
// each other. The base LIST check speaks there ("couldn't list", cleared on
// every success) and so does #202's unknown, and the list's unconditional
// clear used to wipe the unknown's line -- leaving ⌃S refused with nothing
// on screen behind it, the review's second finding.
//
// The unknown wins when both have something to say. It is the one refusing
// the submit: a list that could not be read leaves the picker where it was,
// and a base nobody could check stops the create.
func (m *Model) refreshBaseStatus() {
	if m.baseUnknown() {
		m.worktree.SetBaseStatus("couldn't check "+m.uncheckedBaseRef+": pick a base", true)
		return
	}
	m.worktree.SetBaseStatus(m.baseListNote, false)
}

// baseSettlePending reports whether a base check is out: scheduled for the
// current resolution and not yet landed.
func (m Model) baseSettlePending() bool { return m.baseSettleLanded != m.baseSettleVersion }

// --- title duplicate verdict (spec §6 field 3) ---------------------------

type titleDebounceMsg struct {
	req        request
	branch     string
	dir        string
	worktreeOn bool
}

type titleResultMsg struct {
	req          request
	branchExists bool
	labelTaken   bool
	// branch is the branch the check was scheduled for, and branchInvalid
	// its refusal (BranchRefusal) -- nil for a branch that can be made, or
	// for none at all.
	branch        string
	branchInvalid error
	// timedOut is the git half of this check -- BranchExists -- failing to
	// answer inside the deadline (#202). branchExists then means nothing;
	// every other field is still filled, since the rest of the check asks
	// nobody anything.
	timedOut bool
}

// scheduleTitleCheck bumps the title-duplicate source's own request
// counter and captures the CURRENT branch/dir/worktreeOn snapshot at
// scheduling time (not re-read live when the check finally runs) -- the
// same "capture the relevant state at call time" discipline Atrium's own
// runBranchSearch uses for m.newSessionPath.
func (m *Model) scheduleTitleCheck(title, branch, dir string, worktreeOn bool) tea.Cmd {
	m.titleReqVersion++
	v := m.titleReqVersion
	clock := m.deps.Clock
	return func() tea.Msg {
		clock.sleep(debounceDelay)
		return titleDebounceMsg{
			req:        request{version: v, key: title},
			branch:     branch,
			dir:        dir,
			worktreeOn: worktreeOn,
		}
	}
}

// runTitleCheck computes both duplicate verdicts spec §6 field 3 names --
// an existing local/remote branch (gitx.BranchExists, only meaningful when
// worktree is on: a non-worktree session never creates a branch at all)
// and a herdr workspace label collision (workspaceLabelTaken, a pure
// comparison against the workspace list cached once at form-open, spec
// §8: "herdr workspaces/panes ... form-open") -- combining both into one
// result so they land on TitleField's single verdict line together,
// rather than flickering in one at a time.
//
// Whether the branch can be made at all is decided first, and a branch
// that cannot is never looked up (#199): `git show-ref --verify` answers
// "absent" for a name git could never hold, so the duplicate verdict would
// rest on an answer to a question that has none. The refusal rides this
// check rather than being drawn on each keystroke because it is pure but
// not quiet: every prefixed branch passes through "zvi/" as it is typed,
// and "zvi/" is not a branch name. The submit does not wait on it for its
// correctness -- checkSubmitValidation asks BranchRefusal itself.
//
// An empty branch is not judged here at all. It is refused at submit, the
// way an empty title is: the form opens with none, because nobody has typed
// the title it is derived from, and a panel calling that state wrong would
// be a refusal of a form still being filled in.
func (m Model) runTitleCheck(msg titleDebounceMsg) tea.Cmd {
	git := m.deps.Git
	workspaces := m.workspaces
	req := msg.req
	branch, dir, worktreeOn := msg.branch, msg.dir, msg.worktreeOn
	lt, deadline := m.checkBudget()
	return func() tea.Msg {
		labelTaken := workspaceLabelTaken(workspaces, req.key)

		var invalid error
		if branch != "" {
			invalid = BranchRefusal(worktreeOn, branch)
		}
		// Composed before the one question that leaves this process, so a
		// git that hangs costs only the half it was asked about (#202):
		// the label collision and the branch's own name are answered from
		// what the check was handed, and stay answered either way.
		res := titleResultMsg{req: req, labelTaken: labelTaken, branch: branch, branchInvalid: invalid}
		if invalid != nil || !worktreeOn || branch == "" || dir == "" {
			return res
		}
		exists, ok := awaitCheck(lt, deadline, func(ctx context.Context) bool {
			found, err := git.BranchExists(ctx, pathx.ExpandTilde(dir), branch)
			return err == nil && found
		})
		if !ok {
			res.timedOut = true
			return res
		}
		res.branchExists = exists
		return res
	}
}

// workspaceLabelTaken reports whether any of workspaces already carries
// label as its Label -- an empty title never collides (nothing would be
// created with that label yet).
func workspaceLabelTaken(workspaces []herdrc.WorkspaceInfo, label string) bool {
	_, taken := WorkspaceLabelled(workspaces, label)
	return taken
}

// WorkspaceLabelled is workspaceLabelTaken's comparison, returning the
// workspace that carries label. Exported for internal/create, whose
// pre-flight refuses the same duplicate the form's submit does (#147): one
// comparison shared by both is what stops the two refusals drifting, and
// no plan.Input field records the verdict for equivalence_test.go to catch
// them by.
func WorkspaceLabelled(workspaces []herdrc.WorkspaceInfo, label string) (herdrc.WorkspaceInfo, bool) {
	if strings.TrimSpace(label) == "" {
		return herdrc.WorkspaceInfo{}, false
	}
	for _, w := range workspaces {
		if w.Label == label {
			return w, true
		}
	}
	return herdrc.WorkspaceInfo{}, false
}

func (m Model) handleTitleDebounce(msg titleDebounceMsg) (Model, tea.Cmd) {
	if msg.req.version != m.titleReqVersion {
		return m, nil
	}
	return m, m.runTitleCheck(msg)
}

// handleTitleResult applies a duplicate-verdict result to TitleField. The
// version staleness gate already guarantees msg.req.key still equals
// TitleField's own CURRENT Value() here: any title edit since scheduling
// would have bumped titleReqVersion (see reactToChanges), making this
// result stale and dropped above -- so there is nothing further to check
// before calling SetVerdict.
func (m Model) handleTitleResult(msg titleResultMsg) (Model, tea.Cmd) {
	if msg.req.version != m.titleReqVersion {
		return m, nil
	}
	m.titleLandedVersion = msg.req.version
	m.worktree.SetBranchVerdict(msg.branch, branchVerdictText(msg.branchInvalid))
	m.title.SetVerdict(msg.req.key, m.titleNote(titleVerdictText(msg.branchExists, msg.labelTaken, msg.timedOut)))
	// titleDupBlocked mirrors the SAME verdict just pushed above --
	// checkSubmitValidation (app.go, spec §9) reads this directly rather
	// than re-deriving it from TitleField's own (unexported) verdict
	// state. titleDupUnknown is the third state that verdict now has
	// (#202): a check that never answered blocks like a duplicate does,
	// and says something else while doing it. A label collision still
	// counts on a timed-out check -- that half never asked git.
	m.titleDupBlocked = msg.branchExists || msg.labelTaken
	m.titleDupUnknown = msg.timedOut
	if m.submitHeld {
		// The submit this check was holding goes on from the top, so the
		// duplicate refusal it waited for reads this verdict (#137).
		return m.handleSubmit()
	}
	return m, nil
}

// titleVerdictText composes TitleField's verdict message (bounded to 21
// cells by TitleField itself -- see field_title.go's titleVerdictMaxCells)
// from the two duplicate checks spec §6 field 3 names. No literal wording
// is given in the spec beyond the field's own example fixture text, so
// this is this task's own terse phrasing.
// titleNote is what TitleField's panel line actually says: a duplicate
// warning when there is one, and otherwise the RESTING consequence of the
// title as it stands -- v2 spec §4's own mockup, whose title panel reads
// `branch will be zvi/fix-login-redirect-loop` on a form nothing is wrong
// with.
//
// The layering matters in both directions. A duplicate warning always
// wins: it is the one thing that can stop a submit, and burying it under
// a restatement of the branch name would be the panel's worst possible
// failure. And the resting note is not a verdict at all, which is why it
// is composed HERE rather than inside titleVerdictText -- that function
// stays a pure statement about the two duplicate checks.
//
// A session with no worktree creates no branch, so it has no resting note
// to give: the panel is then genuinely empty, which is honest. Nor does a
// branch the submit would refuse (#199): "branch will be zvi/old " would
// promise, in text that reads exactly like the user's existing zvi/old, a
// branch that will not be made. The worktree panel says why.
func (m Model) titleNote(verdict string) string {
	if verdict != "" {
		return verdict
	}
	if !m.worktree.Enabled() || !m.worktree.On() {
		return ""
	}
	if branch := m.worktree.Branch(); branch != "" && BranchRefusal(true, branch) == nil {
		return "branch will be " + branch
	}
	return ""
}

// branchVerdictText is the worktree panel's line for a refused branch, in v2
// spec §6's `<state>  <reason>` shape, or "" for none. An empty branch is
// not a wrong name but a missing one, and says so as the title row does:
// "title required", "branch name required".
func branchVerdictText(invalid error) string {
	switch {
	case invalid == nil:
		return ""
	case errors.Is(invalid, gitx.ErrBranchNameEmpty):
		return "branch name required"
	}
	return "invalid branch name  " + invalid.Error()
}

func titleVerdictText(branchExists, labelTaken, timedOut bool) string {
	switch {
	// First: a check that did not answer knows nothing about the branch,
	// so "branch exists" is not a thing it could also be saying. The label
	// half is still true and still blocks, but this is the more surprising
	// of the two and the one the user has no way to guess at.
	case timedOut:
		return "couldn't check"
	case branchExists && labelTaken:
		return "branch & label in use"
	case branchExists:
		return "branch exists"
	case labelTaken:
		return "label in use"
	default:
		return ""
	}
}

// --- linear: cache-render-then-refresh (spec §8, §10) --------------------

type linearResultMsg struct {
	issues []linear.Issue
	// err is the fetch failure itself, not a flag that one happened. It
	// used to be a bool, which destroyed the reason at the point of
	// failure and left handleLinearResult with nothing to display -- so a
	// revoked or mistyped API key rendered exactly like an empty Linear
	// queue. spec §13 requires a network failure to degrade "to inert
	// with a reason", and there was no reason to be had.
	err error
}

// refreshLinearCmd fetches the viewer's assigned issues over the network
// (spec §10) -- the async half of "render cache first, then async
// refresh"; the cache-render half happens synchronously in New, before the
// form ever renders.
func (m Model) refreshLinearCmd() tea.Cmd {
	src := m.deps.Linear
	if src == nil {
		// Defense in depth alongside New's own Deps.Linear != nil gate on
		// scheduling this at all -- the same posture reloadClauthCmd's nil
		// guard already takes, and it matters more since finding I5: an
		// IssueField now exists in a state where Deps.Linear is nil (the
		// configured-but-broken, present-but-inert one), so "the field
		// exists" is no longer proof that a source does.
		return nil
	}
	return func() tea.Msg {
		issues, err := src.AssignedIssues(context.Background())
		if err != nil {
			return linearResultMsg{err: err}
		}
		return linearResultMsg{issues: issues}
	}
}

// handleLinearResult applies a successful refresh to IssueField and
// persists it as the new cache (spec §10). Both are no-ops when Linear
// isn't configured at all (m.issue == nil) or the fetch failed: a failed
// refresh leaves whatever the cache-rendered (or previously fetched) list
// already showing in place, matching spec §13's "network failures degrade
// ... to inert with a reason; they never block manual-mode creation" (the
// cache-rendered list, however stale, is still usable).
//
// What it no longer does is fail SILENTLY. The list is left alone, as
// above, but the reason now reaches the panel through SetRefreshError --
// which deliberately does not mark the field inert, precisely so the
// stale-but-usable list this function is careful to preserve stays
// pickable. Reporting a network error by removing the user's ability to
// pick an issue would undo the very degradation this comment describes.
//
// A success clears any previous reason, so a transient failure followed by
// a working refresh does not leave a stale explanation on screen.
func (m Model) handleLinearResult(msg linearResultMsg) (Model, tea.Cmd) {
	if m.issue == nil {
		return m, nil
	}
	if msg.err != nil {
		m.issue.SetRefreshError(linearRefreshReason(msg.err))
		return m, nil
	}
	m.issue.SetRefreshError("")
	m.issueItemsVersion++
	m.issue.SetIssues(m.issueItemsVersion, msg.issues)
	m.linearIssues = msg.issues                  // see Model.linearIssues' own doc comment (handleClearRequested's reseed source).
	_ = linear.SaveCache(m.stateDir, msg.issues) // best-effort; state is loss-tolerant (spec §12).
	return m, nil
}

// --- account picker: debounced preview on project change -------------------

// pickerDebounceMsg and pickerPreviewMsg are versioned like every other async
// source in this file: a slow preview for an old project directory must not
// overwrite a fast one for the current directory.
type pickerDebounceMsg struct{ req request }

type pickerPreviewMsg struct {
	req request
	res picker.Result
	err error
}

// schedulePickerPreview debounces a preview call for path, and puts the row
// into its pending state right away so the wait reads as work rather than as
// a glitch.
//
// Returns nil when no picker is configured -- the normal case -- so nothing is
// scheduled at all rather than scheduled and discarded.
func (m *Model) schedulePickerPreview(path string) tea.Cmd {
	if m.deps.Picker == nil || m.account == nil {
		return nil
	}
	m.pickerReqVersion++
	v := m.pickerReqVersion
	clock := m.deps.Clock
	m.account.SetPickerPreview(form.AccountPickerPreview{Pending: true})
	return func() tea.Msg {
		clock.sleep(debounceDelay)
		return pickerDebounceMsg{req: request{version: v, key: path}}
	}
}

// handlePickerDebounce drops a superseded debounce and runs a current one --
// the same two lines handleDirDebounce is.
func (m Model) handlePickerDebounce(msg pickerDebounceMsg) (Model, tea.Cmd) {
	if msg.req.version != m.pickerReqVersion {
		return m, nil // superseded by a newer project directory
	}
	return m, m.runPickerPreview(msg.req)
}

// runPickerPreview asks the picker what it WOULD choose for this project.
//
// --dry-run is the whole contract of a preview: by protocol it writes no
// ledger entry and builds no account directory, so the row can be refreshed on
// every project change without registering intent for sessions that will never
// exist. The commit-time call (Model.ResolveAccount) is the one that does not
// pass it.
func (m Model) runPickerPreview(req request) tea.Cmd {
	src := m.deps.Picker
	if src == nil {
		return nil
	}
	path := pathx.ExpandTilde(req.key)
	lt := m.deps.Lifetime
	return func() tea.Msg {
		// On the app's own context like the commit pick below, though a
		// preview writes no ledger and so leaves nothing behind: a picker
		// that shells out per account is not free, and there is no reason
		// to leave one running for a form that is gone (#211).
		ctx, done := lt.Begin()
		defer done()
		res, err := src.Pick(ctx, path, picker.Options{DryRun: true})
		return pickerPreviewMsg{req: req, res: res, err: err}
	}
}

// handlePickerPreview applies a CURRENT preview to the account row.
func (m Model) handlePickerPreview(msg pickerPreviewMsg) (Model, tea.Cmd) {
	if msg.req.version != m.pickerReqVersion || m.account == nil {
		return m, nil
	}
	if m.submitResolving {
		// A submit is out on its round trip, and the account row says so --
		// `asking the picker…` is the one thing on screen that explains why
		// the form takes no input (#136). A dry-run answer is about the
		// preview, not the pick under way, and must not overwrite it.
		return m, nil
	}
	m.account.SetPickerPreview(previewFrom(msg.res, msg.err))
	return m, nil
}

// previewFrom reduces a picker answer to the plain values the form needs.
//
// A REFUSAL becomes a reason the row shows; anything else -- the picker could
// not be run at all -- becomes one too, because a row that just goes blank has
// told the user nothing.
func previewFrom(res picker.Result, err error) form.AccountPickerPreview {
	if err != nil {
		var refusal *picker.RefusalError
		if errors.As(err, &refusal) {
			return form.AccountPickerPreview{Refusal: refusal.Short()}
		}
		return form.AccountPickerPreview{Refusal: flattenReason(err.Error())}
	}
	return form.AccountPickerPreview{
		Profile:     res.Profile,
		Tier:        res.Tier,
		FiveHourPct: res.Usage.FiveHour,
		WeeklyPct:   res.Usage.Weekly,
		Load1:       res.Machine.Load1,
		NCPU:        res.Machine.NCPU,
		SwapUsedPct: res.Machine.SwapUsedPct,
		Warnings:    flattenWarnings(res.Warnings),
	}
}

// flattenWarnings puts each picker warning on one line, as flattenReason does
// for a refusal: the panel draws one line per warning, and a picker's own
// text may carry newlines. Control characters beyond whitespace are #151's.
func flattenWarnings(ws []string) []string {
	if len(ws) == 0 {
		return nil
	}
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = flattenReason(w)
	}
	return out
}

// --- a lane's base: resolved at submit (#171) -------------------------------

// linkedCommitMsg carries the commit a lane's base names back to the submit it
// was blocking. Unversioned for pickerCommitMsg's reason: one per submit, and
// the submit is waiting for it.
type linkedCommitMsg struct {
	commit string
	err    error
}

// linkedCommitCmd runs Model.ResolveLinkedCommit off-model: it is a `git
// rev-parse`, and Update does no I/O.
func (m Model) linkedCommitCmd() tea.Cmd {
	// Read here, on the update loop, rather than inside the closure: see
	// linkedCommitReq on why the answer's goroutine is the wrong place for
	// it since the deadline existed.
	git := m.deps.Git
	dir, ref, ask := m.linkedCommitReq()
	lt, deadline := m.checkBudget()
	return func() tea.Msg {
		if !ask {
			return linkedCommitMsg{}
		}
		// On the app's context, so everything a submit is waiting for dies
		// with the popup rather than only the pick that motivated the rule
		// (#211), and gives up rather than waiting for good (#202). It
		// writes nothing either way; cancelling it only means a `git
		// rev-parse` exits a moment sooner.
		msg, ok := awaitCheck(lt, deadline, func(ctx context.Context) linkedCommitMsg {
			commit, err := git.ResolveCommit(ctx, dir, ref)
			return linkedCommitMsg{commit: commit, err: err}
		})
		if !ok {
			return linkedCommitMsg{err: errCheckTimedOut}
		}
		return msg
	}
}

// handleLinkedCommit resumes -- or refuses -- the submit the lane's base was
// blocking.
//
// A failure does NOT fall through to a build that leaves the base to herdr:
// from the primary checkout that would resolve it there -- HEAD as the
// primary's commit -- having said nothing, and plan.Build refuses that plan
// anyway. It stops on the worktree row, where another base is one keystroke
// away.
func (m Model) handleLinkedCommit(msg linkedCommitMsg) (Model, tea.Cmd) {
	// The round trip is over; continueSubmit freezes the form again if the
	// `auto` pick follows (#136).
	m.submitResolving = false
	if msg.err != nil {
		// Two reasons, one refusal. "couldn't resolve" is a ref git
		// answered about and rejected; "couldn't check" is git not
		// answering at all (#202), and saying the first of those for the
		// second would name the ref as the problem when the mount is.
		reason := "couldn't resolve "
		if errors.Is(msg.err, errCheckTimedOut) {
			reason = "couldn't check "
		}
		m.worktree.SetBaseStatus(reason+m.linkedBaseRef()+": pick a base", true)
		return m, m.form.FocusByID("worktree")
	}
	return m.WithLinkedCommit(msg.commit).continueSubmit()
}

// --- account picker: the commit-time pick ----------------------------------

// pickerCommitMsg carries the commit-time pick back to the submit pipeline.
// Unversioned, unlike the preview: there is exactly one of these in flight per
// submit, and the submit is already blocked waiting for it.
type pickerCommitMsg struct {
	res picker.Result
	err error
}

// pickerCommitCmd runs Model.ResolveAccount off-model, which is the only place
// the real (non---dry-run) pick may happen: it writes the picker's ledger.
//
// And which is why it runs on the process's Lifetime (#211). The ledger write
// is what stops two sessions being handed the same account, so a pick the user
// cancelled with `esc` must not reach it: the context dies with the popup, and
// picker.CLI runs the picker through exec.CommandContext, so the picker dies
// with the context. A pick that had ALREADY written is unchanged -- the
// protocol has no verb for handing an account back.
func (m Model) pickerCommitCmd() tea.Cmd {
	src := m
	lt := m.deps.Lifetime
	return func() tea.Msg {
		ctx, done := lt.Begin()
		defer done()
		res, err := src.ResolveAccount(ctx)
		return pickerCommitMsg{res: res, err: err}
	}
}

// handlePickerCommit resumes -- or refuses -- the submit the commit pick was
// blocking.
//
// A refusal does NOT fall through to an unpinned launch. `auto` is a request
// to be picked for, and quietly launching under whatever account happened to
// be live, having said nothing, is the exact silent degradation this feature's
// design was amended to forbid. Focus moves to the account row so the manual
// choices are one keystroke away.
func (m Model) handlePickerCommit(msg pickerCommitMsg) (Model, tea.Cmd) {
	m.submitResolving = false // the round trip is over (#136)
	if m.account == nil {
		return m, nil
	}
	if msg.err != nil {
		p := previewFrom(picker.Result{}, msg.err)
		m.account.SetPickerPreview(p)
		m.account.SetVerdict("", "picker: "+p.Refusal)
		return m, m.form.FocusByID("account")
	}
	m.account.SetPickerPreview(previewFrom(msg.res, nil))
	return m.WithAccount(msg.res).beginSubmit()
}

// --- clauth: reload on account focus (spec §8) ----------------------------

// clauthResultMsg is versioned like every other async source in this file
// (fix round 1: rapid re-focus of Account could otherwise let a slow
// reload's result land AFTER a fresher one and silently overwrite it --
// there is no debounce phase for this source, unlike dir/base/title, so
// version is bumped directly by reloadClauthCmd rather than by a separate
// scheduleX; see handleClauthResult's own staleness check).
type clauthResultMsg struct {
	version int
	status  clauth.Status
	err     error
}

// reloadClauthCmd re-loads clauth's status feed -- spec §8 reads clauth
// "form-open + on account focus". The open-time load happens
// synchronously in Bootstrap/New (it gates whether AccountField is even
// constructed, a static precondition that must be known before the form
// renders); this is the focus-triggered reload (see reactToChanges' own
// FocusedID diff).
//
// Returns nil when m.deps.Clauth is nil -- defense in depth alongside
// New's own Deps.Clauth != nil gate on constructing AccountField at all
// (fix round 1: a reviewer reproduced a nil-interface panic here by
// constructing a Model with clauth profiles present but no clauthSource,
// bypassing that gate; both are now closed, but this guard means a future
// caller of reloadClauthCmd that doesn't route through the m.account != nil
// check in reactToChanges still can't panic).
func (m *Model) reloadClauthCmd() tea.Cmd {
	src := m.deps.Clauth
	if src == nil {
		return nil
	}
	m.clauthReqVersion++
	v := m.clauthReqVersion
	return func() tea.Msg {
		st, err := src.Status(context.Background())
		if err != nil {
			return clauthResultMsg{version: v, err: err}
		}
		return clauthResultMsg{version: v, status: st}
	}
}

// handleClauthResult applies a current reload to AccountField -- a no-op
// when the field wasn't constructed at all, or when a fresher reload has
// since been scheduled (msg.version != m.clauthReqVersion -- see
// clauthResultMsg's own doc comment).
//
// What it does with the result depends on which of the two states the row
// is in, and m.clauthUnavailable is which (New built the row from it, and
// nothing else writes the field's own copy):
//
//   - A LIVE row takes a successful reload's profiles and ignores a
//     failure entirely, which is spec §13's "clauth failures degrade,
//     never block" and is exactly what this did before #200. A failure
//     must not make a live row unavailable: SetUnavailable does not clear
//     the pin and accountPin does not consult the unavailable state, so
//     the session would launch under an account the row had stopped
//     showing.
//   - An UNAVAILABLE row is rebuilt from what the reload found -- #200 and
//     its decision comment (2026-09-20). See recoverAccountRow.
func (m Model) handleClauthResult(msg clauthResultMsg) (Model, tea.Cmd) {
	if msg.version != m.clauthReqVersion || m.account == nil {
		return m, nil
	}
	if m.clauthUnavailable != "" {
		return m.recoverAccountRow(msg), nil
	}
	if msg.err != nil {
		return m, nil
	}
	m.account.SetProfiles(msg.status, m.deps.Clock.now())
	m.clauthStatus = msg.status // see Model.clauthStatus' own doc comment (accountAuthBlocked's lookup source).
	return m, nil
}

// recoverAccountRow applies a reload to a row that is currently
// unavailable: clauth was installed and broken when the popup opened, the
// row said so, and focusing it (spec §8) has just asked clauth again.
//
// Until #200 the answer went nowhere. handleClauthResult loaded the
// profiles and nothing cleared the state, so the row kept reading
// `unavailable <the reason from open>` for the rest of the popup -- and
// since #191 an unavailable row ignores input, so none of the profiles it
// had just loaded could be pinned either. Only reopening recovered.
//
// The three outcomes are the owner's decision, in its own words: a
// successful reload clears the state and the row is rebuilt from what it
// loaded; fewer than two profiles -- a state in which the row would never
// have been built at all -- stays unavailable, with a reason saying THAT
// rather than the stale one from open; and a failed reload replaces the
// reason too, because why clauth failed just now is what the user can act
// on and the two can differ (crashed at open, missing afterwards, or the
// reverse).
//
// Every write goes through m.clauthUnavailable as well as the field.
// That is not bookkeeping: ⌃R⌃R rebuilds the whole form through New from
// the Model's own state (handleClearRequested), so a reason written only
// into the field would be replaced by the open-time one on the first
// clear, and a row recovered here would go straight back to unavailable.
// m.clauthStatus is the other half of that -- New's live-row gate is
// `>= 2 profiles`.
func (m Model) recoverAccountRow(msg clauthResultMsg) Model {
	switch {
	case msg.err != nil:
		m.clauthUnavailable = clauthUnavailableReason(msg.err)
	case len(msg.status.Profiles) < 2:
		m.clauthUnavailable = accountTooFewProfilesReason(len(msg.status.Profiles))
	default:
		m.clauthStatus = msg.status
		m.clauthUnavailable = ""
		m.account.SetUnavailable("")
		m.populateAccountRow()
		return m
	}
	m.account.SetUnavailable(m.clauthUnavailable)
	return m
}

// --- submit pipeline (spec §9) --------------------------------------------
//
// This section is the mechanical half of app.go's submit orchestration
// (handleSubmit/checkSubmitValidation/buildPlanInput/startSubmit): the
// Cmd/message plumbing that runs plan.Execute (Task 13) in the
// background and streams its plan.Progress callbacks back into
// updateSubmitting one message at a time, then -- on failure past step 1
// -- runs plan.CleanCheck and, on a CleanMsg, plan.Clean the same way.
//
// plan.Execute's own onProgress callback fires synchronously and
// REPEATEDLY from WITHIN one long blocking call (through every op,
// including any busy retry and the detection/prompt waits); a bubbletea
// Cmd, however, only ever delivers ONE message per invocation. The only
// way to surface each progress event to Update as it happens -- "progress
// must appear incrementally, not all at once at the end," this task's own
// carried requirement -- rather than only the final state once Execute
// returns, is a producer/consumer channel bridge: runSubmitCmd starts
// Execute in a background goroutine that forwards every onProgress call
// onto an unbuffered channel, and waitForSubmitProgress returns a Cmd
// that reads exactly one value off it, re-arming itself (via the message
// it returns) for the next one -- a standard bubbletea idiom for a
// long-running background process with incremental progress.

// submitProgressMsg carries one plan.Progress event from a running
// plan.Execute call, plus the channel handles needed to keep draining the
// rest.
type submitProgressMsg struct {
	progress   plan.Progress
	progressCh <-chan plan.Progress
	resultCh   <-chan plan.ExecResult
}

// submitDoneMsg reports plan.Execute's own final ExecResult, delivered
// once progressCh above has been fully drained (closed).
type submitDoneMsg struct{ result plan.ExecResult }

// runSubmitCmd starts ops running against r in a background goroutine and
// returns the first Cmd of waitForSubmitProgress's self-re-arming chain
// -- see this section's own file-doc comment for why.
func runSubmitCmd(ctx context.Context, r herdrc.Runner, ops []plan.Op, opts plan.ExecOpts) tea.Cmd {
	progressCh := make(chan plan.Progress)
	resultCh := make(chan plan.ExecResult, 1)
	go func() {
		res := plan.Execute(ctx, r, ops, opts, func(p plan.Progress) { progressCh <- p })
		close(progressCh)
		resultCh <- res
	}()
	return waitForSubmitProgress(progressCh, resultCh)
}

// waitForSubmitProgress returns a Cmd that reads exactly one value off
// progressCh, or -- once the producer goroutine has closed it -- the
// final result off resultCh. progressCh is unbuffered, so the producer
// goroutine (runSubmitCmd) naturally blocks between events until this
// side is ready for the next one; no separate backpressure mechanism is
// needed, and no event can ever be dropped.
func waitForSubmitProgress(progressCh <-chan plan.Progress, resultCh <-chan plan.ExecResult) tea.Cmd {
	return func() tea.Msg {
		if p, ok := <-progressCh; ok {
			return submitProgressMsg{progress: p, progressCh: progressCh, resultCh: resultCh}
		}
		return submitDoneMsg{result: <-resultCh}
	}
}

// handleSubmitProgress applies one streamed plan.Progress event to the
// working row list startSubmit seeded (by Index, updating the row it
// originally seeded at StepPending) and re-renders SubmitView, then
// re-arms waitForSubmitProgress for the next event.
//
// Only the STATE (and, on a failure, the detail) is taken from the
// event: the row's label and detail were resolved once, from the op
// itself, and plan.Progress carries nothing that could improve on them.
func (m Model) handleSubmitProgress(msg submitProgressMsg) (Model, tea.Cmd) {
	if i := msg.progress.Index; i >= 0 && i < len(m.submitSteps) {
		step := m.submitSteps[i]
		step.State = msg.progress.State
		switch msg.progress.State {
		case plan.StepFailed:
			step.Detail = submitStepError(msg.progress.Err, msg.progress.Label)
		case plan.StepFailedNonFatal:
			step.Detail = submitStepError(msg.progress.Err, msg.progress.Label)
			if msg.progress.Kind == plan.OpTabRename {
				// The row's seeded detail was the tab's intended name,
				// which is now exactly what it did not get. The prefix is
				// what makes the error read as a consequence rather than a
				// stop: the rows below this one go on running. A worktree
				// step's caveat (#221) already opens with what is wrong.
				step.Detail = "not named: " + step.Detail
			}
		}
		m.submitSteps[i] = step
	}
	if m.submitView != nil {
		m.submitView.SetSteps(m.submitSteps)
	}
	return m, waitForSubmitProgress(msg.progressCh, msg.resultCh)
}

// --- op -> row mapping (v2 spec §12) --------------------------------------
//
// v2 spec §12's rows are `<glyph> <noun> <detail>` ("✓ worktree
// zvi/fix-login-redirect-loop from main"), which is neither what
// plan.Op.Label is (a verb phrase, "creating worktree") nor something
// plan.Progress carries at all. This layer resolves both, once, from the
// op list and the plan.Input they were built from -- keyed on
// plan.OpKind, never on a label string -- and pushes them into the view,
// the same way it pushes every other verdict into a field. See
// form.Step's own doc comment for why internal/form must not do this
// itself.

// submitHeaderName is the header's left half on the submit screen: the
// form's own name, so the pipeline does not read as a different program
// (v2 spec §12).
const submitHeaderName = "new session"

// submitHeaderContext is the header's right half: the selected project,
// which is the part of v2 spec §4's "repository name and its current
// branch" this layer resolves without any extra I/O. "" when there is no
// project directory to name, which renders the half empty.
func submitHeaderContext(in plan.Input) string {
	if in.ProjectDir == "" {
		return ""
	}
	return filepath.Base(in.ProjectDir)
}

// submitWaitingHint is the footer instruction for a step that has stopped
// for the user (#115) -- one more thing internal/app resolves from
// plan.Input because internal/form is not allowed to know what a prompt op
// is (submitview.go's SetWaitingHint).
//
// The distinction is not decoration. The whole point of waiting rather than
// failing is that the prompt the user composed still gets delivered, so
// saying so is the most useful thing this line can do -- and saying it on a
// plan that carries no prompt would be promising to deliver nothing.
//
// Both strings must fit the 78 cells an 80-column popup leaves; see
// defaultWaitingHint for what happens to one that does not.
func submitWaitingHint(in plan.Input) string {
	if in.Prompt == "" {
		return ""
	}
	return "answer the dialog in the pane — your prompt goes out as soon as you do"
}

// submitSteps maps a built plan to the submit view's rows, one per op,
// all at StepPending -- the full checklist the user sees before the
// first event arrives.
func submitSteps(ops []plan.Op, in plan.Input) []form.Step {
	steps := make([]form.Step, len(ops))
	for i, op := range ops {
		steps[i] = form.Step{
			Label:  submitStepLabel(op, in),
			Detail: submitStepDetail(op, in),
			State:  plan.StepPending,
		}
	}
	return steps
}

// submitStepLabel is the short noun an op occupies the label column
// with. It must stay inside the form's eleven-cell label column, so
// these are all one word.
func submitStepLabel(op plan.Op, in plan.Input) string {
	switch op.Kind {
	case plan.OpWorktreeCreate:
		return "worktree"
	case plan.OpWorkspaceCreate:
		return "workspace"
	case plan.OpTabCreate, plan.OpTabRename:
		return "tab"
	case plan.OpPaneSplit:
		return "pane"
	case plan.OpAgentStart, plan.OpClauthLaunch:
		if in.AgentKind != "" {
			return in.AgentKind
		}
		return "agent"
	case plan.OpAwaitDetection:
		return "detection"
	case plan.OpAgentPrompt:
		return "prompt"
	default:
		return op.Label
	}
}

// submitStepDetail is what an op puts in the value column: the concrete
// thing it is creating, phrased so it reads correctly both while the
// step runs and after it has finished ("zvi/fix-login-redirect-loop from
// main", not "creating zvi/..."). "" is a legitimate answer -- the view
// renders the state's own word ("queued", "working…", "done") instead,
// which is exactly what v2 spec §12's mockup shows for the prompt row.
func submitStepDetail(op plan.Op, in plan.Input) string {
	switch op.Kind {
	case plan.OpWorktreeCreate:
		// A chosen base as chosen. With none, from a lane, the commit it
		// turned out to be, which "HEAD" would misname: from the repository
		// root, HEAD is the primary checkout's. Seven characters, as git
		// abbreviates one, since the row has a branch to fit beside it.
		base := cmp.Or(in.BaseRef, in.Linked.Commit[:min(len(in.Linked.Commit), 7)], "HEAD")
		if in.Branch == "" {
			return "from " + base
		}
		return in.Branch + " from " + base
	case plan.OpWorkspaceCreate, plan.OpTabCreate, plan.OpPaneSplit:
		return in.Title
	case plan.OpTabRename:
		// The name the tab is being given, read off the op rather than
		// assumed to be the title -- the same reason the agent row reads
		// op.Agent.Name.
		if op.Rename != nil {
			return op.Rename.Label
		}
		return ""
	case plan.OpAgentStart:
		if op.Agent != nil {
			return withOptions(op.Agent.Name, in)
		}
		return ""
	case plan.OpClauthLaunch:
		// Two independent corrections to one sentence, both needed, because
		// "under clauth <pin>" was untrue in two different ways.
		//
		// The PROGRAM comes off the argv, not from the word "clauth":
		// [clauth] launcher makes that argv configurable, so a hardcoded
		// "under clauth" states something untrue for a session launched with
		// `["claude-as","{account}"]` -- nothing about clauth runs there. For
		// the default launcher RunArgv[0] IS "clauth", so every existing
		// string and the progress golden frame are unchanged.
		//
		// And which MECHANISM ran is read off op.RunEnv rather than off
		// in.AccountLaunch on purpose: plan.Build falls back to the argv
		// mechanism when wrapper mode has no config dir to isolate with, and a
		// step that named the mode the user ASKED for would be describing a
		// command that was not typed. in.AccountLaunch is then consulted for
		// one thing only -- whether the two disagree, which is the
		// substitution itself and is worth a clause.
		if len(op.RunEnv) > 0 {
			return withOptions("as "+in.AccountPin+", through your shell's claude", in)
		}
		prog := "clauth"
		if len(op.RunArgv) > 0 && op.RunArgv[0] != "" {
			prog = op.RunArgv[0]
		}
		detail := "under " + prog
		if in.AccountPin != "" {
			detail += " " + in.AccountPin
		}
		if in.AccountLaunch == plan.LaunchWrapper {
			// Wrapper mode was ASKED for and did not happen: the pick came
			// back with no config_dir, so plan.Build fell back to the argv
			// mechanism (clauthLaunchCommand's own doc comment). Saying so
			// here is the popup half of that substitution being reported at
			// the moment it happens -- without it the row is honest about the
			// command and silent about the mode, and the one setting the user
			// had to opt into turns itself off with nothing said. The README
			// documents the fallback; a document is not a report.
			detail += " (wrapper mode had no config dir)"
		}
		return withOptions(detail, in)
	case plan.OpAwaitDetection:
		return "waiting for the agent"
	default:
		return ""
	}
}

// submitStepError renders a failed step's error into the value column.
//
// plan.Execute wraps every op error as "plan: execute: <op label>: ...",
// a prefix identical for every failure that would otherwise spend most
// of a sixty-cell column saying nothing -- and the op label is already
// the row's own label. Both are trimmed; anything unexpected is left
// exactly as it came, so a wrapper this does not recognize is never
// silently swallowed.
func submitStepError(err error, opLabel string) string {
	if err == nil {
		return ""
	}
	text := strings.TrimPrefix(err.Error(), execErrorPrefix)
	if opLabel != "" {
		text = strings.TrimPrefix(text, opLabel+": ")
	}
	return text
}

// execErrorPrefix is plan.Execute's own error wrapper (exec.go:
// `fmt.Errorf("plan: execute: %s: %w", op.Label, runErr)`).
const execErrorPrefix = "plan: execute: "

// handleSubmitDone applies plan.Execute's own final ExecResult. A fully
// successful run (FailedIndex == -1) has nothing further to show -- the
// plugin's whole job was creating and launching the session, which is now
// done -- so it quits, mirroring form.CancelMsg's own tea.Quit posture. A
// failure before step 1 (topology creation) ever succeeded (Created ==
// nil) has no space for keep or clean to act on: spec §9's keep-or-clean
// gate is explicitly scoped to "after step 1 succeeded," so the failed
// progress line (already streamed) is simply left showing, with
// updateSubmitting's own Esc/Ctrl+C handling as this state's only way out
// (SubmitView's own k/c grammar never activates -- SetFailure is never
// called). No space is not the same as nothing made, though (#208): herdr
// can fail a worktree create after git has made the branch, so the view
// says "nothing was created" only on ExecResult.NothingCreated, and
// without it says what may be left, naming the branch when the plan had
// one. Otherwise, plan.CleanCheck needs to run first -- real git I/O for
// a worktree space, via gitx.Disposable -- before SubmitView's failure
// prompt can be shown at all, so that's deferred to its own Cmd
// (runCleanCheckCmd) rather than called synchronously here.
func (m Model) handleSubmitDone(msg submitDoneMsg) (Model, tea.Cmd) {
	if msg.result.FailedIndex == -1 {
		// Persist spec §12's state BEFORE quitting -- the quit is deferred
		// to statePersistedMsg's own handler (updateSubmitting), which holds
		// the popup instead when a step finished with a warning (#230), rather than
		// batched alongside the write, since tea.Batch runs its commands
		// concurrently and tea.Quit would race the write to a finish. This
		// is the hook the whole state layer was missing (finding I2):
		// config.SaveState and State.TouchRecent were built and
		// unit-tested and then called from nowhere, so recents.json and
		// last-used.json were never written, spec §6 field 2's recents
		// candidate source was permanently empty, and
		// LastKind/LastPlacement/LastWorktree were dead on both sides.
		return m, m.persistStateCmd()
	}
	if msg.result.Created == nil {
		m.submitDeadEnd = true // see Model.submitDeadEnd's own doc comment.
		if m.submitView != nil {
			// The view needs to be told too, so its footer offers the one
			// key this state actually honors ("esc close") and says so
			// nowhere else -- see SubmitView.footerParts. It gets the
			// worktree and its branch because, without evidence that
			// nothing was made, they are what may be left (#208).
			branch := ""
			if m.submitInput.UseWorktree {
				branch = m.submitInput.Branch
			}
			m.submitView.SetDeadEnd(msg.result, branch)
		}
		// A dead end still owes the user their prompt back. This branch
		// used to return nil because plan.Execute only ever set PromptText
		// when the PROMPT op failed, and reaching the prompt op means the
		// topology op had already succeeded -- so Created == nil implied
		// PromptText == "" and saving here would have been dead code.
		// ExecResult.PromptText now means "the prompt did not land" (#90),
		// which makes this the case where a `worktree create` that failed
		// on a branch name would otherwise take a composed prompt with it.
		return m, saveUnsentPromptCmd(m.stateDir, msg.result.PromptText)
	}
	m.submitResult = msg.result
	return m, tea.Batch(
		runCleanCheckCmd(m.submitInput, msg.result),
		// A prompt that never reached the agent is the one piece of the
		// user's own work this failure can destroy -- save it before the
		// popup can close (finding I6). A no-op when there is none.
		saveUnsentPromptCmd(m.stateDir, msg.result.PromptText),
	)
}

// statePersistedMsg reports that persistStateCmd has finished (whether or
// not the write itself succeeded) -- the signal updateSubmitting turns
// into the tea.Quit that ends a successful submit, or, when a step finished
// with a warning, into the held screen that shows it (#230).
type statePersistedMsg struct{}

// persistStateCmd writes the choices this submit was made with back to the
// plugin state dir (spec §12): the project directory into recents.json's
// most-recently-used list, the agent kind/placement/worktree toggle into
// last-used.json, and the same three plus the base ref into projects.json
// under THIS project's key (v2 spec §10), so the next form-open defaults to
// what the user actually launched with last time -- globally, and more
// specifically here. Called only on a fully successful submit -- a failed
// one says nothing about what the user wants next.
//
// last-used.json keeps being written exactly as before: it is now v2 spec
// §10's global fallback tier rather than the only memory, which is what
// makes per-project memory a pure addition with no migration step and no
// data loss for anyone upgrading.
//
// projects.json is skipped when no key resolved -- a submit fired inside
// the debounce window after a project change, before the dir check
// answered, has nowhere to record itself. The global tier still records it.
//
// The write happens in a Cmd rather than inline in the message handler,
// matching every other I/O-performing source in this file, and its error
// is deliberately dropped: state is loss-tolerant by spec §12, and there
// is nothing useful to tell a user whose session just launched about a
// recents file that did not save. The state SNAPSHOT is taken here, on the
// Model, not inside the closure -- the same "capture the relevant state at
// call time" discipline scheduleTitleCheck documents.
//
// It always returns a non-nil Cmd, even with no state dir to write to:
// statePersistedMsg is what ends a successful submit, so a nil Cmd here
// would leave it hanging on screen forever.
func (m Model) persistStateCmd() tea.Cmd {
	stateDir := m.stateDir

	st := m.state
	if dir := m.submitInput.ProjectDir; dir != "" {
		st.TouchRecent(dir)
	}
	st.LastKind = m.submitInput.AgentKind
	st.LastPlacement = defaults.RememberedPlacement(m.submitInput, m.state.LastPlacement)
	useWorktree := m.submitInput.UseWorktree
	st.LastWorktree = &useWorktree

	projectKey := m.projectKey
	previous, _ := m.projects.Get(projectKey)
	projects := m.projects.Touched(projectKey, config.ProjectDefaults{
		Kind:      m.submitInput.AgentKind,
		Worktree:  &useWorktree,
		Placement: defaults.RememberedPlacement(m.submitInput, previous.Placement),
		Base:      m.submitInput.BaseRef,
	}, time.Now())

	return func() tea.Msg {
		// No stateDir guard here any more: both savers refuse an empty
		// directory themselves. Three call sites with two of them
		// remembering to check is exactly how linear.SaveCache came to
		// write into the user's repository, so the check lives in one
		// place now.
		_ = config.SaveState(stateDir, st)
		if projectKey != "" {
			_ = config.SaveProjects(stateDir, projects)
		}
		return statePersistedMsg{}
	}
}

// --- unsent prompt recovery (spec §9 step 3, finding I6) ------------------

// unsentPromptFileName is where a prompt that never reached the agent is
// written, under $HERDR_PLUGIN_STATE_DIR. A fixed name (rather than a
// timestamped one) keeps the path the failure view shows short enough to
// read and retype, and there is only ever one prompt worth recovering: the
// one from the submit the user is looking at.
const unsentPromptFileName = "unsent-prompt.txt"

// promptSavedMsg reports where an unsent prompt was written, or why it
// could not be.
type promptSavedMsg struct {
	path string
	err  error
}

// saveUnsentPromptCmd writes text to $stateDir/unsent-prompt.txt so spec
// §9 step 3's "prompt text surfaced back to the user for manual paste"
// actually survives (finding I6). Before this, the failure view rendered
// the whole prompt through fitLine -- Inline(true), which strips newlines,
// then a hard clip to the popup width -- so a multi-paragraph
// Linear-seeded prompt became one glued, truncated line, and then the
// popup closed and it was gone.
//
// Returns nil for an empty text (a successful submit, or a plan that
// carried no prompt at all) or an unset state dir.
func saveUnsentPromptCmd(stateDir, text string) tea.Cmd {
	if text == "" || stateDir == "" {
		return nil
	}
	path := filepath.Join(stateDir, unsentPromptFileName)
	return func() tea.Msg {
		if err := os.MkdirAll(stateDir, 0o700); err != nil {
			return promptSavedMsg{err: fmt.Errorf("create %s: %w", stateDir, err)}
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			return promptSavedMsg{err: fmt.Errorf("write %s: %w", path, err)}
		}
		return promptSavedMsg{path: path}
	}
}

// handlePromptSaved hands SubmitView the path the unsent prompt landed at
// -- or the error, so a failed save is not itself silent: the view falls
// back to showing the prompt's own (clipped) text inline, which is still
// better than nothing.
func (m Model) handlePromptSaved(msg promptSavedMsg) (Model, tea.Cmd) {
	if m.submitView != nil {
		m.submitView.SetUnsentPrompt(msg.path, msg.err)
	}
	return m, nil
}

// cleanCheckMsg carries plan.CleanCheck's own verdict for the space
// Execute just failed to finish setting up, alongside the ExecResult it
// was computed for (SubmitView.SetFailure wants both together).
type cleanCheckMsg struct {
	result   plan.ExecResult
	decision plan.CleanDecision
}

// runCleanCheckCmd runs plan.CleanCheck in the background -- it performs
// real git I/O for a worktree space (gitx.Disposable), so it is never
// called directly from a message handler in this package, matching every
// other I/O-performing source in this file.
func runCleanCheckCmd(in plan.Input, result plan.ExecResult) tea.Cmd {
	return func() tea.Msg {
		decision := plan.CleanCheck(context.Background(), in, result)
		return cleanCheckMsg{result: result, decision: decision}
	}
}

// handleCleanCheckResult applies plan.CleanCheck's own verdict to
// SubmitView, switching it into its keep-or-clean failure prompt (spec
// §9) -- and records the decision on Model itself so a later CleanMsg
// (handleCleanRequested) can re-check Allowed before ever calling
// plan.Clean, rather than trusting SubmitView's own k/c gating alone (the
// same defense-in-depth posture reloadClauthCmd's nil-source guard
// already applies elsewhere in this package).
func (m Model) handleCleanCheckResult(msg cleanCheckMsg) (Model, tea.Cmd) {
	m.submitCleanDecision = msg.decision
	if m.submitView != nil {
		m.submitView.SetFailure(msg.result, msg.decision)
	}
	return m, nil
}

// handleCleanRequested implements form.CleanMsg (the "c" keypress from
// SubmitView's own failure prompt): re-checks
// m.submitCleanDecision.Allowed before running plan.Clean at all.
func (m Model) handleCleanRequested() (Model, tea.Cmd) {
	if !m.submitCleanDecision.Allowed {
		return m, nil
	}
	return m, runCleanCmd(m.deps.Runner, m.submitInput, m.submitResult)
}

// cleanDoneMsg reports plan.Clean's own outcome.
type cleanDoneMsg struct {
	outcome plan.CleanOutcome
	err     error
}

func runCleanCmd(r herdrc.Runner, in plan.Input, result plan.ExecResult) tea.Cmd {
	return func() tea.Msg {
		outcome, err := plan.Clean(context.Background(), r, in, result)
		return cleanDoneMsg{outcome: outcome, err: err}
	}
}

// handleCleanDone finishes plan.Clean's own attempt. Success quits (the
// created space is gone; the plugin's job is done, matching
// handleSubmitDone's own full-success posture). A failure does NOT quit
// -- fix round 1 (reviewer finding -- silent failure): an earlier version
// quit here regardless of msg.err, which made a failed Clean
// indistinguishable from a successful one (the created space was left
// behind either way, but only the failure case should have been silent
// about it). SubmitView.SetCleanFailed now surfaces a short error line,
// and staying in the failure state (no cmd) keeps the k/c choice
// available -- "c" retries plan.Clean via handleCleanRequested, "k" gives
// up and keeps the space as-is, the same choice the user already had for
// the ORIGINAL step failure.
//
// Two outcomes are neither of those (#173, #190's review):
//
//   - a *plan.CleanRefusal: Clean found CleanCheck's verdict stale before
//     removing anything. The space is untouched, so this is the verdict a
//     moment later, not a failure -- remove becomes unavailable with the
//     reason, which also stops `c` asking again for a refusal it would get
//     again.
//   - a clean that ran but kept the branch the line had promised to
//     delete. The space is gone, so there is nothing left to keep or
//     remove, and quitting would bury the one thing still true: the branch
//     is there. The view says so, and esc closes it (submitDeadEnd).
//
// A branch kept the way the line already said is no news, and quits.
func (m Model) handleCleanDone(msg cleanDoneMsg) (Model, tea.Cmd) {
	var refusal *plan.CleanRefusal
	switch {
	case errors.As(msg.err, &refusal):
		m.submitCleanDecision = plan.CleanDecision{Allowed: false, Reason: refusal.Reason}
		if m.submitView != nil {
			m.submitView.SetCleanFailed(nil)
			m.submitView.SetFailure(m.submitResult, m.submitCleanDecision)
		}
		return m, nil
	case msg.err != nil:
		if m.submitView != nil {
			m.submitView.SetCleanFailed(msg.err)
		}
		return m, nil
	case msg.outcome.KeptBranch != "" && m.submitCleanDecision.Branch == plan.BranchDeleted:
		if m.submitView != nil {
			m.submitView.SetCleanedKeepingBranch(msg.outcome.KeptBranch, msg.outcome.KeptReason)
		}
		m.submitDeadEnd = true
		return m, nil
	}
	return m, tea.Quit
}

// updateSubmitting is Update's own dispatch table while m.submitting is
// true (form.Model's own key grammar is bypassed entirely during submit
// -- SubmitView is not a form.Section and takes no part in its focus
// ring, per submitview.go's own file doc comment). Esc/Ctrl+C only quit
// when m.submitDeadEnd is true (form.Model's ActionCancel equivalent,
// since MapKey never runs here) -- the one failed state that has no
// other way out at all (step 1 itself failed; see handleSubmitDone/
// Model.submitDeadEnd's own doc comments) -- and Esc, Enter or Ctrl+C
// when m.submitWarned is, a finished create held on screen for its
// warnings (#230), where nothing is left running to strand.
//
// Fix round 1 (reviewer finding): an earlier version of this method quit
// on Esc/Ctrl+C unconditionally, for the WHOLE m.submitting lifetime, not
// just the dead end. Since runSubmitCmd's progress channel is unbuffered
// and only ever re-armed by a fresh Update call (see its own file-doc
// comment), quitting mid-stream stops that draining outright --
// permanently, not just delayed -- which leaves plan.Execute's own
// background goroutine forever blocked on a channel send nobody is left
// to read (reviewer-measured: a permanently elevated goroutine count) AND
// abandons whatever plan.Execute had already created with no CleanCheck,
// no Clean, no keep/clean prompt at all: exactly the "never silent"
// guarantee spec §9 exists to prevent. Every other submitting state (an
// active stream, the CleanCheck wait, or the keep/clean prompt already
// showing) now ignores Esc/Ctrl+C entirely -- forwarded to SubmitView's
// own Update like any other key, which no-ops for anything but "k"/"c"
// once SetFailure has been called (submitview.go), and is a no-op before
// that too (SubmitView's own "zero-value safety" contract).
//
// Adding context cancellation to unblock plan.Execute on demand instead
// was explicitly ruled out for this fix round: it's a bigger, separately-
// reviewable change this late, not a one-line scoping fix.
//
// Every other key is forwarded to SubmitView's own Update (its k/c
// keep-or-clean grammar); every async submit-pipeline message is
// dispatched to its own handler above; anything else (e.g. a stray
// tea.WindowSizeMsg, already captured by Update's own top-level
// width/height snapshot) is ignored.
func (m Model) updateSubmitting(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if s := msg.String(); m.submitDeadEnd && (s == "esc" || s == "ctrl+c") {
			return m, tea.Quit
		}
		if s := msg.String(); m.submitWarned && (s == "esc" || s == "enter" || s == "ctrl+c") {
			return m, tea.Quit
		}
		if m.submitView != nil {
			return m, m.submitView.Update(msg)
		}
		return m, nil
	case submitProgressMsg:
		return m.handleSubmitProgress(msg)
	case submitDoneMsg:
		return m.handleSubmitDone(msg)
	case cleanCheckMsg:
		return m.handleCleanCheckResult(msg)
	case promptSavedMsg:
		return m.handlePromptSaved(msg)
	case statePersistedMsg:
		// A successful submit ends here, once spec §12's state is on disk
		// (handleSubmitDone/persistStateCmd) -- the plugin's whole job was
		// creating and launching the session, which is done. Unless a step
		// finished with a warning (#230): its row's reason is the only
		// place it is said, and quitting would take it off screen at once.
		if m.submitView != nil && hasWarning(m.submitSteps) {
			m.submitWarned = true
			m.submitView.SetDoneWithWarnings()
			return m, nil
		}
		return m, tea.Quit
	case form.KeepMsg:
		return m, tea.Quit
	case form.CleanMsg:
		return m.handleCleanRequested()
	case cleanDoneMsg:
		return m.handleCleanDone(msg)
	default:
		return m, nil
	}
}

// hasWarning reports whether any step finished StepFailedNonFatal.
func hasWarning(steps []form.Step) bool {
	for _, s := range steps {
		if s.State == plan.StepFailedNonFatal {
			return true
		}
	}
	return false
}

// --- production gitSource: internal/gitx + os.Stat ------------------------

// gitxSource implements gitSource over the real internal/gitx package (a
// real `git` subprocess) plus os.Stat for DirExists -- gitx itself has no
// directory-existence helper (Atrium's own equivalent, config.DirExists,
// is not on this task's clean-file list and was never opened; this is a
// two-line stdlib call, not a port).
type gitxSource struct{}

// NewGitSource returns the production gitSource main.go wires into Deps.
func NewGitSource() gitSource { return gitxSource{} }

func (gitxSource) DirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func (gitxSource) IsGitRepo(dir string) bool { return gitx.IsGitRepo(dir) }

func (gitxSource) RepoRoot(ctx context.Context, dir string) (string, error) {
	return gitx.RepoRoot(ctx, dir)
}

func (gitxSource) PrimaryCheckout(ctx context.Context, dir string) (string, error) {
	return gitx.PrimaryCheckout(ctx, dir)
}

func (gitxSource) ResolveCommit(ctx context.Context, dir, ref string) (string, error) {
	return gitx.ResolveRef(ctx, dir, ref)
}

func (gitxSource) ListSubdirs(dir string, limit int) []string {
	return pathx.ListSubdirs(dir, limit)
}

func (gitxSource) ResolvePath(path string) string { return pathx.Resolve(path) }

func (gitxSource) ListBranches(ctx context.Context, dir string, limit int) ([]string, error) {
	return gitx.ListBranches(ctx, dir, limit)
}

func (gitxSource) BranchExists(ctx context.Context, dir, name string) (bool, error) {
	return gitx.BranchExists(ctx, dir, name)
}

func (gitxSource) CurrentBranch(ctx context.Context, dir string) (string, error) {
	return gitx.CurrentBranch(ctx, dir)
}

func (gitxSource) FetchPrune(ctx context.Context, dir string) error {
	return gitx.FetchPrune(ctx, dir)
}

// --- production clauthSource: internal/clauth ------------------------------

// clauthLoader implements clauthSource over the real internal/clauth
// package (clauth.Load: prefers a fresh on-disk status file, falls back to
// invoking the clauth CLI -- see clauth.Load's own doc comment).
type clauthLoader struct{ opts clauth.LoadOpts }

// NewClauthSource returns the production clauthSource main.go wires into
// Deps.
func NewClauthSource(opts clauth.LoadOpts) clauthSource { return clauthLoader{opts: opts} }

func (l clauthLoader) Status(ctx context.Context) (clauth.Status, error) {
	return clauth.Load(ctx, l.opts)
}

// withOptions appends the session options the agent is launched with to a
// launch step's detail, the way the options row states them ("opus ·
// effort xhigh"), so the progress screen says what was started and not
// only where. Only the CHOSEN options: what [agents.extra_args] passes on
// its own was configured, not chosen, and the options panel is where it is
// shown.
func withOptions(detail string, in plan.Input) string {
	var parts []string
	for _, o := range agentopts.For(in.AgentKind) {
		if v := in.AgentOptions[o.Name]; v != "" {
			parts = append(parts, o.Row(v))
		}
	}
	if len(parts) == 0 {
		return detail
	}
	return detail + " · " + strings.Join(parts, " · ")
}
