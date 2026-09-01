// Package linear reads the caller's assigned Linear issues over Linear's
// GraphQL API for the herdr-draft form's issue picker (spec §10). It is
// deliberately read-only: no comments, no status writes -- the GitHub
// integration already owns issue status transitions, and manual writes
// from here would fight it.
package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// assignedIssuesQuery is the Linear GraphQL query for the personal
// "assigned issues" list the form uses to seed a new session, copied
// verbatim from spec §10 (docs/specs/2026-08-31-herdr-draft-design.md,
// "Linear integration (read-only)" -> Query block) so the wire query this
// client sends can be diffed against the design doc directly.
const assignedIssuesQuery = `  { viewer { assignedIssues(
      filter: { state: { type: { in: ["unstarted", "started"] } } },
      orderBy: updatedAt, first: 50
    ) { nodes {
        identifier title branchName url description
        state { name type } estimate priority cycle { number }
  } } } }`

// defaultEndpoint is Linear's public GraphQL API endpoint, used when
// Client.Endpoint is empty.
const defaultEndpoint = "https://api.linear.app/graphql"

// Issue is one Linear issue returned by AssignedIssues, shaped for the form
// fields described in spec §10 (issue picker + seeding template). Estimate
// and CycleNumber are pointers because Linear reports both as JSON null
// when unset (verified against a "started" issue carrying no cycle) --
// nil means "not reported", distinct from a real zero value.
type Issue struct {
	Identifier  string
	Title       string
	BranchName  string
	URL         string
	Description string
	StateName   string
	StateType   string
	Estimate    *float64
	Priority    int
	CycleNumber *int
}

// Client talks to the Linear GraphQL API on behalf of the herdr-draft form.
// It performs only the read-only assignedIssues query; see the package doc
// for why writes are out of scope.
type Client struct {
	HTTP     *http.Client
	APIKey   string
	Endpoint string
}

// httpClient returns c.HTTP, defaulting to http.DefaultClient when unset.
func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// endpoint returns c.Endpoint, defaulting to defaultEndpoint when empty.
func (c *Client) endpoint() string {
	if c.Endpoint != "" {
		return c.Endpoint
	}
	return defaultEndpoint
}

// graphQLRequest is the wire body of a GraphQL POST request.
type graphQLRequest struct {
	Query string `json:"query"`
}

// graphQLError is one entry in a GraphQL response's top-level "errors"
// array -- a server-side rejection of the query (bad auth, malformed
// query), distinct from an HTTP transport failure.
type graphQLError struct {
	Message string `json:"message"`
}

