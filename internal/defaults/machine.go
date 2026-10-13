package defaults

import (
	"path/filepath"
	"strings"

	"github.com/ZviBaratz/herdr-draft/internal/config"
)

// FieldMachine is the machine's provenance key (`create --json`'s
// "provenance.machine"). It is not a Resolved.From key: the machine
// resolves through Machine, never through Resolve.
const FieldMachine = "machine"

// Machine resolves which herdr machine a project opens on (machine spec
// §5): the most specific [machine.paths] rule whose directory is the
// project key or one of its ancestors, else [machine] default, else this
// machine. It returns the selector, the tier that supplied it, and the
// matched rule's directory ("" when no rule matched).
//
// A function of its own, shaped like AgentOptions, so the chain's stop at
// config.toml is a property of the signature rather than of a test: it is
// handed the config and the key and nothing else, so no memory file and no
// .herdr-draft.toml can reach it. That is decision 2 -- a one-off `local`
// for a browser session must never become the next session's default --
// and v2 spec §11's trust boundary, which keeps a cloned repository from
// choosing which host runs your code.
//
// key is v2 spec §10's project key, canonical (pathx.CanonicalKey), and
// the rules were canonicalised the same way when config.toml loaded, so the
// comparison is lexical: whole path components, so ~/quantivly-old is not
// under ~/quantivly. Pure, like everything in this package.
func Machine(cfg config.Config, key string) (selector string, from Tier, rule string) {
	best := -1
	for i, r := range cfg.Machine.Rules {
		if key == "" || !within(key, r.Dir) {
			continue
		}
		if best < 0 || len(r.Dir) > len(cfg.Machine.Rules[best].Dir) {
			best = i
		}
	}
	if best >= 0 {
		r := cfg.Machine.Rules[best]
		return r.Selector, TierUserConfig, r.Dir
	}
	if d := strings.TrimSpace(cfg.Machine.Default); d != "" {
		return d, TierUserConfig, ""
	}
	return config.LocalMachine, TierBuiltin, ""
}

// within reports whether path is dir or lies under it, by whole components.
func within(path, dir string) bool {
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}
