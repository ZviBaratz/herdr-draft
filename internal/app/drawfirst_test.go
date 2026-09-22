package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ZviBaratz/herdr-draft/internal/clauth"
	"github.com/ZviBaratz/herdr-draft/internal/config"
	"github.com/ZviBaratz/herdr-draft/internal/form"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
	"github.com/ZviBaratz/herdr-draft/internal/linear"
	"github.com/ZviBaratz/herdr-draft/internal/picker"
	"github.com/ZviBaratz/herdr-draft/internal/theme"
)

// drawfirst_test.go covers #293: the popup draws before the three slow
// startup calls answer, and the rows say what is still coming
// (docs/specs/2026-09-21-draw-first-design.md, §9.2's own list).
//
// The tests here are one per CAUSE rather than one per screen, because the
// screens are golden frames in frames_test.go and a frame proves only the
// state someone thought to fixture.

// --- §3: before the draw ---------------------------------------------------

// touchScript writes an executable script that records having run by
// creating marker, and answers stdout. The marker is the assertion: a
// Bootstrap that ran it leaves a file behind, where a `calls` counter on a
// fake would only ever prove that the FAKE was not called.
func touchScript(t *testing.T, name, marker, stdout string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a POSIX shell script")
	}
	bin := filepath.Join(t.TempDir(), name)
	body := "#!/bin/sh\n: > " + marker + "\n"
	if stdout != "" {
		body += "printf '%s' " + strconv.Quote(stdout) + "\n"
	}
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return bin
}

// TestBootstrap_RunsNoSubprocessButWorkspaceList is draw-first spec §3.1
// stated as an assertion, and it is the whole of #293: before #293 this
// arrangement -- an api_key_cmd, a stale clauth and a named picker -- cost
// the user up to two minutes of blank pane before a single byte was drawn.
//
// The three are real executables rather than fakes, so what is asserted is
// that nothing RAN, not that a stand-in was not called.
func TestBootstrap_RunsNoSubprocessButWorkspaceList(t *testing.T) {
	scratch := t.TempDir()
	keyMarker := filepath.Join(scratch, "key-ran")
	pickerMarker := filepath.Join(scratch, "picker-ran")
	keyCmd := touchScript(t, "key-cmd", keyMarker, "lin_api_key")
	pickerBin := touchScript(t, "stub-picker", pickerMarker, "{}")

	configDir := t.TempDir()
	cfg := "[linear]\napi_key_cmd = [" + strconv.Quote(keyCmd) + "]\n" +
		"[clauth]\npicker = " + strconv.Quote(pickerBin) + "\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cl := &fakeClauth{peek: clauth.CachedAsk, status: twoProfileStatus()}
	runner := &fakeRunner{}
	env := Env{ContextJSON: validContextJSON(), ConfigDir: configDir, StateDir: t.TempDir()}

	m, err := Bootstrap(env, runner, cl, newFakeGit(), noSleep, nil)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	if runner.listCalls != 1 {
		t.Errorf("WorkspaceList ran %d times, want exactly 1", runner.listCalls)
	}
	for _, marker := range []string{keyMarker, pickerMarker} {
		if _, err := os.Stat(marker); err == nil {
			t.Errorf("%s ran before the first frame", filepath.Base(marker))
		}
	}
	if cl.calls != 0 {
		t.Errorf("`clauth status --json` ran %d times before the first frame, want 0", cl.calls)
	}
	if cl.peeks != 1 {
		t.Errorf("clauth.Peek ran %d times, want exactly 1", cl.peeks)
	}

	if !m.linearResolving || !m.clauthLoading || !m.pickerProbePending {
		t.Fatalf("the three deferred reads = (linear %v, clauth %v, probe %v), want all three set",
			m.linearResolving, m.clauthLoading, m.pickerProbePending)
	}

	// And Init carries both loads: the key resolve, appended by Bootstrap,
	// and clauth's CLI, scheduled by New.
	var sawKey, sawClauth bool
	for _, cmd := range m.initCmds {
		if cmd == nil {
			continue
		}
		switch cmd().(type) {
		case linearKeyMsg:
			sawKey = true
		case clauthResultMsg:
			sawClauth = true
		}
	}
	if !sawKey || !sawClauth {
		t.Fatalf("Init carries (key %v, clauth %v), want both", sawKey, sawClauth)
	}
	// Running them is what makes the markers appear, which is the other
	// half of the claim: the calls were deferred, not dropped.
	if _, err := os.Stat(keyMarker); err != nil {
		t.Errorf("running Init's key command left no marker: %v", err)
	}
}

// With NO api_key_cmd the resolve is $LINEAR_API_KEY or an inline key plus
// one stat -- no process at all -- so it stays before the draw and those
// users' first frame is exactly today's (draw-first spec §3.1).
func TestBootstrap_AnInlineKeyStillResolvesBeforeTheDraw(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "lin_api_from_the_environment")
	env := Env{ContextJSON: validContextJSON(), ConfigDir: t.TempDir(), StateDir: t.TempDir()}

	m, err := Bootstrap(env, &fakeRunner{}, nil, newFakeGit(), noSleep, nil)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if m.linearResolving {
		t.Error("Model.linearResolving is set with no api_key_cmd, want the resolve done before the draw")
	}
	if m.deps.Linear == nil {
		t.Fatal("Deps.Linear is nil with $LINEAR_API_KEY set")
	}
	if m.issue == nil {
		t.Fatal("Model.issue is nil with a key source configured")
	}
}

