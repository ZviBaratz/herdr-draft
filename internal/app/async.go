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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/defaults"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/pathx"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

// debounceDelay is spec §8's shared 150ms debounce window every versioned
// source in this file waits out before running its real check.
const debounceDelay = 150 * time.Millisecond

// maxBaseRefs is spec §6 field 4's "capped at 50" bound on the base-ref
// picker's candidate list.
const maxBaseRefs = 50

// maxBrowseEntries bounds one path-mode directory listing (spec §6 field
// 2), matching atrium's own maxDirEntries. A directory with more
// subdirectories than this stays reachable by typing the target out --
// DirField's literal-path fallback row.
const maxBrowseEntries = 500

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
	// memoryKey is the projects.json key for this directory (spec §10):
	// the repository root when it is a repo, so a linked worktree and its
	// origin share one memory, and the canonical absolute path otherwise.
	// "" when the directory does not exist -- there is nothing to remember
	// about a path that is not there. Resolved in the background alongside
	// the validity check (projectMemoryKey), since it costs a `git
	// rev-parse` and a symlink walk.
	memoryKey string
}

// scheduleDirCheck bumps the directory-validity source's own request
// counter and returns a tea.Cmd that sleeps out debounceDelay before
// reporting the fire -- see runDirCheck for the actual check this leads to.
func (m *Model) scheduleDirCheck(path string) tea.Cmd {
	m.dirReqVersion++
	v := m.dirReqVersion
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
	// req.key stays the RAW typed text -- DirField.SetValidity keys its own
	// inline marker on it and only renders while it still equals Value().
	// Only the path actually handed to the filesystem is expanded (finding
	// I3): without this, typing "~/Projects/foo" was reported invalid, and
	// on submit `herdr workspace create --cwd` (which, unlike `worktree
	// create`, does no tilde expansion of its own) would have rooted a
	// workspace at a directory literally named "~".
	path := pathx.ExpandTilde(req.key)
	return func() tea.Msg {
		exists := git.DirExists(path)
		isRepo := exists && git.IsGitRepo(path)
		return dirResultMsg{
			req:       req,
			dirExists: exists,
			isGitRepo: isRepo,
			memoryKey: projectMemoryKey(git, path, exists, isRepo),
		}
	}
}

