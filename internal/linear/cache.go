package linear

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// cacheFileName is the on-disk cache file under the plugin state dir
// (spec §12: "$HERDR_PLUGIN_STATE_DIR/... linear-cache.json").
const cacheFileName = "linear-cache.json"

// SaveCache writes issues to stateDir as the last-known Linear
// assignedIssues result, so the form can render instantly at open before an
// async refresh completes (spec §10).
func SaveCache(stateDir string, issues []Issue) error {
	b, err := json.Marshal(issues)
	if err != nil {
		return fmt.Errorf("save linear cache: encode issues: %w", err)
	}
	path := filepath.Join(stateDir, cacheFileName)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("save linear cache: write %s: %w", path, err)
	}
	return nil
}

// LoadCache reads back the cache SaveCache wrote, returning the issues plus
// the cache file's mtime as the "as of" timestamp callers use to judge
// staleness (spec §10 TTL). It returns an error whenever the file is
// missing or its contents don't parse as []Issue; per spec §12, discarding
// that error and falling back to an empty/no-cache state is the caller's
// (app-layer) responsibility, not this package's.
func LoadCache(stateDir string) ([]Issue, time.Time, error) {
	path := filepath.Join(stateDir, cacheFileName)

	info, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("load linear cache: stat %s: %w", path, err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("load linear cache: read %s: %w", path, err)
	}

	var issues []Issue
	if err := json.Unmarshal(b, &issues); err != nil {
		return nil, time.Time{}, fmt.Errorf("load linear cache: parse %s: %w", path, err)
	}

	return issues, info.ModTime(), nil
}
