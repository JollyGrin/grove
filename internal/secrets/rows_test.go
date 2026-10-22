package secrets

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRowsShadowAndNoValues(t *testing.T) {
	scratch(t)
	if _, _, err := InitIdentity(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ ns, name string }{
		{Global, "A_KEY"}, {Global, "B_KEY"}, {"canopy", "A_KEY"},
	} {
		if err := Set(c.ns, c.name, []byte("ZZZSENTINEL")); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := Rows("canopy")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Row{}
	for _, r := range rows {
		got[r.Namespace+"/"+r.Name] = r
	}
	if !got["global/A_KEY"].Shadowed || got["global/B_KEY"].Shadowed || got["canopy/A_KEY"].Shadowed {
		t.Errorf("shadowed marks wrong: %+v", rows)
	}
	if got["canopy/A_KEY"].Recipients != 1 {
		t.Errorf("recipients = %d, want 1", got["canopy/A_KEY"].Recipients)
	}
	if b, _ := json.Marshal(rows); strings.Contains(string(b), "ZZZSENTINEL") {
		t.Error("a value leaked into the rows")
	}
	if none, _ := Rows(""); none[0].Shadowed || none[1].Shadowed || none[2].Shadowed {
		t.Error("no ambient workspace → nothing is shadowed")
	}
	held, _ := Held()
	if !held["A_KEY"] || !held["B_KEY"] || held["C_KEY"] {
		t.Errorf("Held = %v", held)
	}
}
