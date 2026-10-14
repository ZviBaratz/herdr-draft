package machine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ZviBaratz/herdr-draft/internal/gitx"
	"github.com/ZviBaratz/herdr-draft/internal/herdrc"
)

var (
	dev      = herdrc.MachineInfo{ID: "af09", Label: "dev (EC2)", Target: "dev", Session: "default", Enabled: true}
	nanoclaw = herdrc.MachineInfo{ID: "fb47", Label: "nanoclaw", Target: "nanoclaw", Session: "default", Enabled: true}
)

// TestSelectResolvesInTheSpecsOrder is machine spec §4.1: local, then a
// profile id, then an exact label, then a unique ssh target -- and whatever
// matched, the choice carries the profile, so herdr is handed its id.
func TestSelectResolvesInTheSpecsOrder(t *testing.T) {
	saved := []herdrc.MachineInfo{dev, nanoclaw}
	for _, sel := range []string{"af09", "dev (EC2)", "dev"} {
		c, err := Select(saved, sel)
		if err != nil || c.Info != dev || c.Selector != sel {
			t.Errorf("Select(%q) = %+v, %v; want dev", sel, c, err)
		}
	}
	c, err := Select(saved, "local")
	if err != nil || c.Remote() || c.Selector != "local" {
		t.Errorf("Select(local) = %+v, %v; want this machine", c, err)
	}
	if c.Label() != "local" || (Choice{Info: dev}).Label() != "dev (EC2)" {
		t.Errorf("labels: %q, %q", c.Label(), (Choice{Info: dev}).Label())
	}
}

// TestSelectRefusesWhatItCannotNameOnce: an unknown selector, a disabled
// profile, a target two profiles share, and a selector that is one
// profile's label and another's target are all refused, by name.
func TestSelectRefusesWhatItCannotNameOnce(t *testing.T) {
	disabled := dev
	disabled.Enabled = false
	devAgain := herdrc.MachineInfo{ID: "cc01", Label: "dev agents", Target: "dev", Session: "agents", Enabled: true}
	confusing := herdrc.MachineInfo{ID: "dd02", Label: "dev", Target: "devbox", Enabled: true}
	tests := []struct {
		name  string
		saved []herdrc.MachineInfo
		sel   string
		frag  string
	}{
		{"unknown", []herdrc.MachineInfo{dev}, "prod", `no saved herdr machine "prod"`},
		{"disabled", []herdrc.MachineInfo{disabled}, "dev", "disabled"},
		{"shared target", []herdrc.MachineInfo{dev, devAgain}, "dev", "ambiguous"},
		{"label of one, target of another", []herdrc.MachineInfo{dev, confusing}, "dev", "ambiguous"},
	}
	for _, tt := range tests {
		_, err := Select(tt.saved, tt.sel)
		if err == nil || !strings.Contains(err.Error(), tt.frag) {
			t.Errorf("%s: Select(%q) = %v; want an error mentioning %q", tt.name, tt.sel, err, tt.frag)
		}
	}
}

// TestSelectLocalIsAlwaysThisMachine: a profile someone labelled "local"
// is reachable by its id, never by the word that means this machine.
func TestSelectLocalIsAlwaysThisMachine(t *testing.T) {
	odd := herdrc.MachineInfo{ID: "ee03", Label: "local", Target: "box", Enabled: true}
	if c, err := Select([]herdrc.MachineInfo{odd}, "local"); err != nil || c.Remote() {
		t.Errorf("Select(local) = %+v, %v; want this machine", c, err)
	}
	if c, err := Select([]herdrc.MachineInfo{odd}, "ee03"); err != nil || c.Info != odd {
		t.Errorf("Select(ee03) = %+v, %v; want the profile labelled local", c, err)
	}
}

// TestRelIsThePathUnderHome is §6.1: a project carries to another machine
// as its path under the laptop's home; outside home there is nothing to
// carry.
func TestRelIsThePathUnderHome(t *testing.T) {
	if got, err := Rel("/home/zvi", "/home/zvi/quantivly/hub"); err != nil || got != "quantivly/hub" {
		t.Errorf("Rel = %q, %v", got, err)
	}
	for _, key := range []string{"/srv/repo", "/home/zvi2/repo", "/home/zvi", ""} {
		if got, err := Rel("/home/zvi", key); err == nil {
			t.Errorf("Rel(%q) = %q, nil; want a refusal", key, got)
		}
	}
	if _, err := Rel("", "/home/zvi/x"); err == nil {
		t.Error("Rel with no home accepted a key")
	}
}