// The cache is loaded for every CONFIGURED user, not only for one whose key
// already resolved -- which is #292's own defect, one line up: LoadCache
// used to sit inside `case key != ""`.
func TestBootstrap_TheCacheIsLoadedWhileTheKeyIsStillOut(t *testing.T) {
	stateDir := t.TempDir()
	if err := linear.SaveCache(stateDir, frameIssues()); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"),
		[]byte("[linear]\napi_key_cmd = [\"/nonexistent/never-runs-in-this-test\"]\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	m, err := Bootstrap(Env{ContextJSON: validContextJSON(), ConfigDir: configDir, StateDir: stateDir},
		&fakeRunner{}, nil, newFakeGit(), noSleep, nil)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if len(m.linearIssues) != len(frameIssues()) {
		t.Fatalf("Model.linearIssues = %d issues while the key is out, want the cache (%d)",
			len(m.linearIssues), len(frameIssues()))
	}
	if !m.issue.Enabled() {
		t.Error("the issue row is inert while the key is out, want it pickable from the cache")
	}
}

// --- §4.2, §6: what the rows say ------------------------------------------

func TestIssueRow_TheFourStates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup testSetup
		row   string
		panel string
		live  bool
	}{
		{
			name:  "a key resolved, with a cache",
			setup: testSetup{Linear: &fakeLinear{}, LinearCache: frameIssues()},
			row:   "none",
			panel: "fetching assigned issues…",
			live:  true,
		},
		{
			name:  "a key resolved, no cache",
			setup: testSetup{Linear: &fakeLinear{}},
			row:   "loading…",
			panel: "fetching assigned issues…",
			live:  true,
		},
		{
			name:  "the key is out, with a cache",
			setup: testSetup{LinearResolving: true, LinearCache: frameIssues()},
			row:   "none",
			panel: "waiting for api_key_cmd…",
			live:  true,
		},
		{
			name:  "the key is out, no cache",
			setup: testSetup{LinearResolving: true},
			row:   "loading…",
			panel: "waiting for api_key_cmd…",
			live:  true,
		},
		{
			name:  "the key failed, with a cache",
			setup: testSetup{LinearUnavailable: "pass: not in the store", LinearCache: frameIssues()},
			row:   "none",
			panel: "pass: not in the store",
			live:  true,
		},
		{
			name:  "the key failed, no cache",
			setup: testSetup{LinearUnavailable: "pass: not in the store"},
			row:   "unavailable  pass: not in the store",
			panel: "pass: not in the store",
			live:  false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t, tc.setup)
			if m.issue == nil {
				t.Fatal("Model.issue is nil, want a row in every one of these states")
			}
			if got := m.issue.Enabled(); got != tc.live {
				t.Errorf("IssueField.Enabled() = %v, want %v", got, tc.live)
			}
			if got := ansiRow(m.issue, 120); got != tc.row {
				t.Errorf("row = %q, want %q", got, tc.row)
			}
			if got := fieldText(m.issue, 120); !strings.Contains(got, tc.panel) {
				t.Errorf("panel does not say %q:\n%s", tc.panel, got)
			}
		})
	}
}

// ansiRow is one Section's row, stripped and trimmed -- fieldText's row
// half, for the assertions that are about the row alone.
func ansiRow(s form.Section, w int) string {
	return strings.TrimSpace(ansi.Strip(s.Row(w)))
}

func TestAccountRow_LoadingIsInertAndNotAFocusStop(t *testing.T) {
	m := newTestModel(t, testSetup{
		Clauth:        &fakeClauth{peek: clauth.CachedAsk, status: twoProfileStatus()},
		ClauthLoading: true,
	})
	if m.account == nil {
		t.Fatal("Model.account is nil while clauth is being read, want a row saying so")
	}
	m.account.SetAgentIsClaude(true)
	if m.account.Enabled() {
		t.Error("AccountField.Enabled() = true while loading, want inert (ruling 8)")
	}
	if m.account.RetryOnFocus() {
		t.Error("AccountField.RetryOnFocus() = true while loading, want no focus stop -- the read is already out")
	}
	if got := ansiRow(m.account, 120); got != "loading…" {
		t.Errorf("row = %q, want %q", got, "loading…")
	}
	if got := fieldText(m.account, 120); !strings.Contains(got, "reading clauth profiles…") {
		t.Errorf("panel does not name the read:\n%s", got)
	}
	// Inert means the focus ring skips it, which is what makes the row
	// keyboard-unreachable rather than merely unhelpful -- the state ruling
	// 8 chose deliberately, because there is nothing there to retry.
	m.form.FocusByID("title")
	for range 12 {
		next, _ := m.Update(keyTab)
		m = next.(Model)
		if m.form.FocusedID() == "account" {
			t.Fatal("the focus ring stops on a loading account row")
		}
	}
}

// --- §5.1: the key lands ---------------------------------------------------