// assignedIssuesResponse mirrors Linear's response shape for
// assignedIssuesQuery: {"data":{"viewer":{"assignedIssues":{"nodes":[...]}}}}.
// Data is a pointer because a request that only fails at the GraphQL level
// (Errors non-empty) can carry a null/absent "data" key.
type assignedIssuesResponse struct {
	Data *struct {
		Viewer struct {
			AssignedIssues struct {
				Nodes []issueNode `json:"nodes"`
			} `json:"assignedIssues"`
		} `json:"viewer"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

// issueNode is one element of assignedIssuesResponse's nodes[], matching
// the field selection in assignedIssuesQuery exactly. Estimate and Cycle
// are pointers to preserve Linear's JSON null for "not set".
type issueNode struct {
	Identifier  string `json:"identifier"`
	Title       string `json:"title"`
	BranchName  string `json:"branchName"`
	URL         string `json:"url"`
	Description string `json:"description"`
	State       struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"state"`
	Estimate *float64 `json:"estimate"`
	Priority int      `json:"priority"`
	Cycle    *struct {
		Number int `json:"number"`
	} `json:"cycle"`
}

// AssignedIssues runs assignedIssuesQuery against c.Endpoint and returns the
// caller's currently assigned, not-yet-done issues (spec §10: state type in
// ["unstarted", "started"], ordered by updatedAt, capped at 50). It never
// mutates Linear state.
func (c *Client) AssignedIssues(ctx context.Context) ([]Issue, error) {
	body, err := json.Marshal(graphQLRequest{Query: assignedIssuesQuery})
	if err != nil {
		return nil, fmt.Errorf("linear assigned issues: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("linear assigned issues: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Linear personal API keys go in Authorization with no "Bearer " prefix.
	req.Header.Set("Authorization", c.APIKey)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("linear assigned issues: request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("linear assigned issues: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("linear assigned issues: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var parsed assignedIssuesResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("linear assigned issues: parse response: %w", err)
	}
	if len(parsed.Errors) > 0 {
		msgs := make([]string, len(parsed.Errors))
		for i, e := range parsed.Errors {
			msgs[i] = e.Message
		}
		return nil, fmt.Errorf("linear assigned issues: %s", strings.Join(msgs, "; "))
	}
	if parsed.Data == nil {
		return nil, fmt.Errorf("linear assigned issues: response had no data and no errors")
	}

	nodes := parsed.Data.Viewer.AssignedIssues.Nodes
	issues := make([]Issue, len(nodes))
	for i, n := range nodes {
		issue := Issue{
			Identifier:  n.Identifier,
			Title:       n.Title,
			BranchName:  n.BranchName,
			URL:         n.URL,
			Description: n.Description,
			StateName:   n.State.Name,
			StateType:   n.State.Type,
			Estimate:    n.Estimate,
			Priority:    n.Priority,
		}
		if n.Cycle != nil {
			num := n.Cycle.Number
			issue.CycleNumber = &num
		}
		issues[i] = issue
	}
	return issues, nil
}

// configFileName is the plugin config file whose permissions ResolveAPIKey
// checks before trusting an inline api_key literal (spec §12: "discouraged;
// file perms checked (0600)").
const configFileName = "config.toml"

// ResolveAPIKey resolves the Linear personal API key from three possible
// sources, in priority order:
//
//  1. apiKeyCmd (e.g. config's [linear] api_key_cmd, such as
//     ["pass", "show", "linear-api-key"]): if non-empty, it is executed and
//     its trimmed stdout is used. A command that is configured but fails to
//     run is a hard error -- it is not silently treated as absent, since a
//     broken api_key_cmd is a real misconfiguration the user should see.
//     A command that runs successfully but prints nothing is treated the
//     same as "not configured" and resolution falls through to the next
//     source.
//  2. The $LINEAR_API_KEY environment variable.
//  3. apiKeyLiteral (config's [linear] api_key inline value): only trusted
//     when <configDir>/config.toml has no group/other permission bits set
//     (i.e. is no wider than 0600). A wider file is rejected with an error
//     rather than silently falling through, since an over-permissioned
//     config file is the exact hazard this check exists to catch.
//
// When all three sources are absent, ResolveAPIKey returns ("", nil) --
// per spec §10, an absent key means the Linear field is simply not
// rendered, not an error.
func ResolveAPIKey(apiKeyCmd []string, apiKeyLiteral, configDir string) (string, error) {
	if len(apiKeyCmd) > 0 {
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(apiKeyCmd[0], apiKeyCmd[1:]...) //nolint:gosec // user-configured command, run intentionally
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("resolve linear api key: run api_key_cmd: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		if key := strings.TrimSpace(stdout.String()); key != "" {
			return key, nil
		}
	}

	if key := strings.TrimSpace(os.Getenv("LINEAR_API_KEY")); key != "" {
		return key, nil
	}

	if apiKeyLiteral == "" {
		return "", nil
	}

	if err := checkConfigPerm(configDir); err != nil {
		return "", err
	}
	return apiKeyLiteral, nil
}

// checkConfigPerm rejects an inline api_key literal when
// <configDir>/config.toml is readable or writable by anyone other than its
// owner. A missing config file has nothing to check and is allowed through
// (the literal must have come from somewhere other than a file this
// process can see, e.g. a test harness).
func checkConfigPerm(configDir string) error {
	path := filepath.Join(configDir, configFileName)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("resolve linear api key: stat %s: %w", path, err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("resolve linear api key: %s has permissions %04o, wider than 0600; refusing inline api_key", path, perm)
	}
	return nil
}