type refs struct {
	full   map[string]string
	head   string
	origin map[string]bool
}

func (r refs) FullRefName(_ context.Context, _, name string) (string, error) {
	if f, ok := r.full[name]; ok {
		return f, nil
	}
	return "", errors.New("no such ref")
}
func (r refs) OriginHead(context.Context, string) (string, error) { return r.head, nil }
func (r refs) OriginBranchExists(_ context.Context, _, name string) (bool, error) {
	return r.origin[name], nil
}
func (r refs) OriginURL(context.Context, string) (string, error) { return "", nil }

// TestRemoteBaseIsARefTheRemoteCanFetch is §6.4's table.
func TestRemoteBaseIsARefTheRemoteCanFetch(t *testing.T) {
	g := refs{
		full: map[string]string{
			"origin/develop": "refs/remotes/origin/develop",
			"main":           "refs/heads/main",
			"local-only":     "refs/heads/local-only",
			"v1.0":           "refs/tags/v1.0",
			"upstream/x":     "refs/remotes/upstream/x",
			"0123abcd":       "",
		},
		head:   "origin/main",
		origin: map[string]bool{"main": true},
	}
	ok := map[string]string{
		"":               "origin/main",
		"HEAD":           "origin/main",
		"@":              "origin/main",
		"origin/develop": "origin/develop",
		"main":           "origin/main",
	}
	for base, want := range ok {
		got, err := RemoteBase(context.Background(), g, "/repo", base, "dev (EC2)")
		if err != nil || got != want {
			t.Errorf("RemoteBase(%q) = %q, %v; want %q", base, got, err, want)
		}
	}
	for _, base := range []string{"local-only", "v1.0", "upstream/x", "0123abcd", "nope"} {
		got, err := RemoteBase(context.Background(), g, "/repo", base, "dev (EC2)")
		if err == nil {
			t.Errorf("RemoteBase(%q) = %q, nil; want a refusal", base, got)
			continue
		}
		if !strings.Contains(err.Error(), "dev (EC2)") {
			t.Errorf("RemoteBase(%q)'s refusal does not name the machine: %v", base, err)
		}
	}
	noHead := g
	noHead.head = ""
	if _, err := RemoteBase(context.Background(), noHead, "/repo", "", "dev (EC2)"); err == nil {
		t.Error("an empty base with no origin/HEAD was accepted")
	}
}

// TestNormalizeOriginComparesTheRepositoryNotTheTransport: dev rewrites ssh
// remotes to https (machine spec §7), so the same repository arrives in
// several spellings.
func TestNormalizeOriginComparesTheRepositoryNotTheTransport(t *testing.T) {
	want := "github.com/quantivly/hub"
	for _, u := range []string{
		"git@github.com:quantivly/hub.git",
		"https://github.com/quantivly/hub.git",
		"ssh://git@github.com/quantivly/hub",
		"https://zvi@github.com/Quantivly/Hub/",
	} {
		if got := NormalizeOrigin(u); got != want {
			t.Errorf("NormalizeOrigin(%q) = %q, want %q", u, got, want)
		}
	}
}

