package create

import (
	"testing"
)

// #220: --json's provenance used to say config.toml for the branch prefix,
// the worktree toggle and the placement whenever config.Load had filled
// them in from its own defaults -- including with no config.toml at all.
// Seen live in #209's Route A0 pass, as "branch_prefix": "config.toml"
// beside a username prefix no file named.
func TestProvenance_LoadsDefaultsAreBuiltIn(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string // "" writes no config.toml
		args   []string
		want   map[string]string
	}{
		{
			name: "no config.toml",
			args: []string{"--title", "fix login"},
			want: map[string]string{"branch_prefix": "built-in", "worktree": "built-in", "placement": "worktree"},
		},
		{
			name:   "a config.toml that sets only the worktree toggle",
			config: "default_worktree = false\n",
			args:   []string{"--title", "fix login"},
			want:   map[string]string{"branch_prefix": "built-in", "worktree": "config.toml", "placement": "built-in"},
		},
		{
			name:   "a config.toml that sets all three",
			config: "branch_prefix = \"me/\"\ndefault_worktree = false\ndefault_placement = \"new-space\"\n",
			args:   []string{"--title", "fix login"},
			want:   map[string]string{"branch_prefix": "config.toml", "worktree": "config.toml", "placement": "config.toml"},
		},
		{
			name: "no config.toml, and no worktree",
			args: []string{"--title", "fix login", "--no-worktree"},
			want: map[string]string{"placement": "built-in"},
		},
		{
			name:   "a config.toml key in another case",
			config: "DEFAULT_WORKTREE = false\n",
			args:   []string{"--title", "fix login"},
			want:   map[string]string{"worktree": "config.toml"},
		},
		{
			name:   "an empty branch_prefix is config.toml's: it asks for no prefix",
			config: "branch_prefix = \"\"\n",
			args:   []string{"--title", "fix login", "--worktree"},
			want:   map[string]string{"branch_prefix": "config.toml"},
		},
		{
			name:   "a branch_prefix config.toml sets and Load refuses",
			config: "branch_prefix = \"-x\"\n",
			args:   []string{"--title", "fix login"},
			want:   map[string]string{"branch_prefix": "built-in"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			if tc.config != "" {
				writeConfig(t, h.env.ConfigDir, tc.config)
			}
			args := append(append([]string{}, tc.args...), "--dry-run", "--json")
			if code := h.run(args...); code != ExitOK {
				t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, h.stderr)
			}
			out := decodeReport(t, h.stdout.String())
			for field, want := range tc.want {
				if got := out.Provenance[field]; got != want {
					t.Errorf("provenance[%s] = %q, want %q", field, got, want)
				}
			}
		})
	}
}
