package sub

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/config"
)

func cfgWithProfiles(profiles map[string]*config.ModelProfile) *config.Config {
	c := &config.Config{ModelProfiles: profiles}
	return c
}

func noEnv(string) string { return "" }

func TestResolveLaneOrder(t *testing.T) {
	cfg := cfgWithProfiles(map[string]*config.ModelProfile{
		"cfg-lane":  {BaseURL: "https://cfg", Haiku: "m"},
		"env-lane":  {BaseURL: "https://env", Haiku: "m"},
		"flag-lane": {BaseURL: "https://flag", Haiku: "m"},
	})
	cfg.Sub.Lane = "cfg-lane"

	// config only
	l, err := ResolveLane(cfg, "", "", noEnv)
	if err != nil || l.Name != "cfg-lane" {
		t.Fatalf("config-only: l=%+v err=%v, want cfg-lane", l, err)
	}

	// env beats config
	getenv := func(k string) string {
		if k == "GV_SUB_LANE" {
			return "env-lane"
		}
		return ""
	}
	l, err = ResolveLane(cfg, "", "", getenv)
	if err != nil || l.Name != "env-lane" {
		t.Fatalf("env-over-config: l=%+v err=%v, want env-lane", l, err)
	}

	// flag beats env and config
	l, err = ResolveLane(cfg, "flag-lane", "", getenv)
	if err != nil || l.Name != "flag-lane" {
		t.Fatalf("flag-over-all: l=%+v err=%v, want flag-lane", l, err)
	}
}

func TestResolveLaneNone(t *testing.T) {
	cfg := cfgWithProfiles(nil)
	_, err := ResolveLane(cfg, "", "", noEnv)
	if !errors.Is(err, ErrNoLane) {
		t.Fatalf("err = %v, want ErrNoLane", err)
	}
}

func TestResolveLaneUnknownListsKeys(t *testing.T) {
	cfg := cfgWithProfiles(map[string]*config.ModelProfile{
		"b-lane": {Haiku: "m"},
		"a-lane": {Haiku: "m"},
	})
	_, err := ResolveLane(cfg, "nope", "", noEnv)
	if !errors.Is(err, ErrUnknownLane) {
		t.Fatalf("err = %v, want ErrUnknownLane", err)
	}
	if !strings.Contains(err.Error(), "a-lane") || !strings.Contains(err.Error(), "b-lane") {
		t.Fatalf("err = %v, want it to list a-lane and b-lane sorted", err)
	}
	// sorted: a-lane appears before b-lane
	if strings.Index(err.Error(), "a-lane") > strings.Index(err.Error(), "b-lane") {
		t.Fatalf("err = %v, want a-lane before b-lane (sorted)", err)
	}
}

func TestResolveModelOrder(t *testing.T) {
	cfg := cfgWithProfiles(map[string]*config.ModelProfile{
		"full":      {Opus: "o", Sonnet: "s", Haiku: "h"},
		"no-haiku":  {Opus: "o", Sonnet: "s"},
		"opus-only": {Opus: "o"},
		"none":      {},
	})

	l, err := ResolveLane(cfg, "full", "", noEnv)
	if err != nil || l.Model != "h" {
		t.Fatalf("full: l=%+v err=%v, want haiku h", l, err)
	}
	l, err = ResolveLane(cfg, "no-haiku", "", noEnv)
	if err != nil || l.Model != "s" {
		t.Fatalf("no-haiku: l=%+v err=%v, want sonnet s", l, err)
	}
	l, err = ResolveLane(cfg, "opus-only", "", noEnv)
	if err != nil || l.Model != "o" {
		t.Fatalf("opus-only: l=%+v err=%v, want opus o", l, err)
	}
	_, err = ResolveLane(cfg, "none", "", noEnv)
	if !errors.Is(err, ErrNoModel) {
		t.Fatalf("none: err = %v, want ErrNoModel", err)
	}

	// flag model beats everything
	cfg.Sub.Model = "cfg-model"
	l, err = ResolveLane(cfg, "full", "flag-model", noEnv)
	if err != nil || l.Model != "flag-model" {
		t.Fatalf("flag beats config/lane: l=%+v err=%v, want flag-model", l, err)
	}
	l, err = ResolveLane(cfg, "full", "", noEnv)
	if err != nil || l.Model != "cfg-model" {
		t.Fatalf("cfg.Sub.Model beats lane slugs: l=%+v err=%v, want cfg-model", l, err)
	}
}

func TestResolveLanePaid(t *testing.T) {
	cfg := cfgWithProfiles(map[string]*config.ModelProfile{
		"openrouter-x": {Haiku: "m"},
		"flat-x":       {Haiku: "m"},
	})
	l, err := ResolveLane(cfg, "openrouter-x", "", noEnv)
	if err != nil || !l.Paid {
		t.Fatalf("openrouter-x: l=%+v err=%v, want Paid=true", l, err)
	}
	l, err = ResolveLane(cfg, "flat-x", "", noEnv)
	if err != nil || l.Paid {
		t.Fatalf("flat-x: l=%+v err=%v, want Paid=false", l, err)
	}
}

func TestResolveLaneAnthropic(t *testing.T) {
	cfg := cfgWithProfiles(nil)
	l, err := ResolveLane(cfg, "anthropic", "", noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if l.BaseURL != "https://api.anthropic.com" || l.TokenEnv != "ANTHROPIC_API_KEY" {
		t.Errorf("anthropic lane = %+v, want the anthropic.com endpoint + ANTHROPIC_API_KEY", l)
	}
	if l.Model != "claude-haiku-4-5" {
		t.Errorf("anthropic lane model = %q, want default claude-haiku-4-5", l.Model)
	}
	l, err = ResolveLane(cfg, "anthropic", "claude-opus-5", noEnv)
	if err != nil || l.Model != "claude-opus-5" {
		t.Fatalf("anthropic lane with flag model: l=%+v err=%v, want claude-opus-5", l, err)
	}
}

func TestLaneKeyMissing(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("export OTHER_KEY=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := Lane{Name: "x", TokenEnv: "MISSING_VAR"}
	_, err := l.Key(envPath)
	if !errors.Is(err, ErrNoKey) {
		t.Fatalf("err = %v, want ErrNoKey", err)
	}
	if !strings.Contains(err.Error(), "MISSING_VAR") || !strings.Contains(err.Error(), envPath) {
		t.Fatalf("err = %v, want it to name the var and the file", err)
	}

	if err := os.WriteFile(envPath, []byte("export MISSING_VAR=sekret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := l.Key(envPath)
	if err != nil || key != "sekret" {
		t.Fatalf("key=%q err=%v, want sekret", key, err)
	}
}