// keyResolvingModel is the popup a person with an `op read` helper sees: the
// issue row live over whatever cache there was, the key still out.
func keyResolvingModel(t *testing.T, cache []linear.Issue) Model {
	t.Helper()
	return newTestModel(t, testSetup{LinearResolving: true, LinearCache: cache})
}

func TestLinearKey_Success(t *testing.T) {
	m := keyResolvingModel(t, frameIssues())
	src := &fakeLinear{issues: frameIssues()}

	next, cmd := m.Update(linearKeyMsg{src: src})
	m = next.(Model)

	if m.linearResolving {
		t.Error("Model.linearResolving survived the key landing")
	}
	if m.deps.Linear == nil {
		t.Fatal("Deps.Linear is nil after a key resolved")
	}
	if got := fieldText(m.issue, 120); !strings.Contains(got, "fetching assigned issues…") {
		t.Errorf("the panel does not move on to the fetch:\n%s", got)
	}
	if cmd == nil {
		t.Fatal("a resolved key scheduled no refresh")
	}
	if _, ok := cmd().(linearResultMsg); !ok {
		t.Fatalf("a resolved key scheduled %T, want the assigned-issues refresh", cmd())
	}
}

func TestLinearKey_FailureKeepsACachedListPickable(t *testing.T) {
	m := keyResolvingModel(t, frameIssues())

	next, _ := m.Update(linearKeyMsg{err: errors.New("resolve linear api key: pass: not in the password store")})
	m = next.(Model)

	if !m.issue.Enabled() {
		t.Fatal("a failed key made the issue row inert over a perfectly pickable cache (#292)")
	}
	if m.linearUnavailable == "" {
		t.Error("Model.linearUnavailable is empty after a failed key")
	}
	got := fieldText(m.issue, 120)
	if !strings.Contains(got, "not in the password store") {
		t.Errorf("the panel does not say why the list may be stale:\n%s", got)
	}
	if strings.Contains(got, "unavailable") {
		t.Errorf("a pickable row still calls itself unavailable:\n%s", got)
	}
	if sel := m.issue.Selected(); sel != nil {
		t.Errorf("the cursor moved off `none`: %v", sel)
	}
	// The list is really there, which is the whole point of #292.
	if !strings.Contains(got, frameIssues()[0].Identifier) {
		t.Errorf("the cached list is gone from the panel:\n%s", got)
	}
}

func TestLinearKey_FailureWithNoCacheIsInert(t *testing.T) {
	m := keyResolvingModel(t, nil)

	next, _ := m.Update(linearKeyMsg{err: errors.New("resolve linear api key: pass: not in the password store")})
	m = next.(Model)

	if m.issue.Enabled() {
		t.Error("a failed key with nothing to pick left the row live")
	}
	if got := ansiRow(m.issue, 120); !strings.HasPrefix(got, "unavailable") {
		t.Errorf("row = %q, want the inert `unavailable  <reason>` shape", got)
	}
}

// A resolver that answers no key and no error cannot happen through
// linear.ResolveAPIKey with an api_key_cmd configured -- decision 3 made
// that an error -- but resolveKeyCmd takes a resolver as a parameter, and
// answering it is what keeps a live row from sitting behind no source at
// all with nothing anywhere saying so.
func TestResolveKeyCmd_AnEmptyAnswerIsReported(t *testing.T) {
	cmd := resolveKeyCmd(nil, func(context.Context) (string, error) { return "", nil })
	msg, ok := cmd().(linearKeyMsg)
	if !ok {
		t.Fatalf("resolveKeyCmd produced %T, want linearKeyMsg", cmd())
	}
	if msg.src != nil {
		t.Fatal("an empty key built a client")
	}
	if !errors.Is(msg.err, linear.ErrKeyCmdEmpty) {
		t.Fatalf("err = %v, want linear.ErrKeyCmdEmpty", msg.err)
	}
}

// §7.4: the key's message is unversioned, so it lands on the form a ⌃R⌃R
// rebuilt -- and the rebuilt Init carries no second resolve, because a
// second `op read` is a second approval prompt.
func TestLinearKey_LandsOnARebuiltFormAndIsNeverAskedTwice(t *testing.T) {
	m := keyResolvingModel(t, frameIssues())

	fresh := mustClear(t, m)
	if !fresh.linearResolving {
		t.Fatal("the rebuilt form forgot that a key was still out")
	}
	for _, cmd := range fresh.initCmds {
		if cmd == nil {
			continue
		}
		if _, ok := cmd().(linearKeyMsg); ok {
			t.Fatal("the rebuilt form ran api_key_cmd again -- a second approval prompt")
		}
	}

	next, _ := fresh.Update(linearKeyMsg{src: &fakeLinear{}})
	if got := next.(Model); got.deps.Linear == nil {
		t.Fatal("the key landed on the discarded form rather than the rebuilt one")
	}
}

// --- §5.3: clauth lands ----------------------------------------------------

func loadingClauthModel(t *testing.T, cl *fakeClauth) Model {
	t.Helper()
	return newTestModel(t, testSetup{Clauth: cl, ClauthLoading: true})
}

