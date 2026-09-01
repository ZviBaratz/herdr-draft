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
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/plan"
)

// debounceDelay is spec §8's shared 150ms debounce window every versioned
// source in this file waits out before running its real check.
const debounceDelay = 150 * time.Millisecond

// maxBaseRefs is spec §6 field 4's "capped at 50" bound on the base-ref
// picker's candidate list.
const maxBaseRefs = 50

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
	path := req.key
	return func() tea.Msg {
		exists := git.DirExists(path)
		isRepo := exists && git.IsGitRepo(path)
		return dirResultMsg{req: req, dirExists: exists, isGitRepo: isRepo}
	}
}

func (m Model) handleDirDebounce(msg dirDebounceMsg) (Model, tea.Cmd) {
	if msg.req.version != m.dirReqVersion {
		return m, nil // superseded by a newer directory selection
	}
	return m, m.runDirCheck(msg.req)
}

// handleDirResult applies a directory-validity result: DirField's own
// inline (invalid)/(direct) marker, WorktreeField's git-target gate, and --
// the first time a git-repo target is actually observed -- the
// config/state-derived worktree on/off default (see WorktreeField.SetOn's
// own doc comment on why this can only be applied once the target is known
// to be a usable git repo, and worktreeDefaultApplied's doc comment on why
// it's a one-shot rather than reapplied on every later git-repo target).
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
	if msg.isGitRepo && !m.worktreeDefaultApplied {
		m.worktreeDefaultApplied = true
		m.worktree.SetOn(m.worktreeDefaultOn)
	}
	m.syncDerivedInertness()

	var cmd tea.Cmd
	if m.worktree.On() != worktreeOnBefore {
		// The worktree default just flipped the toggle -- runTitleCheck's
		// own branch-exists half depends on it (skipped entirely while
		// worktree is off), so re-run the title-duplicate check now rather
		// than waiting for the next title/branch/dir edit to happen to
		// notice: syncDerivedInertness above already resynced
		// lastWorktreeOn to the NEW value, so reactToChanges' own diff
		// would otherwise never see this specific transition.
		cmd = m.scheduleTitleCheck(m.title.Value(), m.worktree.Branch(), msg.req.key, m.worktree.On())
	}
	return m, cmd
}

// --- base-ref list + once-per-repo git fetch --prune (spec §6 field 4) --

type baseDebounceMsg struct{ req request }

type baseResultMsg struct {
	req  request
	refs []string
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
	path := req.key
	return func() tea.Msg {
		if !git.IsGitRepo(path) {
			return baseResultMsg{req: req, err: true}
		}
		refs, err := git.ListBranches(context.Background(), path, maxBaseRefs)
		if err != nil {
			return baseResultMsg{req: req, err: true}
		}
		return baseResultMsg{req: req, refs: refs}
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
		_ = git.FetchPrune(context.Background(), path)
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
			exists, err := git.BranchExists(context.Background(), dir, branch)
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
		return m, tea.Quit
	}
	if msg.result.Created == nil {
		return m, nil
	}
	m.submitCreated = *msg.result.Created
	return m, runCleanCheckCmd(m.submitInput, *msg.result.Created, msg.result)
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

// cleanDoneMsg reports plan.Clean's own outcome. err is captured (not
// silently discarded at the source) but currently unread by
// handleCleanDone -- SubmitView's existing API has no "clean failed"
// state for it to surface into (see this task's own report's Concerns
// section).
type cleanDoneMsg struct{ err error }

func runCleanCmd(r herdrc.Runner, in plan.Input, created herdrc.CreatedTopology) tea.Cmd {
	return func() tea.Msg {
		err := plan.Clean(context.Background(), r, in, created)
		return cleanDoneMsg{err: err}
	}
}

// handleCleanDone finishes the submit pipeline once plan.Clean returns,
// success or failure: the created space is left in SOME terminal state
// either way (removed on success, or exactly Keep's own outcome on
// failure), so this quits regardless -- see cleanDoneMsg's own doc
// comment on why its error goes no further than this.
func (m Model) handleCleanDone(cleanDoneMsg) (Model, tea.Cmd) {
	return m, tea.Quit
}

// updateSubmitting is Update's own dispatch table while m.submitting is
// true (form.Model's own key grammar is bypassed entirely during submit
// -- SubmitView is not a form.Section and takes no part in its focus
// ring, per submitview.go's own file doc comment). Esc/Ctrl+C are this
// state's own escape hatch (form.Model's ActionCancel equivalent, since
// MapKey never runs here) -- needed in particular for a step-1 failure,
// which otherwise leaves the user with no way to close the popup at all
// (see handleSubmitDone). Every other key is forwarded to SubmitView's
// own Update (its k/c keep-or-clean grammar); every async submit-pipeline
// message is dispatched to its own handler above; anything else
// (e.g. a stray tea.WindowSizeMsg, already captured by Update's own
// top-level width/height snapshot) is ignored.
func (m Model) updateSubmitting(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if s := msg.String(); s == "esc" || s == "ctrl+c" {
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

func (gitxSource) ListBranches(ctx context.Context, dir string, limit int) ([]string, error) {
	return gitx.ListBranches(ctx, dir, limit)
}

func (gitxSource) BranchExists(ctx context.Context, dir, name string) (bool, error) {
	return gitx.BranchExists(ctx, dir, name)
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
