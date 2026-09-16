package skill

import (
	"strings"
	"testing"
)

func TestRenderSubstitutesBothPlaceholders(t *testing.T) {
	out := Render("/opt/plugins/draft/bin/herdr-draft", "1.2.3")

	if !strings.Contains(out, "/opt/plugins/draft/bin/herdr-draft") {
		t.Error("rendered skill does not name the binary path it was given")
	}
	if !strings.Contains(out, "1.2.3") {
		t.Error("rendered skill does not carry the version it was given")
	}
	if strings.Contains(out, "{{") {
		t.Errorf("rendered skill still contains an unsubstituted placeholder:\n%s", out)
	}
}