func TestClauthLoading_ThreeOutcomes(t *testing.T) {
	t.Run("profiles land and the row goes live", func(t *testing.T) {
		m := loadingClauthModel(t, &fakeClauth{peek: clauth.CachedAsk, status: twoProfileStatus()})
		m = landClauth(t, m, clauthResultMsg{status: twoProfileStatus()})
		m.account.SetAgentIsClaude(true)
		if !m.account.Enabled() {
			t.Fatal("the row is still inert after the profiles landed")
		}
		if m.clauthLoading {
			t.Error("Model.clauthLoading survived the answer")
		}
	})

	t.Run("too few profiles leave the row with a reason", func(t *testing.T) {
		m := loadingClauthModel(t, &fakeClauth{peek: clauth.CachedAsk})
		m = landClauth(t, m, clauthResultMsg{status: clauth.Status{Profiles: []clauth.Profile{{Name: "only"}}}})
		if m.account == nil {
			t.Fatal("the row was taken away, want decision 2's `the row stays`")
		}
		if got := fieldText(m.account, 120); !strings.Contains(got, "one profile") {
			t.Errorf("the row does not say clauth has one profile:\n%s", got)
		}
	})

	t.Run("a failure replaces the loading state with the reason", func(t *testing.T) {
		m := loadingClauthModel(t, &fakeClauth{peek: clauth.CachedAsk})
		m = landClauth(t, m, clauthResultMsg{err: errors.New("clauth status --json: exit status 1: broken")})
		if got := fieldText(m.account, 120); !strings.Contains(got, "unavailable") {
			t.Errorf("the row does not report the failure:\n%s", got)
		}
		if m.clauthLoading {
			t.Error("Model.clauthLoading survived the failure")
		}
	})
}

// landClauth delivers a clauth answer at the version the model is waiting
// on, through Update, so the version guard is exercised rather than stepped
// around.
func landClauth(t *testing.T, m Model, msg clauthResultMsg) Model {
	t.Helper()
	msg.version = m.reqs.clauth
	next, _ := m.Update(msg)
	return next.(Model)
}

// The focus reload is suppressed while the row is loading: the open-time
// read is already out, and a second one would retire it through the version
// guard -- turning a focus into a reason the first answer never got to
// give.
func TestClauthLoading_FocusDoesNotAskAgain(t *testing.T) {
	cl := &fakeClauth{peek: clauth.CachedAsk, status: twoProfileStatus()}
	m := loadingClauthModel(t, cl)
	before := m.reqs.clauth

	m.form.FocusByID("account")
	for _, cmd := range m.reactToChanges() {
		if cmd == nil {
			continue
		}
		if _, ok := cmd().(clauthResultMsg); ok {
			t.Fatal("focusing a loading account row scheduled a second clauth read")
		}
	}
	if m.reqs.clauth != before {
		t.Errorf("reqs.clauth moved from %d to %d on focus, retiring the read in flight", before, m.reqs.clauth)
	}
}

// §7.4: a ⌃R⌃R asks clauth again -- the cost is one more `clauth status
// --json`, which nobody has to approve -- and superseded() has already
// moved the counter past the discarded form's read, so the first answer is
// dropped.
func TestClauthLoading_ARebuildAsksAgainAndDropsTheOldAnswer(t *testing.T) {
	cl := &fakeClauth{peek: clauth.CachedAsk, status: twoProfileStatus()}
	m := loadingClauthModel(t, cl)
	stale := clauthResultMsg{version: m.reqs.clauth, status: twoProfileStatus()}

	fresh := mustClear(t, m)
	if !fresh.clauthLoading {
		t.Fatal("the rebuilt form forgot that clauth was being read")
	}
	var asked bool
	for _, cmd := range fresh.initCmds {
		if cmd == nil {
			continue
		}
		if _, ok := cmd().(clauthResultMsg); ok {
			asked = true
		}
	}
	if !asked {
		t.Fatal("the rebuilt form never asked clauth again")
	}

	next, _ := fresh.Update(stale)
	if got := next.(Model); !got.clauthLoading {
		t.Fatal("the discarded form's answer landed on the rebuilt one")
	}
}

// --- §5.4: the probe's verdict ---------------------------------------------

// probingModel is the popup a person with a configured picker sees the
// instant it opens: a live account row offering `auto`, and the probe still
// owed.
func probingModel(t *testing.T, p *fakePicker) Model {
	t.Helper()
	return newTestModel(t, testSetup{
		Clauth:             &fakeClauth{status: twoProfileStatus()},
		ClauthStatus:       twoProfileStatus(),
		Picker:             p,
		PickerProbePending: true,
		Config:             config.Config{Clauth: config.ClauthConfig{Picker: "stub"}},
	})
}

func TestProbe_ThePreviewCarriesIt(t *testing.T) {
	p := &fakePicker{res: pickedResult()}
	m := probingModel(t, p)

	m = runPreview(t, m, "/repo")
	if len(p.calls) != 1 {
		t.Fatalf("the picker ran %d times, want exactly one call doing both jobs", len(p.calls))
	}
	if !p.calls[0].Probe || !p.calls[0].DryRun {
		t.Fatalf("the opening preview = %+v, want a probed --dry-run", p.calls[0])
	}
	if m.pickerProbePending {
		t.Error("Model.pickerProbePending survived a probed answer")
	}
	if m.deps.Picker == nil {
		t.Error("a passing probe dropped the picker")
	}
}

