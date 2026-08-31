package chatweb

// grove-218: the two safety gates, pure and table-tested, because this is
// the first thing grove has ever LISTENED on and it spawns Claude sessions
// and pastes into panes. Design §7.

import (
	"fmt"
	"net"
	"strings"
)

// Loopback reports whether bind addresses only this machine. Everything
// else — an empty string (which net.Listen reads as "every interface"), a
// LAN address, `0.0.0.0`, `::` — is reachable by somebody else and gets
// BindWarning's paragraph. The empty case is the one that matters: reading
// "" as loopback would silently publish the spawn endpoint to the LAN.
func Loopback(bind string) bool {
	bind = strings.TrimSpace(bind)
	if bind == "" {
		return false
	}
	if bind == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(bind, "[]"))
	if ip == nil {
		// A hostname we cannot judge is treated as exposed. Uncertainty
		// about who can reach a spawn endpoint resolves toward the warning.
		return false
	}
	return ip.IsLoopback()
}

// BindWarning is what the operator sees before a non-loopback listener
// starts: "" for a loopback bind, else a paragraph naming what somebody on
// that network can do. Not a refusal — the flag IS the consent (a default
// of 127.0.0.1 means every other bind was typed on purpose) — but it says
// the consequences out loud rather than logging a URL and hoping.
func BindWarning(bind string, port int) string {
	if Loopback(bind) {
		return ""
	}
	where := bind
	if where == "" {
		where = "every interface"
	}
	return strings.Join([]string{
		fmt.Sprintf("⚠ gv chat serve is binding %s:%d — NOT loopback.", where, port),
		"  Anyone who can reach that address can, with no password:",
		"    • read every orchestrator chat on this machine, in full",
		"    • type into your live chats (it pastes straight into the pane)",
		"    • answer permission prompts on your behalf",
		"    • spawn new Claude sessions in any registered workspace",
		"  There is no auth here by design. The sanctioned exposure is",
		"  `tailscale serve --bg " + fmt.Sprint(port) + "` in front of the DEFAULT 127.0.0.1 bind,",
		"  which is tailnet-only and identity-gated. `tailscale funnel` is never correct.",
	}, "\n")
}

// ContentSecurityPolicy is what makes it safe to render an agent's output
// as markdown. marked does not sanitize (the option was removed in v8), so
// the browser is the sanitizer instead: with `script-src 'self'` and no
// 'unsafe-inline', an `<img onerror=…>` or a `javascript:` href smuggled
// through a file the agent happened to read cannot run, and nothing on the
// page can reach another origin. `frame-ancestors 'none'` keeps a spawn
// button out of somebody else's frame.
//
// The one relaxation is style-src: the page's CSS is an inline <style>
// block. Styles cannot execute, so that costs nothing the policy is for.
const ContentSecurityPolicy = "default-src 'none'; " +
	"script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; connect-src 'self'; font-src 'self'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// ContentTypeJSON is the content type every mutating request must carry.
//
// This is not authentication (there is none — `tailscale serve` is the
// whole auth story). It is the one line that stops a page the operator
// happens to have open in another tab from driving this server: a
// cross-origin `<form>` or `fetch` with no preflight can only send
// text/plain, form-urlencoded or multipart, so requiring application/json
// forces a CORS preflight — and this server answers no preflight and sends
// no Access-Control-Allow-Origin. Cheap, configuration-free, and it costs a
// legitimate client one header.
const ContentTypeJSON = "application/json"

// GuardWrite is that gate. contentType is the raw header; parameters
// (`; charset=utf-8`) are allowed, a different type is not.
func GuardWrite(contentType string) error {
	base, _, _ := strings.Cut(contentType, ";")
	if strings.EqualFold(strings.TrimSpace(base), ContentTypeJSON) {
		return nil
	}
	return fmt.Errorf("this endpoint requires Content-Type: %s (a cross-origin form must not be able to steer an agent)", ContentTypeJSON)
}
