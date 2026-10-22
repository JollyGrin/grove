package secrets

import (
	"reflect"
	"strings"
	"testing"
)

func TestExecEnv(t *testing.T) {
	scratch(t)
	if _, _, err := InitIdentity(); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"GVX_A", "GVX_B", "GVX_C"} {
		t.Setenv(n, "") // empty is "unset" to Lookup; restored on cleanup
		if err := Set(Global, n, []byte("v-"+n)); err != nil {
			t.Fatal(err)
		}
	}
	base := []string{"PATH=/bin", "GVX_A=old", "HOME=/h"}

	got, err := ExecEnv("", []string{"GVX_A", "GVX_B", "GVX_A"}, base)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"PATH=/bin", "HOME=/h", "GVX_A=v-GVX_A", "GVX_B=v-GVX_B"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("env = %v want %v", got, want)
	}
	if base[1] != "GVX_A=old" {
		t.Errorf("base mutated: %v", base)
	}
	for _, kv := range got {
		if strings.HasPrefix(kv, "GVX_C=") {
			t.Errorf("unnamed secret leaked: %s", kv)
		}
	}

	_, err = ExecEnv("canopy", []string{"GVX_A", "GVX_NOPE"}, base)
	if err == nil || !strings.Contains(err.Error(), "GVX_NOPE") || !strings.Contains(err.Error(), `workspace "canopy"`) || !strings.Contains(err.Error(), "global") {
		t.Errorf("missing-name err = %v", err)
	}
}

func TestExecEnvInvalidNameBeforeDecrypt(t *testing.T) {
	scratch(t) // no identity: any decrypt attempt would fail differently
	_, err := ExecEnv("", []string{"GVX_OK", "bad name"}, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid secret name") {
		t.Errorf("err = %v", err)
	}
}