// TestJudgeRefusesWhatCannotHostTheSession is §7's three refusals that
// come from the clone probe, each with its sentinel for create's exit code.
func TestJudgeRefusesWhatCannotHostTheSession(t *testing.T) {
	c := Choice{Selector: "dev", Info: dev}
	repo := gitx.CloneProbe{Home: "/home/ubuntu", Dir: "/home/ubuntu/quantivly/hub", Exists: true, IsRepo: true,
		Origin: "https://github.com/quantivly/hub.git"}

	dir, err := Judge(Report{Clone: repo}, c, "quantivly/hub", true, "git@github.com:quantivly/hub.git")
	if err != nil || dir != "/home/ubuntu/quantivly/hub" {
		t.Errorf("a matching clone: %q, %v", dir, err)
	}

	missing := repo
	missing.Exists, missing.IsRepo = false, false
	if _, err := Judge(Report{Clone: missing}, c, "quantivly/hub", false, ""); !errors.Is(err, ErrNoClone) ||
		!strings.Contains(err.Error(), "~/quantivly/hub is not on dev (EC2)") {
		t.Errorf("a missing directory: %v", err)
	}

	plain := repo
	plain.IsRepo = false
	if _, err := Judge(Report{Clone: plain}, c, "quantivly/hub", true, ""); !errors.Is(err, ErrNoClone) {
		t.Errorf("a worktree session in a plain directory: %v", err)
	}
	if dir, err := Judge(Report{Clone: plain}, c, "notes", false, ""); err != nil || dir != plain.Dir {
		t.Errorf("a plain directory without a worktree is fine: %q, %v", dir, err)
	}

	other := repo
	other.Origin = "https://github.com/someone/hub.git"
	if _, err := Judge(Report{Clone: other}, c, "quantivly/hub", true, "git@github.com:quantivly/hub.git"); !errors.Is(err, ErrWrongRepo) {
		t.Errorf("a different repository: %v", err)
	}
	// Nothing to compare is not a mismatch.
	if _, err := Judge(Report{Clone: other}, c, "quantivly/hub", true, ""); err != nil {
		t.Errorf("a laptop clone with no origin refused: %v", err)
	}
}

// TestHasBranch: the remote half of the duplicate-branch check.
func TestHasBranch(t *testing.T) {
	r := Report{Clone: gitx.CloneProbe{Branches: []string{"main", "zvi/old"}}}
	if !r.HasBranch("zvi/old") || r.HasBranch("zvi/new") {
		t.Errorf("HasBranch disagrees with %q", r.Clone.Branches)
	}
}

type probeRunner struct {
	herdrc.Runner
	machine string
	ws      []herdrc.WorkspaceInfo
	err     error
}

func (r *probeRunner) On(id string) herdrc.Runner { s := *r; s.machine = id; return &s }
func (r *probeRunner) WorkspaceList(context.Context) ([]herdrc.WorkspaceInfo, error) {
	if r.machine == "" {
		return nil, errors.New("asked this machine, not the remote")
	}
	return r.ws, r.err
}

// TestCLIProberAsksBothProbesOfTheMachine: the forwarding probe goes
// through the runner scoped to the profile id, the clone probe to its ssh
// target, and either failing is ErrUnreachable in that probe's own words.
func TestCLIProberAsksBothProbesOfTheMachine(t *testing.T) {
	c := Choice{Selector: "dev", Info: dev}
	var target, rel string
	clone := gitx.CloneProbe{Home: "/home/ubuntu", Exists: true}
	p := CLIProber{
		Runner: &probeRunner{ws: []herdrc.WorkspaceInfo{{Label: "fix it"}}},
		Clone: func(_ context.Context, tg, r string) (gitx.CloneProbe, error) {
			target, rel = tg, r
			return clone, nil
		},
	}
	got, err := p.Probe(context.Background(), c, "quantivly/hub")
	if err != nil || !reflect.DeepEqual(got.Clone, clone) || len(got.Workspaces) != 1 {
		t.Fatalf("Probe = %+v, %v", got, err)
	}
	if target != "dev" || rel != "quantivly/hub" {
		t.Errorf("clone probe asked %q about %q", target, rel)
	}

	p.Runner = &probeRunner{err: errors.New("remote Herdr does not support machine API forwarding")}
	if _, err := p.Probe(context.Background(), c, "quantivly/hub"); !errors.Is(err, ErrUnreachable) ||
		!strings.Contains(err.Error(), "machine API forwarding") {
		t.Errorf("forwarding refused: %v", err)
	}
	p.Runner = &probeRunner{}
	p.Clone = func(context.Context, string, string) (gitx.CloneProbe, error) {
		return gitx.CloneProbe{}, errors.New("Token for dev is expired")
	}
	if _, err := p.Probe(context.Background(), c, "quantivly/hub"); !errors.Is(err, ErrUnreachable) ||
		!strings.Contains(err.Error(), "Token for dev is expired") {
		t.Errorf("ssh failed: %v", err)
	}
}