func TestProbe_AFailureRemovesAutoAndSaysWhy(t *testing.T) {
	p := &fakePicker{err: &picker.ProbeError{Reason: "stub answered without the documented keys profile, reason"}}
	m := probingModel(t, p)
	m.account.SetAgentIsClaude(true)

	m = runPreview(t, m, "/repo")

	if m.deps.Picker != nil {
		t.Error("Deps.Picker survived a failed probe")
	}
	if m.account.IsAuto() {
		t.Error("`auto` survived a failed probe")
	}
	if m.pickerUnavailable == "" {
		t.Error("Model.pickerUnavailable is empty after a failed probe")
	}
	if got := fieldText(m.account, 200); !strings.Contains(got, "documented keys") {
		t.Errorf("the account row does not carry the probe's reason as a note:\n%s", got)
	}
}

// The verdict is about the EXECUTABLE, so neither of handlePickerPreview's
// guards may discard it: not the version, and not a submit being out on a
// round trip. A dropped verdict leaves pickerProbePending set for the rest
// of the popup and refuses every `auto` submit (§7.3).
func TestProbe_TheFirstProbedAnswerDecidesAcrossAVersionBump(t *testing.T) {
	p := &fakePicker{err: &picker.ProbeError{Reason: "stub is not a picker"}}
	m := probingModel(t, p)

	// The answer to the OPENING preview, which a project change has since
	// superseded.
	stale := pickerPreviewMsg{req: request{version: m.reqs.picker, key: "/repo"}, err: p.err, probed: true}
	m.reqs.picker += 3

	m, _ = m.handlePickerPreview(stale)
	if m.pickerProbePending {
		t.Fatal("a superseded answer dropped the verdict about the executable")
	}
	if m.deps.Picker != nil {
		t.Fatal("a superseded answer dropped the verdict about the executable")
	}
}

func TestProbe_AnAnswerLandingDuringASubmitRoundTripStillDecides(t *testing.T) {
	p := &fakePicker{err: &picker.ProbeError{Reason: "stub is not a picker"}}
	m := probingModel(t, p)
	m.submitResolving = true

	msg := pickerPreviewMsg{req: request{version: m.reqs.picker, key: "/repo"}, err: p.err, probed: true}
	m, _ = m.handlePickerPreview(msg)
	if m.pickerProbePending {
		t.Fatal("a verdict landing during a round trip was discarded with the preview")
	}
}

// Pick's OTHER errors are about this call, not about the executable: they
// reach the row as any preview's reason does and leave `auto` alone.
func TestProbe_APlainErrorIsNotAVerdict(t *testing.T) {
	p := &fakePicker{err: errors.New("picker stub: exit 0 (picked) but no profile in the answer")}
	m := probingModel(t, p)

	m = runPreview(t, m, "/repo")
	if m.pickerProbePending {
		t.Error("the probe is still owed after an answer that carried it")
	}
	if m.deps.Picker == nil {
		t.Error("a plain error dropped the picker, want it treated as this call's own failure")
	}
	if m.pickerUnavailable != "" {
		t.Errorf("Model.pickerUnavailable = %q for an error that is not a verdict", m.pickerUnavailable)
	}
}

func pickedResult() picker.Result {
	return picker.Result{Profile: "alpha-1", Tier: "Max", ConfigDir: "/dirs/alpha-1"}
}

// --- §5.5: the Lifetime ----------------------------------------------------

// Quitting the popup kills the work it started, and #293 gives it two more
// pieces to kill: an `op read` waiting on a person, and `clauth status
// --json`. Both run on lt.Begin(), so a cancelled Lifetime reaches them.
func TestLifetime_CancelsTheKeyResolveAndTheClauthRead(t *testing.T) {
	lt := NewLifetime()

	var keyCtx context.Context
	keyCmd := resolveKeyCmd(lt, func(ctx context.Context) (string, error) {
		keyCtx = ctx
		return "lin_api_x", nil
	})
	keyCmd()

	cl := &fakeCtxClauth{}
	m := New(Setup{
		Deps:          Deps{Runner: &fakeRunner{}, Clauth: cl, Git: newFakeGit(), Clock: noSleep, Lifetime: lt},
		Palette:       theme.Default(),
		ClauthLoading: true,
	})
	reload := m.reloadClauthCmd()
	if reload == nil {
		t.Fatal("reloadClauthCmd returned nothing")
	}
	reload()

	lt.Shutdown(0)

	for name, ctx := range map[string]context.Context{"the key resolve": keyCtx, "the clauth read": cl.ctx} {
		if ctx == nil {
			t.Fatalf("%s ran on no context at all", name)
			continue
		}
		if ctx.Err() == nil {
			t.Errorf("%s survived the quit -- it is not on the Lifetime", name)
		}
	}
}

// fakeCtxClauth records the context its Status call was handed, which is
// the whole of what TestLifetime_... asserts.
type fakeCtxClauth struct{ ctx context.Context }

func (f *fakeCtxClauth) Status(ctx context.Context) (clauth.Status, error) {
	f.ctx = ctx
	return clauth.Status{}, nil
}

func (f *fakeCtxClauth) Peek() (clauth.Status, clauth.Cached) {
	return clauth.Status{}, clauth.CachedAsk
}