// projectMemoryKey resolves spec §10's per-project memory key for path,
// which must already be tilde-expanded: the ORIGIN repository root for a
// repo (gitx.RepoRoot -- so every worktree of one repository shares a
// single entry rather than accumulating one each), the canonical absolute
// path otherwise.
//
// Both branches end in pathx.CanonicalKey, deliberately: one normalization
// rule for both means a directory cannot acquire two keys by being reached
// once as a repo and once not, or through differently-symlinked parents.
//
// A directory that does not exist gets no key. A repo whose root cannot be
// resolved (git missing, an unreadable repository) falls back to the path
// key rather than losing its memory entirely -- a slightly wrong key still
// remembers something, and RepoRoot reports a plain non-repository as ("",
// nil) rather than as a failure.
func projectMemoryKey(git gitSource, path string, exists, isRepo bool) string {
	if !exists {
		return ""
	}
	if isRepo {
		if root, err := git.RepoRoot(context.Background(), path); err == nil && root != "" {
			return pathx.CanonicalKey(root)
		}
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
// spec §10's layered defaults re-resolved for the project this directory
// belongs to (applyProjectDefaults, which also owns the worktree on/off
// toggle -- see WorktreeField.SetOn's own doc comment on why that can only
// be applied once the target is known to be a usable git repo).
//
// The defaults are re-applied on EVERY project change, not once per form
// open: the top tier is per-project, so a new project genuinely has a new
// answer. What keeps that from fighting the user is the touched rule
// (Model.worktreeTouched and friends), which replaced the one-shot flag
// this handler used to carry.
func (m Model) handleDirResult(msg dirResultMsg) (Model, tea.Cmd) {
	if msg.req.version != m.dirReqVersion {
		return m, nil // a newer request landed while this one was in flight
	}

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

	worktreeOnBefore := m.worktree.On()
	m.applyProjectDefaults(msg.memoryKey, msg.isGitRepo)

	var cmd tea.Cmd
	if m.worktree.On() != worktreeOnBefore {
		// The worktree default just flipped the toggle -- runTitleCheck's
		// own branch-exists half depends on it (skipped entirely while
		// worktree is off), so re-run the title-duplicate check now rather
		// than waiting for the next title/branch/dir edit to happen to
		// notice: applyProjectDefaults' own syncDerivedInertness already
		// resynced lastWorktreeOn to the NEW value, so reactToChanges' own
		// diff would otherwise never see this specific transition.
		cmd = m.scheduleTitleCheck(m.title.Value(), m.worktree.Branch(), msg.req.key, m.worktree.On())
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
func (m Model) handleBaseResult(msg baseResultMsg) (Model, tea.Cmd) {
	if msg.req.version != m.baseReqVersion {
		return m, nil
	}
	if msg.err {
		m.worktree.SetBaseStatus("couldn't list")
		return m, nil
	}

	m.baseItemsVersion++
	m.worktree.SetHeadBranch(msg.head)
	m.worktree.SetBaseItems(m.baseItemsVersion, msg.refs)
	m.worktree.SetBaseStatus("")

	path := msg.req.key
	var cmd tea.Cmd
	if !m.fetchedRepos[path] {
		m.fetchedRepos[path] = true
		cmd = m.runFetchPrune(path)
	}
	return m, cmd
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
		_ = git.FetchPrune(context.Background(), pathx.ExpandTilde(path))
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
	return m, m.runBaseCheck(request{version: m.baseReqVersion, key: msg.path})
}

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
func (m Model) runTitleCheck(msg titleDebounceMsg) tea.Cmd {
	git := m.deps.Git
	workspaces := m.workspaces
	req := msg.req
	branch, dir, worktreeOn := msg.branch, msg.dir, msg.worktreeOn
	return func() tea.Msg {
		labelTaken := workspaceLabelTaken(workspaces, req.key)

		branchExists := false
		if worktreeOn && branch != "" && dir != "" {
			exists, err := git.BranchExists(context.Background(), pathx.ExpandTilde(dir), branch)
			branchExists = err == nil && exists
		}
		return titleResultMsg{req: req, branchExists: branchExists, labelTaken: labelTaken}
	}
}

// workspaceLabelTaken reports whether any of workspaces already carries
// label as its Label -- an empty title never collides (nothing would be
// created with that label yet).
func workspaceLabelTaken(workspaces []herdrc.WorkspaceInfo, label string) bool {
	if strings.TrimSpace(label) == "" {
		return false
	}
	for _, w := range workspaces {
		if w.Label == label {
			return true
		}
	}
	return false
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
	m.title.SetVerdict(msg.req.key, titleVerdictText(msg.branchExists, msg.labelTaken))
	// titleDupBlocked mirrors the SAME verdict just pushed above --
	// checkSubmitValidation (app.go, spec §9) reads this directly rather
	// than re-deriving it from TitleField's own (unexported) verdict
	// state.
	m.titleDupBlocked = msg.branchExists || msg.labelTaken
	return m, nil
}

// titleVerdictText composes TitleField's verdict message (bounded to 21
// cells by TitleField itself -- see field_title.go's titleVerdictMaxCells)
// from the two duplicate checks spec §6 field 3 names. No literal wording
// is given in the spec beyond the field's own example fixture text, so
// this is this task's own terse phrasing.
func titleVerdictText(branchExists, labelTaken bool) string {
	switch {
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
	err    bool
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
			return linearResultMsg{err: true}
		}
		return linearResultMsg{issues: issues}
	}
}

// handleLinearResult applies a successful refresh to IssueField and
// persists it as the new cache (spec §10) -- both are no-ops when Linear
// isn't configured at all (m.issue == nil) or the fetch failed: a failed
// refresh leaves whatever the cache-rendered (or previously fetched) list
// already showing in place, matching spec §13's "network failures degrade
// ... to inert with a reason; they never block manual-mode creation" (the
// cache-rendered list, however stale, is still usable).
func (m Model) handleLinearResult(msg linearResultMsg) (Model, tea.Cmd) {
	if msg.err || m.issue == nil {
		return m, nil
	}
	m.issueItemsVersion++
	m.issue.SetIssues(m.issueItemsVersion, msg.issues)
	m.linearIssues = msg.issues                  // see Model.linearIssues' own doc comment (handleClearRequested's reseed source).
	_ = linear.SaveCache(m.stateDir, msg.issues) // best-effort; state is loss-tolerant (spec §12).
	return m, nil
}

// --- clauth: reload on account focus (spec §11) ---------------------------

// clauthResultMsg is versioned like every other async source in this file
// (fix round 1: rapid re-focus of Account could otherwise let a slow
// reload's result land AFTER a fresher one and silently overwrite it --
// there is no debounce phase for this source, unlike dir/base/title, so
// version is bumped directly by reloadClauthCmd rather than by a separate
// scheduleX; see handleClauthResult's own staleness check).
type clauthResultMsg struct {
	version int
	status  clauth.Status
	err     bool
}

// reloadClauthCmd re-loads clauth's status feed -- spec §11: "load at open
// and on account focus." The open-time load happens synchronously in
// Bootstrap/New (it gates whether AccountField is even constructed, a
// static precondition that must be known before the form renders); this
// is the focus-triggered reload (see reactToChanges' own FocusedID diff).
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
			return clauthResultMsg{version: v, err: true}
		}
		return clauthResultMsg{version: v, status: st}
	}
}