// TestCLIProberHonoursItsContext: the probe is bounded by its caller's
// deadline (awaitCheck in the popup, deps.checks in create); a probe that
// never answers returns when the context does.
func TestCLIProberHonoursItsContext(t *testing.T) {
	p := CLIProber{
		Runner: &probeRunner{},
		Clone: func(ctx context.Context, _, _ string) (gitx.CloneProbe, error) {
			<-ctx.Done()
			return gitx.CloneProbe{}, ctx.Err()
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := p.Probe(ctx, Choice{Info: dev}, "x"); err == nil {
		t.Fatal("a probe past its deadline succeeded")
	}
}

// TestCLIProberReturnsOnTheFirstRefusal: herdr refusing forwarding is a
// definitive answer, and a hanging ssh beside it must not turn it into "no
// answer" (exit 5 instead of 3).
func TestCLIProberReturnsOnTheFirstRefusal(t *testing.T) {
	p := CLIProber{
		Runner: &probeRunner{err: errors.New("remote Herdr does not support machine API forwarding")},
		Clone: func(ctx context.Context, _, _ string) (gitx.CloneProbe, error) {
			<-ctx.Done()
			return gitx.CloneProbe{}, ctx.Err()
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, err := p.Probe(ctx, Choice{Info: dev}, "x")
	if !errors.Is(err, ErrUnreachable) || time.Since(start) > time.Second {
		t.Errorf("Probe = %v after %v; want herdr's refusal at once", err, time.Since(start))
	}
}

// TestJudgeComparesTheRepositoryNotTheHost: an ssh host alias or an
// insteadOf short form names the same repository by another host, so the
// owner/repository pair decides -- and a token in either URL never reaches
// the screen.
func TestJudgeComparesTheRepositoryNotTheHost(t *testing.T) {
	c := Choice{Info: dev}
	clone := gitx.CloneProbe{Dir: "/home/ubuntu/quantivly/hub", Exists: true, IsRepo: true,
		Origin: "https://ghp_secret@github.com/quantivly/hub.git"}
	for _, laptop := range []string{"git@github-work:quantivly/hub.git", "ssh://git@github.com:22/quantivly/hub"} {
		if _, err := Judge(Report{Clone: clone}, c, "quantivly/hub", true, laptop); err != nil {
			t.Errorf("laptop origin %q refused: %v", laptop, err)
		}
	}
	_, err := Judge(Report{Clone: clone}, c, "quantivly/hub", true, "git@github.com:someone/else.git")
	if !errors.Is(err, ErrWrongRepo) {
		t.Fatalf("a different repository: %v", err)
	}
	if strings.Contains(err.Error(), "ghp_secret") {
		t.Errorf("the refusal shows a credential: %v", err)
	}
}

// TestOfferedAcceptsAnyNameForAnOfferedMachine: config.toml may call it
// `dev`, and `create --json` hands back its label and id; all three name
// the one offered machine. A saved machine config.toml never names is
// still refused.
func TestOfferedAcceptsAnyNameForAnOfferedMachine(t *testing.T) {
	saved := []herdrc.MachineInfo{dev, nanoclaw}
	offered := []string{"local", "dev"}
	for _, sel := range []string{"dev", "dev (EC2)", "af09", "local"} {
		if _, err := SelectOffered(saved, offered, sel); err != nil {
			t.Errorf("SelectOffered(%q) = %v", sel, err)
		}
	}
	if _, err := SelectOffered(saved, offered, "nanoclaw"); err == nil || !strings.Contains(err.Error(), "not one config.toml's [machine] offers") {
		t.Errorf("SelectOffered(nanoclaw) = %v; want it refused as not offered", err)
	}
}

// TestRemoteBaseNamesTheRemedyForAMissingOriginHead: a clone made with
// `remote add` and `fetch` has no origin/HEAD, which refuses every remote
// worktree left at HEAD -- the refusal names the one command that fixes it.
func TestRemoteBaseNamesTheRemedyForAMissingOriginHead(t *testing.T) {
	_, err := RemoteBase(context.Background(), refs{}, "/repo", "", "dev (EC2)")
	if err == nil || !strings.Contains(err.Error(), "git remote set-head origin --auto") {
		t.Errorf("RemoteBase with no origin/HEAD = %v; want the remedy named", err)
	}
}