// --- §7: the holds ---------------------------------------------------------

// heldSubmitModel is a form one ⌃S away from creating: a title, a settled
// project check and claude selected -- with whatever #293 state the caller
// asked for still out.
func heldSubmitModel(t *testing.T, s testSetup) Model {
	t.Helper()
	if s.Ctx.WorkspaceCwd == "" {
		s.Ctx = herdrc.Context{WorkspaceCwd: "/repo"}
	}
	m := newTestModel(t, s)
	m = settle(t, m)
	// Set directly rather than typed, as every other submit test in this
	// package does: a routed keystroke would schedule the title-duplicate
	// check, and a submit held for THAT (#202) is not what these tests are
	// about.
	m.title.SetTitle("Fix pagination", false)
	m.prompt.SetValue("do it", false)
	return m
}

// loadingClauthSubmitModel is heldSubmitModel with the account row still
// waiting on clauth.
func loadingClauthSubmitModel(t *testing.T, cfg config.Config) Model {
	t.Helper()
	return heldSubmitModel(t, testSetup{
		Clauth:        &fakeClauth{peek: clauth.CachedAsk, status: twoProfileStatus()},
		ClauthLoading: true,
		Config:        cfg,
	})
}

func TestClauthHold_ALandingReleasesTheSubmit(t *testing.T) {
	m := loadingClauthSubmitModel(t, config.Config{})

	next, cmd := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting {
		t.Fatal("⌃S went ahead while clauth was still being read")
	}
	if !m.submitHeld || m.accountHeld != accountHoldClauth {
		t.Fatalf("submitHeld=%v accountHeld=%v, want the clauth hold", m.submitHeld, m.accountHeld)
	}
	if cmd == nil {
		t.Fatal("the hold armed no timer -- ⌃S would wait for good")
	}

	m = landClauth(t, m, clauthResultMsg{status: twoProfileStatus()})
	if !m.submitting {
		t.Fatal("the submit did not resume once clauth answered")
	}
}

// Decision 5: on timeout the create goes ahead WITHOUT the #243
// dead-credential check, under what the row would have selected from config
// alone (§7.2). Three cases, which are the live row's own precedence read
// off config instead of off a field that has no profile list.
func TestClauthHold_TheTimerProceedsUnderTheAccountConfigNames(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cfg    config.Config
		picker pickerSource
		pin    string
	}{
		{
			name: "a [clauth] default naming a profile",
			cfg:  config.Config{Clauth: config.ClauthConfig{Default: "work"}},
			pin:  "work",
		},
		{
			name: "the documented `active` sentinel is not a profile",
			cfg:  config.Config{Clauth: config.ClauthConfig{Default: "active"}},
		},
		{
			name: "nothing named at all",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := loadingClauthSubmitModel(t, tc.cfg)
			m = spendTheAccountBudget(t, m)

			if !m.submitting {
				t.Fatal("the submit did not go ahead once the budget was spent")
			}
			if got := m.submitInput.AccountPin; got != tc.pin {
				t.Errorf("AccountPin = %q, want %q", got, tc.pin)
			}
		})
	}
}

// The picker case is its own test because it does not launch: `auto` is a
// promise to be picked for, so the submit goes through the commit pick
// first (§7.2 item 2).
func TestClauthHold_TheTimerRestsOnAutoWithAPickerConfigured(t *testing.T) {
	p := &fakePicker{res: pickedResult()}
	m := heldSubmitModel(t, testSetup{
		Clauth:        &fakeClauth{peek: clauth.CachedAsk, status: twoProfileStatus()},
		ClauthLoading: true,
		Picker:        p,
		Config:        config.Config{Clauth: config.ClauthConfig{Picker: "stub"}},
	})

	m = spendTheAccountBudget(t, m)
	if m.submitting {
		t.Fatal("the submit launched without spending the pick it rests on")
	}
	if !m.submitResolving {
		t.Fatal("the submit is neither resolving nor launched -- it is lost")
	}
	if !m.accountFromConfig.auto {
		t.Errorf("accountFromConfig = %+v, want `auto`", m.accountFromConfig)
	}
}