// handleClauthResult applies a successful, current reload to AccountField
// -- a no-op when the field wasn't constructed at all, the reload failed
// (spec §13: clauth failures degrade, never block), or a fresher reload
// has since been scheduled (msg.version != m.clauthReqVersion -- see
// clauthResultMsg's own doc comment).
func (m Model) handleClauthResult(msg clauthResultMsg) (Model, tea.Cmd) {
	if msg.version != m.clauthReqVersion || msg.err || m.account == nil {
		return m, nil
	}
	m.account.SetProfiles(msg.status)
	m.clauthStatus = msg.status // see Model.clauthStatus' own doc comment (accountAuthBlocked's lookup source).
	return m, nil
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
func runSubmitCmd(ctx context.Context, r herdrc.Runner, ops []plan.Op) tea.Cmd {
	progressCh := make(chan plan.Progress)
	resultCh := make(chan plan.ExecResult, 1)
	go func() {
		res := plan.Execute(ctx, r, ops, func(p plan.Progress) { progressCh <- p })
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
// working progress slice startSubmit seeded (by Index, replacing the row
// it originally seeded at StepPending) and re-renders SubmitView, then
// re-arms waitForSubmitProgress for the next event.
func (m Model) handleSubmitProgress(msg submitProgressMsg) (Model, tea.Cmd) {
	if msg.progress.Index >= 0 && msg.progress.Index < len(m.submitProgress) {
		m.submitProgress[msg.progress.Index] = msg.progress
	}
	if m.submitView != nil {
		m.submitView.SetProgress(m.submitProgress)
	}
	return m, waitForSubmitProgress(msg.progressCh, msg.resultCh)
}

// handleSubmitDone applies plan.Execute's own final ExecResult. A fully
// successful run (FailedIndex == -1) has nothing further to show -- the
// plugin's whole job was creating and launching the session, which is now
// done -- so it quits, mirroring form.CancelMsg's own tea.Quit posture. A
// failure before step 1 (topology creation) ever succeeded (Created ==
// nil) has nothing to keep or clean either: spec §9's keep-or-clean gate
// is explicitly scoped to "after step 1 succeeded," so the failed
// progress line (already streamed) is simply left showing, with
// updateSubmitting's own Esc/Ctrl+C handling as this state's only way out
// (SubmitView's own k/c grammar never activates -- SetFailure is never
// called). Otherwise, plan.CleanCheck needs to run first -- real git I/O
// for a worktree space, via gitx.Disposable -- before SubmitView's
// failure prompt can be shown at all, so that's deferred to its own Cmd
// (runCleanCheckCmd) rather than called synchronously here.
func (m Model) handleSubmitDone(msg submitDoneMsg) (Model, tea.Cmd) {
	if msg.result.FailedIndex == -1 {
		// Persist spec §12's state BEFORE quitting -- the quit is deferred
		// to statePersistedMsg's own handler (updateSubmitting) rather than
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
		return m, nil
	}
	m.submitCreated = *msg.result.Created
	return m, tea.Batch(
		runCleanCheckCmd(m.submitInput, *msg.result.Created, msg.result),
		// A prompt that never reached the agent is the one piece of the
		// user's own work this failure can destroy -- save it before the
		// popup can close (finding I6). A no-op when there is none.
		saveUnsentPromptCmd(m.stateDir, msg.result.PromptText),
	)
}

// statePersistedMsg reports that persistStateCmd has finished (whether or
// not the write itself succeeded) -- the signal updateSubmitting turns
// into the tea.Quit that ends a successful submit.
type statePersistedMsg struct{}

// persistStateCmd writes the choices this submit was made with back to the
// plugin state dir (spec §12): the project directory into recents.json's
// most-recently-used list, the agent kind/placement/worktree toggle into
// last-used.json, and the same three plus the base ref into projects.json
// under THIS project's key (spec §10), so the next form-open defaults to
// what the user actually launched with last time -- globally, and more
// specifically here. Called only on a fully successful submit -- a failed
// one says nothing about what the user wants next.
//
// last-used.json keeps being written exactly as before: it is now spec
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
// statePersistedMsg is what quits the program, so a nil Cmd here would
// leave a successful submit hanging on screen forever.
func (m Model) persistStateCmd() tea.Cmd {
	stateDir := m.stateDir

	st := m.state
	if dir := m.submitInput.ProjectDir; dir != "" {
		st.TouchRecent(dir)
	}
	st.LastKind = m.submitInput.AgentKind
	st.LastPlacement = defaults.PlacementValue(m.submitInput.Placement)
	useWorktree := m.submitInput.UseWorktree
	st.LastWorktree = &useWorktree

	projectKey := m.projectKey
	projects := m.projects.Touched(projectKey, config.ProjectDefaults{
		Kind:      m.submitInput.AgentKind,
		Worktree:  &useWorktree,
		Placement: defaults.PlacementValue(m.submitInput.Placement),
		Base:      m.submitInput.BaseRef,
	}, time.Now())

	return func() tea.Msg {
		if stateDir != "" {
			_ = config.SaveState(stateDir, st)
			if projectKey != "" {
				_ = config.SaveProjects(stateDir, projects)
			}
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
// Returns nil for an empty text (every failure that is not an
// OpAgentPrompt failure) or an unset state dir.
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
func runCleanCheckCmd(in plan.Input, created herdrc.CreatedTopology, result plan.ExecResult) tea.Cmd {
	return func() tea.Msg {
		decision := plan.CleanCheck(context.Background(), in, created)
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
	return m, runCleanCmd(m.deps.Runner, m.submitInput, m.submitCreated)
}

// cleanDoneMsg reports plan.Clean's own outcome.
type cleanDoneMsg struct{ err error }

func runCleanCmd(r herdrc.Runner, in plan.Input, created herdrc.CreatedTopology) tea.Cmd {
	return func() tea.Msg {
		err := plan.Clean(context.Background(), r, in, created)
		return cleanDoneMsg{err: err}
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
func (m Model) handleCleanDone(msg cleanDoneMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		if m.submitView != nil {
			m.submitView.SetCleanFailed(msg.err)
		}
		return m, nil
	}
	return m, tea.Quit
}

// updateSubmitting is Update's own dispatch table while m.submitting is
// true (form.Model's own key grammar is bypassed entirely during submit
// -- SubmitView is not a form.Section and takes no part in its focus
// ring, per submitview.go's own file doc comment). Esc/Ctrl+C only quit
// when m.submitDeadEnd is true (form.Model's ActionCancel equivalent,
// since MapKey never runs here) -- the ONE submitting state that has no
// other way out at all (step 1 itself failed; see handleSubmitDone/
// Model.submitDeadEnd's own doc comments).
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
		// creating and launching the session, which is done.
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
