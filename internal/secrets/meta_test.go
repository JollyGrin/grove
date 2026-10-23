package secrets

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPreviewFor(t *testing.T) {
	hi := "tk_demo_8723abCDefGH0123456789xyzQWERTY6AE2" // 42 chars, high entropy
	hi40 := hi[:36] + "6AE2"
	cases := []struct {
		name    string
		val     string
		reveal  int
		want    string
		errPart string
	}{
		{"reveal12", hi40, 12, hi40[:12] + "***" + hi40[36:], ""},
		{"reveal17", hi40, 17, "", "cap"},
		{"short23", hi40[:23], 4, "", "shorter"},
		{"short23-reveal1", hi40[:23], 1, "", "shorter"},
		{"low entropy", strings.Repeat("a", 40), 8, "", "distinct"},
		{"too few hidden", hi40[:24], 8, "", "hidden"},
		{"reveal0", hi40, 0, "", ""},
	}
	for _, c := range cases {
		got, err := PreviewFor([]byte(c.val), c.reveal)
		if c.errPart == "" && err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
		}
		if c.errPart != "" && (err == nil || !strings.Contains(err.Error(), c.errPart)) {
			t.Errorf("%s: err %v, want mention of %q", c.name, err, c.errPart)
		}
		if got != c.want {
			t.Errorf("%s: preview %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSetWithMetaSidecar(t *testing.T) {
	scratch(t)
	if _, _, err := InitIdentity(); err != nil {
		t.Fatal(err)
	}
	val := "tk_demo_8723abCDefGH0123456789xyzQWER6AE2"[:40]
	perr, err := SetWithMeta(Global, "STRIPE", []byte(val+"\n"), "Stripe restricted key", 12)
	if err != nil || perr != nil {
		t.Fatalf("err=%v perr=%v", err, perr)
	}
	raw, err := os.ReadFile(metaPath(Global, "STRIPE"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), val) {
		t.Errorf("sidecar holds the full value:\n%s", raw)
	}
	rows, _ := Rows("")
	if len(rows) != 1 || rows[0].Type != "Stripe restricted key" || rows[0].Preview != val[:12]+"***"+val[36:] {
		t.Errorf("rows: %+v", rows)
	}
	// unknown fields ignored
	os.WriteFile(metaPath(Global, "STRIPE"), []byte("type: T\nfuture: 1\n"), 0o600)
	if m, err := ReadMeta(Global, "STRIPE"); err != nil || m.Type != "T" {
		t.Errorf("round-trip: %+v %v", m, err)
	}
	var m Meta
	if err := yaml.Unmarshal([]byte("type: x\nzzz: 1\n"), &m); err != nil {
		t.Error(err)
	}
	// refused reveal still stores, no preview
	perr, err = SetWithMeta(Global, "LOW", []byte(strings.Repeat("a", 40)), "", 12)
	if err != nil || perr == nil {
		t.Fatalf("err=%v perr=%v", err, perr)
	}
	if err := Remove(Global, "STRIPE"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(metaPath(Global, "STRIPE")); !os.IsNotExist(err) {
		t.Errorf("sidecar survived rm: %v", err)
	}
	rows, _ = Rows("")
	if len(rows) != 1 || rows[0].Name != "LOW" || rows[0].Preview != "" {
		t.Errorf("rows after: %+v", rows)
	}
}