// A #243-blocked profile is the check the spent budget skips (decision 5):
// the create goes ahead without it, because there is no status to check
// against. With clauth ANSWERED it still blocks, which is what makes the
// skip a consequence of the timeout rather than a hole.
//
// The first case hands the Model a status it should NOT consult, which is
// how the skip is held to being explicit. A form that reaches the hold
// really does have an empty clauthStatus -- Bootstrap only fills it from a
// fresh status file, which is the case that never loads -- so a skip that
// worked only because the lookup happened to find nothing would pass every
// test here and be one refactor from refusing a create on a status nobody
// read. The `return nil, false` that ends the branch is what this asserts.
func TestClauthHold_TheTimerSkipsTheDeadCredentialCheck(t *testing.T) {
	broken := clauth.Status{Schema: 1, ActiveProfile: "work", Profiles: []clauth.Profile{
		{Name: "work", Tier: "Max", AuthStatus: string(clauth.AuthBroken)},
		{Name: "home", Tier: "Max", AuthStatus: "ok"},
	}}
	cfg := config.Config{Clauth: config.ClauthConfig{Default: "work"}}

	t.Run("the budget is spent", func(t *testing.T) {
		m := heldSubmitModel(t, testSetup{
			Clauth:        &fakeClauth{peek: clauth.CachedAsk, status: broken},
			ClauthStatus:  broken, // see the doc comment: deliberately there to be ignored
			ClauthLoading: true,
			Config:        cfg,
		})
		m = spendTheAccountBudget(t, m)
		if !m.submitting {
			t.Fatal("the create was refused for a credential nothing had read")
		}
		if got := m.submitInput.AccountPin; got != "work" {
			t.Errorf("AccountPin = %q, want the config default pinned without verification", got)
		}
	})

	t.Run("clauth answers instead", func(t *testing.T) {
		m := heldSubmitModel(t, testSetup{
			Clauth:        &fakeClauth{peek: clauth.CachedAsk, status: broken},
			ClauthLoading: true,
			Config:        cfg,
		})
		next, _ := m.Update(form.SubmitMsg{})
		m = landClauth(t, next.(Model), clauthResultMsg{status: broken})
		if m.submitting {
			t.Fatal("a profile clauth reports broken launched anyway once clauth had answered")
		}
	})
}

// §7.2's last paragraph: a clauth answer landing while the submit is out on
// its round trip updates the ROW and not the submit in flight.
func TestClauthHold_AnAnswerDuringTheRoundTripDoesNotChangeTheAccount(t *testing.T) {
	p := &fakePicker{res: pickedResult()}
	m := heldSubmitModel(t, testSetup{
		Clauth:        &fakeClauth{peek: clauth.CachedAsk, status: twoProfileStatus()},
		ClauthLoading: true,
		Picker:        p,
		Config:        config.Config{Clauth: config.ClauthConfig{Picker: "stub"}},
	})
	m = spendTheAccountBudget(t, m)
	if !m.submitResolving {
		t.Fatal("the submit is not out on the commit pick")
	}

	// A profile list arrives, carrying a [clauth] default this form never
	// saw. The row takes it; the submit does not.
	m = landClauth(t, m, clauthResultMsg{status: twoProfileStatus()})
	if !m.accountFromConfig.set {
		t.Fatal("the recorded account was discarded by a late clauth answer")
	}
	next, _ := m.Update(pickerCommitMsg{res: pickedResult()})
	m = next.(Model)
	if got := m.submitInput.AccountPin; got != pickedResult().Profile {
		t.Errorf("AccountPin = %q, want the picker's own answer", got)
	}
}

// §7.3, case one: the budget runs out with the probe still pending. An
// unknown is not a pass, and launching unpinned under whatever account is
// live is what handlePickerCommit exists to prevent.
func TestProbeHold_APendingProbeRefusesAtTheDeadline(t *testing.T) {
	p := &fakePicker{res: pickedResult()}
	m := heldSubmitModel(t, testSetup{
		Clauth:             &fakeClauth{status: twoProfileStatus()},
		ClauthStatus:       twoProfileStatus(),
		Picker:             p,
		PickerProbePending: true,
		Config:             config.Config{Clauth: config.ClauthConfig{Picker: "stub"}},
	})

	next, cmd := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitting {
		t.Fatal("⌃S spent a commit pick on an executable that has not answered the probe")
	}
	if m.accountHeld != accountHoldProbe {
		t.Fatalf("accountHeld = %v, want the probe hold", m.accountHeld)
	}
	if !m.submitResolving {
		t.Error("the probe hold did not freeze the form -- an edit could reach a plan validation never saw")
	}

	m = fireAccountWait(t, m, cmd)
	if m.submitting {
		t.Fatal("a pending probe let the submit through")
	}
	if m.submitResolving {
		t.Error("the refusal left the form frozen")
	}
	if len(p.calls) != 0 {
		t.Fatalf("the picker was run %d times after refusing, want 0", len(p.calls))
	}
	if got := fieldText(m.account, 200); !strings.Contains(got, "picker has not answered yet") {
		t.Errorf("the account panel does not say why:\n%s", got)
	}
	if got := m.form.FocusedID(); got != "account" {
		t.Errorf("focus = %q, want the row the refusal is about", got)
	}
}

// §7.3, case two: the probe FAILS while the submit waits on it. Removing
// `auto` would otherwise release the hold and launch the submit unpinned.
func TestProbeHold_AProbeThatFailsDuringTheHoldRefuses(t *testing.T) {
	p := &fakePicker{err: &picker.ProbeError{Reason: "stub is not a picker"}}
	m := heldSubmitModel(t, testSetup{
		Clauth:             &fakeClauth{status: twoProfileStatus()},
		ClauthStatus:       twoProfileStatus(),
		Picker:             p,
		PickerProbePending: true,
		Config:             config.Config{Clauth: config.ClauthConfig{Picker: "stub"}},
	})

	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.accountHeld != accountHoldProbe {
		t.Fatalf("accountHeld = %v, want the probe hold", m.accountHeld)
	}

	m, _ = m.handlePickerPreview(pickerPreviewMsg{
		req: request{version: m.reqs.picker, key: "/repo"}, err: p.err, probed: true,
	})
	if m.submitting {
		t.Fatal("a failed probe released the submit to launch unpinned")
	}
	if m.submitHeld {
		t.Error("the submit is still held after a verdict that will never change")
	}
	if got := fieldText(m.account, 200); !strings.Contains(got, "not a picker") {
		t.Errorf("the account panel does not carry the probe's own reason:\n%s", got)
	}
}

