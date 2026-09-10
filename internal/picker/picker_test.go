package picker

import (
	"errors"
	"reflect"
	"testing"
)

// realDryRun is the verbatim stdout of a real picker's
// `--dir <a repo> --json --dry-run` on 2026-09-10, kept whole rather than
// trimmed to the keys this package models. Three of its keys (state,
// exit_code, notes) are NOT in the documented protocol, and that is the
// point: a picker is free to say more than the contract asks, and a decoder
// that rejected the extra would refuse the one implementation that exists.
const realDryRun = `{
  "tenant": "alpha",
  "tenant_state": "default",
  "profile": "alpha-1",
  "tier": "Max",
  "config_dir": null,
  "score": 8066,
  "class": "eligible",
  "state": "picked",
  "reason": null,
  "exit_code": 0,
  "overflow": false,
  "usage": { "five_hour": 3, "weekly": 11, "cache_age_s": 18 },
  "resets_at": { "five_hour": "2026-09-10T22:49:59Z", "weekly": "2026-09-13T01:59:59Z" },
  "machine": { "load1": 61.17, "ncpu": 8, "swap_used_pct": 68 },
  "warnings": ["machine: load 61.17 on 8 threads (7.6x)"],
  "notes": [],
  "skipped": [ { "profile": "alpha-0", "why": null, "five_hour": 0, "weekly": 100 } ]
}`

func f64(v float64) *float64 { return &v }

func TestDecodeAPickedResult(t *testing.T) {
	got, err := Decode([]byte(realDryRun))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := Result{
		Tenant:      "alpha",
		TenantState: "default",
		Profile:     "alpha-1",
		Tier:        "Max",
		Class:       "eligible",
		Usage:       Usage{FiveHour: f64(3), Weekly: f64(11), CacheAgeS: f64(18)},
		ResetsAt:    Resets{FiveHour: "2026-09-10T22:49:59Z", Weekly: "2026-09-13T01:59:59Z"},
		Machine:     Machine{Load1: f64(61.17), NCPU: f64(8), SwapUsedPct: f64(68)},
		Warnings:    []string{"machine: load 61.17 on 8 threads (7.6x)"},
		Skipped:     []Skipped{{Profile: "alpha-0"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Decode:\n got %+v\nwant %+v", got, want)
	}
}

// A null string field must decode to "" rather than failing: the picker
// reports every field on every exit path and nulls whatever it could not
// determine.
func TestDecodeTreatsNullsAsAbsent(t *testing.T) {
	got, err := Decode([]byte(`{"tenant":null,"profile":null,"config_dir":null,
		"reason":"the tenant table is configured but resolved nothing",
		"usage":{"five_hour":null,"weekly":null,"cache_age_s":null},
		"resets_at":{"five_hour":null,"weekly":null},"skipped":[],"warnings":[]}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Profile != "" || got.Tenant != "" || got.ConfigDir != "" {
		t.Fatalf("null strings should decode empty, got %+v", got)
	}
	if got.Usage.FiveHour != nil {
		t.Fatalf("null five_hour should decode nil, got %v", *got.Usage.FiveHour)
	}
	if got.Reason != "the tenant table is configured but resolved nothing" {
		t.Fatalf("Reason = %q", got.Reason)
	}
}

func TestDecodeRejectsNonObjectOutput(t *testing.T) {
	if _, err := Decode([]byte("alpha-1\n")); err == nil {
		t.Fatal("Decode of a bare profile name (the picker's non---json output) should fail")
	}
}

func TestMissingKeysNamesEveryDocumentedKeyThatIsAbsent(t *testing.T) {
	got := MissingKeys([]byte(`{"profile":"alpha-1","usage":{"five_hour":1}}`))
	want := []string{"config_dir", "reason", "usage.cache_age_s", "usage.weekly"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MissingKeys = %v, want %v", got, want)
	}
}

func TestMissingKeysIsEmptyForTheRealPicker(t *testing.T) {
	if got := MissingKeys([]byte(realDryRun)); len(got) != 0 {
		t.Fatalf("the real picker is missing documented keys: %v", got)
	}
}

func TestCodeMeaningNamesEveryDocumentedCode(t *testing.T) {
	for code, want := range map[int]string{
		ExitPicked:       "picked",
		ExitExhausted:    "pool exhausted",
		ExitBackpressure: "machine under load",
		ExitBadTable:     "no account table to pick from",
		ExitNoProfiles:   "no profiles registered",
		ExitUsage:        "picker rejected its own arguments",
	} {
		if got := CodeMeaning(code); got != want {
			t.Fatalf("CodeMeaning(%d) = %q, want %q", code, got, want)
		}
	}
	if got := CodeMeaning(17); got != "picker exited 17" {
		t.Fatalf("CodeMeaning(17) = %q", got)
	}
}

// A refusal must carry the picker's OWN reason when it gave one, and fall
// back to the code's meaning when it did not -- never to a bare exit status,
// which is the shape of a message a user cannot act on.
func TestRefusalErrorPrefersThePickersOwnReason(t *testing.T) {
	withReason := &RefusalError{Code: ExitExhausted, Reason: "pool exhausted (resets 22:49)"}
	if got, want := withReason.Short(), "pool exhausted (resets 22:49)"; got != want {
		t.Fatalf("Short() = %q, want %q", got, want)
	}
	bare := &RefusalError{Code: ExitNoProfiles}
	if got, want := bare.Short(), "no profiles registered"; got != want {
		t.Fatalf("Short() = %q, want %q", got, want)
	}
	var target *RefusalError
	if !errors.As(error(bare), &target) {
		t.Fatal("RefusalError must be reachable through errors.As")
	}
}

// A multi-line reason -- the real picker's bad-table reason is a full
// sentence with an embedded git error -- must collapse to one line. Every
// surface this reaches has exactly one.
func TestRefusalShortIsOneLine(t *testing.T) {
	e := &RefusalError{Code: ExitBadTable, Reason: "the tenant table\nresolved\n  nothing"}
	if got, want := e.Short(), "the tenant table resolved nothing"; got != want {
		t.Fatalf("Short() = %q, want %q", got, want)
	}
}
