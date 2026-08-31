package chatweb_test

import (
	"strings"
	"testing"

	"github.com/JollyGrin/grove/internal/chatweb"
)

func TestLoopback(t *testing.T) {
	cases := []struct {
		bind string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.53", true},
		{"::1", true},
		{"[::1]", true},
		{"localhost", true},
		{" 127.0.0.1 ", true},
		// The one that matters: net.Listen reads "" as every interface, so
		// reading it as loopback would publish the spawn endpoint silently.
		{"", false},
		{"0.0.0.0", false},
		{"::", false},
		{"192.168.1.40", false},
		{"100.64.0.1", false}, // a tailnet address is still not loopback
		// A name we cannot resolve to an IP resolves toward the warning.
		{"groveremote.tail504e3.ts.net", false},
	}
	for _, c := range cases {
		if got := chatweb.Loopback(c.bind); got != c.want {
			t.Errorf("Loopback(%q) = %v, want %v", c.bind, got, c.want)
		}
	}
}

func TestBindWarning(t *testing.T) {
	if w := chatweb.BindWarning("127.0.0.1", 3000); w != "" {
		t.Errorf("a loopback bind must warn about nothing, got %q", w)
	}
	w := chatweb.BindWarning("0.0.0.0", 3000)
	if w == "" {
		t.Fatal("a non-loopback bind must warn")
	}
	// The warning has to NAME the consequences, not just say "careful":
	// this endpoint spawns Claude sessions and types into live panes.
	for _, want := range []string{"0.0.0.0:3000", "spawn", "tailscale serve --bg 3000", "funnel"} {
		if !strings.Contains(w, want) {
			t.Errorf("BindWarning must mention %q:\n%s", want, w)
		}
	}
	if !strings.Contains(chatweb.BindWarning("", 8080), "every interface") {
		t.Error(`an empty bind must be described as "every interface", not as ""`)
	}
}

func TestGuardWrite(t *testing.T) {
	for _, ok := range []string{"application/json", "application/json; charset=utf-8", "Application/JSON"} {
		if err := chatweb.GuardWrite(ok); err != nil {
			t.Errorf("GuardWrite(%q) = %v, want nil", ok, err)
		}
	}
	// Exactly the three types a cross-origin request can send with no
	// preflight. Each must be refused — that refusal IS the CSRF defense.
	for _, bad := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x"} {
		if err := chatweb.GuardWrite(bad); err == nil {
			t.Errorf("GuardWrite(%q) = nil, want a refusal (a cross-origin form must not steer an agent)", bad)
		}
	}
}

func TestContentSecurityPolicy(t *testing.T) {
	csp := chatweb.ContentSecurityPolicy
	// The policy is the sanitizer for marked's output; 'unsafe-inline' in
	// script-src would silently remove that protection.
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") || strings.Contains(csp, "script-src 'unsafe-inline'") {
		t.Fatalf("script-src must not allow inline script — it is what makes rendering agent markdown safe:\n%s", csp)
	}
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "frame-ancestors 'none'", "connect-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP is missing %q:\n%s", want, csp)
		}
	}
}