// And a probe that PASSES during the hold releases it into the commit pick.
func TestProbeHold_APassingProbeReleasesTheSubmit(t *testing.T) {
	p := &fakePicker{res: pickedResult()}
	m := heldSubmitModel(t, testSetup{
		Clauth:             &fakeClauth{status: twoProfileStatus()},
		ClauthStatus:       twoProfileStatus(),
		Picker:             p,
		PickerProbePending: true,
		Config:             config.Config{Clauth: config.ClauthConfig{Picker: "stub"}},
	})
	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)

	m, cmd := m.handlePickerPreview(pickerPreviewMsg{
		req: request{version: m.reqs.picker, key: "/repo"}, res: pickedResult(), probed: true,
	})
	if cmd == nil {
		t.Fatal("the released submit scheduled nothing")
	}
	if _, ok := cmd().(pickerCommitMsg); !ok {
		t.Fatalf("the released submit scheduled %T, want the commit pick", cmd())
	}
}

// Both holds apply only to claude, the one kind an account is pinned or
// picked for. A codex submit never waits on clauth or a picker.
func TestAccountHolds_ACodexSubmitNeverHolds(t *testing.T) {
	m := loadingClauthSubmitModel(t, config.Config{Agents: config.AgentsConfig{Favorites: []string{"codex", "claude"}}})
	m.agent.SetKind("codex")
	m.syncDerivedInertness()

	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitHeld {
		t.Fatal("a codex submit waited for clauth")
	}
	if !m.submitting {
		t.Fatal("a codex submit did not go ahead")
	}
}

// The holds sit AFTER every validation that does not need an account, so a
// required title is refused at once rather than five seconds later.
func TestAccountHolds_ARequiredTitleIsRefusedAtOnce(t *testing.T) {
	m := loadingClauthSubmitModel(t, config.Config{})
	m.title.SetTitle("", false)

	next, _ := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if m.submitHeld {
		t.Fatal("an empty title waited for clauth before being refused")
	}
	if got := fieldText(m.title, 120); !strings.Contains(got, "title required") {
		t.Errorf("the title row does not say what is wrong:\n%s", got)
	}
}

// ONE BUDGET, ARMED ONCE PER ⌃S (§7.1). Re-entries must not re-arm it, or
// every landing would push the deadline back and the budget would never be
// spent.
func TestAccountWait_ArmedOncePerSubmit(t *testing.T) {
	m := loadingClauthSubmitModel(t, config.Config{})

	next, cmd := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("the first hold armed no timer")
	}
	armed := m.reqs.accountWait

	// A re-entry: the same hold, reached again from handleSubmit.
	m2, again := m.handleSubmit()
	if again != nil {
		t.Fatal("a re-entry armed a second timer")
	}
	if m2.reqs.accountWait != armed {
		t.Errorf("reqs.accountWait moved from %d to %d on a re-entry", armed, m2.reqs.accountWait)
	}

	// A NEW ⌃S may arm a new one.
	next, cmd = m2.Update(form.SubmitMsg{})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("a fresh ⌃S armed no timer")
	}
	if m.reqs.accountWait == armed {
		t.Error("a fresh ⌃S reused the previous submit's timer version")
	}
	if m.accountWaitExpired {
		t.Error("a fresh ⌃S inherited the previous submit's spent budget")
	}
}

// A timer from a submit that has since been released must not release the
// next hold, which is what the version on the message is for.
func TestAccountWait_AStaleTimerNeverReleasesALaterHold(t *testing.T) {
	m := loadingClauthSubmitModel(t, config.Config{})
	next, cmd := m.Update(form.SubmitMsg{})
	m = next.(Model)
	stale, ok := cmd().(accountWaitMsg)
	if !ok {
		t.Fatalf("the hold armed %T, want accountWaitMsg", cmd())
	}

	// A second ⌃S arms its own timer, one version on.
	next, _ = m.Update(form.SubmitMsg{})
	m = next.(Model)

	next, _ = m.Update(stale)
	m = next.(Model)
	if m.accountWaitExpired {
		t.Fatal("a stale timer spent the budget of a later hold")
	}
	if m.submitting {
		t.Fatal("a stale timer released a later hold")
	}
}

// spendTheAccountBudget presses ⌃S and lets the one timer it arms fire,
// which is the whole of draw-first spec §7's "hold, then proceed".
func spendTheAccountBudget(t *testing.T, m Model) Model {
	t.Helper()
	next, cmd := m.Update(form.SubmitMsg{})
	m = next.(Model)
	if !m.submitHeld {
		t.Fatal("⌃S did not hold")
	}
	return fireAccountWait(t, m, cmd)
}

// fireAccountWait runs the timer a hold armed and delivers it.
func fireAccountWait(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("no timer to fire")
	}
	msg, ok := cmd().(accountWaitMsg)
	if !ok {
		t.Fatalf("the hold armed %T, want accountWaitMsg", cmd())
	}
	next, _ := m.Update(msg)
	return next.(Model)
}
