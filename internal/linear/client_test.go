package linear

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return b
}

func fixtureBytes(t *testing.T) []byte {
	t.Helper()
	return readFixture(t, filepath.Join("testdata", "assigned.json"))
}

// TestAssignedIssuesRequestAndParse asserts the outbound request shape
// (bare-key Authorization header, spec §10 query in the body) and that the
// fixture response decodes into []Issue with nils handled for the "started"
// issue's null estimate/cycle.
func TestAssignedIssuesRequestAndParse(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixtureBytes(t))
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), APIKey: "lin_api_testkey123", Endpoint: srv.URL}
	issues, err := c.AssignedIssues(context.Background())
	if err != nil {
		t.Fatalf("AssignedIssues: %v", err)
	}

	if gotAuth != "lin_api_testkey123" {
		t.Errorf("Authorization header = %q, want bare key with no Bearer prefix", gotAuth)
	}
	if !strings.Contains(gotBody, "assignedIssues") {
		t.Errorf("request body missing %q: %s", "assignedIssues", gotBody)
	}
	if !strings.Contains(gotBody, "branchName") {
		t.Errorf("request body missing %q: %s", "branchName", gotBody)
	}

	if len(issues) != 2 {
		t.Fatalf("len(issues) = %d, want 2", len(issues))
	}

	unstarted := issues[0]
	if unstarted.Identifier != "ENG-142" {
		t.Errorf("issues[0].Identifier = %q, want ENG-142", unstarted.Identifier)
	}
	if unstarted.BranchName != "zvi/eng-142-fix-pagination-off-by-one" {
		t.Errorf("issues[0].BranchName = %q", unstarted.BranchName)
	}
	if unstarted.StateType != "unstarted" {
		t.Errorf("issues[0].StateType = %q, want unstarted", unstarted.StateType)
	}
	if unstarted.Estimate == nil || *unstarted.Estimate != 3 {
		t.Errorf("issues[0].Estimate = %v, want *3", unstarted.Estimate)
	}
	if unstarted.CycleNumber == nil || *unstarted.CycleNumber != 14 {
		t.Errorf("issues[0].CycleNumber = %v, want *14", unstarted.CycleNumber)
	}
	if unstarted.Priority != 2 {
		t.Errorf("issues[0].Priority = %d, want 2", unstarted.Priority)
	}

	started := issues[1]
	if started.Identifier != "ENG-108" {
		t.Errorf("issues[1].Identifier = %q, want ENG-108", started.Identifier)
	}
	if started.StateType != "started" {
		t.Errorf("issues[1].StateType = %q, want started", started.StateType)
	}
	if started.Estimate != nil {
		t.Errorf("issues[1].Estimate = %v, want nil", started.Estimate)
	}
	if started.CycleNumber != nil {
		t.Errorf("issues[1].CycleNumber = %v, want nil", started.CycleNumber)
	}
}

// TestAssignedIssuesGraphQLError asserts that a 200-status GraphQL response
// carrying a top-level "errors" array becomes a Go error, even though the
// HTTP transport itself succeeded.
func TestAssignedIssuesGraphQLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"errors":[{"message":"Authentication required, not authenticated"}]}`))
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), APIKey: "bad-key", Endpoint: srv.URL}
	_, err := c.AssignedIssues(context.Background())
	if err == nil {
		t.Fatal("AssignedIssues: got nil error, want error for GraphQL errors payload")
	}
	if !strings.Contains(err.Error(), "Authentication required") {
		t.Errorf("error = %v, want it to include the GraphQL error message", err)
	}
}

// TestAssignedIssuesNonOKStatus asserts a non-200 HTTP status (e.g. a
// malformed request Linear rejects before GraphQL execution) surfaces as an
// error rather than being silently parsed as an empty result.
func TestAssignedIssuesNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"errors":[{"message":"authentication failed"}]}`))
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), APIKey: "bad-key", Endpoint: srv.URL}
	_, err := c.AssignedIssues(context.Background())
	if err == nil {
		t.Fatal("AssignedIssues: got nil error, want error for non-200 status")
	}
}

// TestClientDefaultEndpoint asserts Client.Endpoint defaults to Linear's
// public GraphQL endpoint when empty -- verified indirectly by confirming a
// Client with no Endpoint set does not send its request to the test server
// (it targets the real default instead, which this test never lets happen
// since it substitutes a nil-safe check on the constant).
func TestClientDefaultEndpoint(t *testing.T) {
	c := &Client{}
	if c.endpoint() != defaultEndpoint {
		t.Errorf("endpoint() = %q, want %q", c.endpoint(), defaultEndpoint)
	}
	if defaultEndpoint != "https://api.linear.app/graphql" {
		t.Errorf("defaultEndpoint = %q, want https://api.linear.app/graphql", defaultEndpoint)
	}
}

// fakeAPIKeyCmd writes a disposable shell script to t.TempDir() that echoes
// stdout verbatim on success, mirroring internal/clauth/status_test.go's
// fakeClauth idiom.
func fakeAPIKeyCmd(t *testing.T, stdout string) []string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "api-key-cmd")
	script := "#!/bin/sh\ncat <<'EOF'\n" + stdout + "\nEOF\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{bin}
}

// fakeFailingAPIKeyCmd writes a disposable shell script that always fails
// with the given stderr message and exit code 1.
func fakeFailingAPIKeyCmd(t *testing.T, stderr string) []string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "api-key-cmd")
	script := "#!/bin/sh\necho \"" + stderr + "\" 1>&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{bin}
}

