package secrets

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pushFixture: host A holds two secrets (one with a sidecar) and returns
// B's identity state dir + recipient and A's state dir.
func pushFixture(t *testing.T) (aState, bState, bRecipient string) {
	t.Helper()
	scratch(t)
	aState, _ = host(t)
	if err := Set(Global, "ONE", []byte("hunter2")); err != nil {
		t.Fatal(err)
	}
	if _, err := SetWithMeta(Global, "TWO", []byte("second"), "Test key", 0); err != nil {
		t.Fatal(err)
	}
	bState, bRecipient = host(t)
	use(t, aState)
	return
}

func TestPayloadRoundTrip(t *testing.T) {
	pushFixture(t)
	p, err := BuildPayload(Global)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("hunter2")) {
		t.Fatal("payload carries plaintext")
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"namespace", "recipients", "secrets", "meta"} {
		if _, ok := generic[k]; !ok {
			t.Fatalf("payload lacks %q: %s", k, raw)
		}
	}
	got, err := DecodePayload(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got.Namespace != Global || len(got.Secrets) != 2 || got.Meta["TWO"] == "" || got.Meta["ONE"] != "" {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func payloadWith(t *testing.T, mutate func(*Payload)) []byte {
	t.Helper()
	p, err := BuildPayload(Global)
	if err != nil {
		t.Fatal(err)
	}
	mutate(p)
	raw, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestReceiveInvalidWritesNothing(t *testing.T) {
	pushFixture(t)
	for name, mutate := range map[string]func(*Payload){
		"bad name":       func(p *Payload) { p.Secrets["../evil"] = p.Secrets["ONE"] },
		"bad namespace":  func(p *Payload) { p.Namespace = "../x" },
		"not ciphertext": func(p *Payload) { p.Secrets["PLAIN"] = "hunter2" },
		"orphan sidecar": func(p *Payload) { p.Meta["GHOST"] = "type: x\n" },
	} {
		t.Run(name, func(t *testing.T) {
			raw := payloadWith(t, mutate)
			home := t.TempDir() // a fresh receiving host
			t.Setenv("HOME", home)
			if _, err := Receive(bytes.NewReader(raw)); err == nil {
				t.Fatal("want an error")
			}
			if _, err := os.Stat(filepath.Join(home, ".config")); err == nil {
				t.Fatalf("config dir created: receive wrote something")
			}
		})
	}
}

func TestReceiveWritesAndIsIdempotent(t *testing.T) {
	_, bState, bR := pushFixture(t)
	if _, err := AddRecipient(Global, "host: b", bR); err != nil {
		t.Fatal(err)
	}
	if _, err := Reseal(Global); err != nil {
		t.Fatal(err)
	}
	raw := payloadWith(t, func(*Payload) {})

	home := t.TempDir()
	t.Setenv("HOME", home)
	n, err := Receive(bytes.NewReader(raw))
	if err != nil || n != 2 {
		t.Fatalf("Receive = %d, %v", n, err)
	}
	dir := filepath.Join(home, ".config", "grove", "secrets", Global)
	if m := mode(t, dir); m != 0o700 {
		t.Fatalf("dir mode %o", m)
	}
	snap := func() map[string]string {
		out := map[string]string{}
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			if m := mode(t, filepath.Join(dir, e.Name())); m != 0o600 {
				t.Fatalf("%s mode %o", e.Name(), m)
			}
			out[e.Name()] = string(b)
		}
		return out
	}
	first := snap()
	if len(first) != 4 { // recipients.txt, ONE.age, TWO.age, TWO.meta.yaml
		t.Fatalf("files = %v", first)
	}
	for name := range first {
		if strings.Contains(name, ".tmp-") {
			t.Fatalf("temp file left behind: %s", name)
		}
	}
	if n, err := Receive(bytes.NewReader(raw)); err != nil || n != 2 {
		t.Fatalf("second Receive = %d, %v", n, err)
	}
	second := snap()
	for k, v := range first {
		if second[k] != v {
			t.Fatalf("%s changed on the second receive", k)
		}
	}
	// B decrypts; a third identity does not.
	use(t, bState)
	if v, _, err := Lookup("", "ONE"); err != nil || v != "hunter2" {
		t.Fatalf("B Lookup = %q, %v", v, err)
	}
	host(t)
	if _, _, err := Lookup("", "ONE"); !errors.Is(err, ErrNotRecipient) {
		t.Fatalf("third identity: %v, want ErrNotRecipient", err)
	}
}

func TestPushFlow(t *testing.T) {
	_, bState, bR := pushFixture(t)
	var sent []byte
	res, err := Push(Global, "pc",
		func() (string, error) { return bR, nil },
		func(p []byte) (int, error) { sent = p; return 2, nil })
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != 2 || res.Recipient != bR {
		t.Fatalf("res = %+v", res)
	}
	rs, _ := Recipients(Global)
	if len(rs) != 2 {
		t.Fatalf("recipients = %v", rs)
	}
	raw, _ := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".config", "grove", "secrets", Global, "recipients.txt"))
	if !strings.Contains(string(raw), "# host: pc") {
		t.Fatalf("recipients.txt = %s", raw)
	}
	if bytes.Contains(sent, []byte("hunter2")) {
		t.Fatal("plaintext on the wire")
	}
	// the sent payload opens with B's identity
	t.Setenv("HOME", t.TempDir())
	if _, err := Receive(bytes.NewReader(sent)); err != nil {
		t.Fatal(err)
	}
	use(t, bState)
	if v, _, err := Lookup("", "TWO"); err != nil || v != "second" {
		t.Fatalf("Lookup = %q, %v", v, err)
	}
}

func TestPushErrors(t *testing.T) {
	pushFixture(t)
	boom := errors.New("boom")
	if _, err := Push(Global, "pc", func() (string, error) { return "", boom }, nil); !errors.Is(err, boom) {
		t.Fatalf("fetch error = %v", err)
	}
	if _, err := Push(Global, "pc", func() (string, error) { return "not-a-key", nil }, nil); err == nil {
		t.Fatal("garbage recipient accepted")
	}
	if _, err := Push("empty", "pc", nil, nil); err == nil || !strings.Contains(err.Error(), "nothing to push") {
		t.Fatalf("empty ns: %v", err)
	}
	_, bR := host(t)
	if _, err := Push(Global, "pc", func() (string, error) { return bR, nil }, func([]byte) (int, error) { return 1, nil }); err == nil {
		// the active identity (B) is not a recipient of A's files
		t.Fatal("push by a non-recipient succeeded")
	}
}

func TestRemoteErr(t *testing.T) {
	for _, tc := range []struct {
		code   int
		stderr string
		want   string
	}{
		{1, "unknown command \"keys\"\n\nusage: ...", "pc's gv predates gv keys — run `gv update --yes` there"},
		{1, "unknown keys verb \"receive\"", "predates gv keys"},
		{255, "Connection refused", "ssh to pc failed: Connection refused"},
		{1, "no age identity on this host — run `gv keys init`", "pc: gv keys recipient: no age identity"},
		{3, "", "exited 3"},
	} {
		err := RemoteErr("pc", "recipient", tc.code, tc.stderr)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("RemoteErr(%d, %q) = %v, want %q", tc.code, tc.stderr, err, tc.want)
		}
	}
}
