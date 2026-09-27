package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/JollyGrin/grove/internal/feature"
)

// TestPrintLandJSONDryRunOmitsLandedAndFailed pins the `--json` dry-run
// shape (Decision 6): `landed`/`failed` must be genuinely ABSENT — not
// present as `[]` — until a run actually executed, so a plugin can tell
// "never ran" from "ran, nothing happened".
func TestPrintLandJSONDryRunOmitsLandedAndFailed(t *testing.T) {
	plan := feature.LandPlan{
		Land:    []feature.LandRow{{Ticket: "grove-1", Number: 1, PR: 101}},
		Skipped: []feature.SkipRow{{Ticket: "grove-2", Number: 2, Reason: feature.SkipPROpen}},
	}
	out := captureStdout(t, func() {
		if err := printLandJSON("trains", plan, nil); err != nil {
			t.Fatal(err)
		}
	})
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("printLandJSON wrote invalid JSON: %v\n%s", err, out)
	}
	if _, ok := got["landed"]; ok {
		t.Errorf("dry-run JSON carries `landed`, want it absent: %s", out)
	}
	if _, ok := got["failed"]; ok {
		t.Errorf("dry-run JSON carries `failed`, want it absent: %s", out)
	}
	if got["feature"] != "trains" {
		t.Errorf("feature = %v, want trains", got["feature"])
	}
	if _, ok := got["schema_version"]; !ok {
		t.Errorf("missing schema_version: %s", out)
	}
}

// TestPrintLandJSONExecutedIncludesEmptyLanded pins the other half: once a
// run executed (res != nil), `landed`/`failed` are present even when
// nothing landed or failed — `[]`, never omitted, never null.
func TestPrintLandJSONExecutedIncludesEmptyLanded(t *testing.T) {
	res := &feature.LandResult{Landed: []int{}, Failed: []feature.LandFailure{}}
	out := captureStdout(t, func() {
		if err := printLandJSON("trains", feature.LandPlan{}, res); err != nil {
			t.Fatal(err)
		}
	})
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("printLandJSON wrote invalid JSON: %v\n%s", err, out)
	}
	landed, ok := got["landed"].([]any)
	if !ok {
		t.Fatalf("landed missing or wrong type: %s", out)
	}
	if len(landed) != 0 {
		t.Errorf("landed = %v, want empty", landed)
	}
	if _, ok := got["failed"]; !ok {
		t.Errorf("executed JSON missing `failed`: %s", out)
	}
}

func TestPrintLandTableNoCarsMessage(t *testing.T) {
	out := captureStdout(t, func() { printLandTable("trains", feature.LandPlan{}) })
	want := "no cars on feature trains\n"
	if out != want {
		t.Errorf("printLandTable(empty) = %q, want %q", out, want)
	}
}

func TestPrintLandTableRows(t *testing.T) {
	plan := feature.LandPlan{
		Land:    []feature.LandRow{{Ticket: "grove-1", Number: 1, PR: 101}},
		Skipped: []feature.SkipRow{{Ticket: "grove-2", Number: 2, Reason: feature.SkipPROpen}},
	}
	out := captureStdout(t, func() { printLandTable("trains", plan) })
	if !bytes.Contains([]byte(out), []byte("grove-1")) || !bytes.Contains([]byte(out), []byte("land")) {
		t.Errorf("table missing the land row: %s", out)
	}
	if !bytes.Contains([]byte(out), []byte("grove-2")) || !bytes.Contains([]byte(out), []byte(feature.SkipPROpen)) {
		t.Errorf("table missing the skipped row and its reason: %s", out)
	}
}