// writeConfigWithPerm writes an (empty-content, perm-only-matters) config.toml
// into a fresh temp dir at the given permission and returns the dir.
func writeConfigWithPerm(t *testing.T, perm os.FileMode) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[linear]\napi_key = \"lin_api_literal\"\n"), perm); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestResolveAPIKeyPrefersCmdOverEnvAndLiteral(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "from-env")
	cmd := fakeAPIKeyCmd(t, "from-cmd")
	key, err := ResolveAPIKey(cmd, "from-literal", t.TempDir())
	if err != nil {
		t.Fatalf("ResolveAPIKey: %v", err)
	}
	if key != "from-cmd" {
		t.Errorf("ResolveAPIKey = %q, want %q", key, "from-cmd")
	}
}

func TestResolveAPIKeyFallsBackToEnvWhenNoCmd(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "from-env")
	key, err := ResolveAPIKey(nil, "from-literal", t.TempDir())
	if err != nil {
		t.Fatalf("ResolveAPIKey: %v", err)
	}
	if key != "from-env" {
		t.Errorf("ResolveAPIKey = %q, want %q", key, "from-env")
	}
}

func TestResolveAPIKeyFallsBackToLiteralWhenNoCmdOrEnv(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	dir := writeConfigWithPerm(t, 0o600)
	key, err := ResolveAPIKey(nil, "from-literal", dir)
	if err != nil {
		t.Fatalf("ResolveAPIKey: %v", err)
	}
	if key != "from-literal" {
		t.Errorf("ResolveAPIKey = %q, want %q", key, "from-literal")
	}
}

func TestResolveAPIKeyEmptyCmdOutputFallsThroughToEnv(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "from-env")
	cmd := fakeAPIKeyCmd(t, "")
	key, err := ResolveAPIKey(cmd, "from-literal", t.TempDir())
	if err != nil {
		t.Fatalf("ResolveAPIKey: %v", err)
	}
	if key != "from-env" {
		t.Errorf("ResolveAPIKey = %q, want %q", key, "from-env")
	}
}

func TestResolveAPIKeyCmdFailureIsAnError(t *testing.T) {
	cmd := fakeFailingAPIKeyCmd(t, "pass: not in the password store")
	_, err := ResolveAPIKey(cmd, "from-literal", t.TempDir())
	if err == nil {
		t.Fatal("ResolveAPIKey: got nil error, want error when api_key_cmd fails")
	}
}

func TestResolveAPIKeyLiteralRejectsWidePerms(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	dir := writeConfigWithPerm(t, 0o644)
	_, err := ResolveAPIKey(nil, "from-literal", dir)
	if err == nil {
		t.Fatal("ResolveAPIKey: got nil error, want error for config.toml perms wider than 0600")
	}
}

func TestResolveAPIKeyAllAbsentReturnsEmptyNoError(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	key, err := ResolveAPIKey(nil, "", t.TempDir())
	if err != nil {
		t.Fatalf("ResolveAPIKey: %v", err)
	}
	if key != "" {
		t.Errorf("ResolveAPIKey = %q, want empty", key)
	}
}

func TestSaveLoadCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	est := 3.0
	cyc := 14
	want := []Issue{
		{Identifier: "ENG-142", Title: "Fix pagination", BranchName: "zvi/eng-142", URL: "https://linear.app/x", Description: "d", StateName: "Todo", StateType: "unstarted", Estimate: &est, Priority: 2, CycleNumber: &cyc},
		{Identifier: "ENG-108", Title: "Add retry", BranchName: "zvi/eng-108", URL: "https://linear.app/y", Description: "", StateName: "In Progress", StateType: "started", Estimate: nil, Priority: 1, CycleNumber: nil},
	}

	before := time.Now().Add(-time.Second)
	if err := SaveCache(dir, want); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}
	after := time.Now().Add(time.Second)

	got, ts, err := LoadCache(dir)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Identifier != want[i].Identifier || got[i].BranchName != want[i].BranchName {
			t.Errorf("got[%d] = %+v, want %+v", i, got[i], want[i])
		}
		if (got[i].Estimate == nil) != (want[i].Estimate == nil) {
			t.Errorf("got[%d].Estimate = %v, want %v", i, got[i].Estimate, want[i].Estimate)
		}
		if got[i].Estimate != nil && *got[i].Estimate != *want[i].Estimate {
			t.Errorf("got[%d].Estimate = %v, want %v", i, *got[i].Estimate, *want[i].Estimate)
		}
		if (got[i].CycleNumber == nil) != (want[i].CycleNumber == nil) {
			t.Errorf("got[%d].CycleNumber = %v, want %v", i, got[i].CycleNumber, want[i].CycleNumber)
		}
	}
	if ts.Before(before) || ts.After(after) {
		t.Errorf("LoadCache timestamp = %v, want between %v and %v", ts, before, after)
	}
}

func TestLoadCacheCorruptFileErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "linear-cache.json"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadCache(dir)
	if err == nil {
		t.Fatal("LoadCache: got nil error, want error for corrupt cache file")
	}
}

func TestLoadCacheMissingFileErrors(t *testing.T) {
	_, _, err := LoadCache(t.TempDir())
	if err == nil {
		t.Fatal("LoadCache: got nil error, want error for missing cache file")
	}
}
