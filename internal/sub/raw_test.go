package sub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func withFastRetry(t *testing.T) {
	t.Helper()
	old := retryDelay
	retryDelay = time.Millisecond
	t.Cleanup(func() { retryDelay = old })
}

func TestRawHappy(t *testing.T) {
	withFastRetry(t)
	var gotHeaders http.Header
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Write([]byte(`{"content":[{"type":"text","text":"hello "},{"type":"text","text":"world"}],"usage":{"input_tokens":5,"output_tokens":2,"cache_read_input_tokens":1}}`))
	}))
	defer srv.Close()

	l := Lane{BaseURL: srv.URL, Model: "m"}
	res, err := Raw(context.Background(), l, "sekret-key", "sys", "user text", 100, false, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "hello world" {
		t.Errorf("Text = %q, want %q", res.Text, "hello world")
	}
	if res.InputTokens != 5 || res.OutputTokens != 2 || res.CacheReadTokens != 1 {
		t.Errorf("usage = %+v", res)
	}
	if gotHeaders.Get("x-api-key") != "sekret-key" {
		t.Errorf("x-api-key = %q", gotHeaders.Get("x-api-key"))
	}
	if gotHeaders.Get("anthropic-version") != "2023-06-01" {
		t.Errorf("anthropic-version = %q", gotHeaders.Get("anthropic-version"))
	}
	if gotHeaders.Get("content-type") != "application/json" {
		t.Errorf("content-type = %q", gotHeaders.Get("content-type"))
	}
	thinking, _ := gotBody["thinking"].(map[string]any)
	if thinking["type"] != "disabled" {
		t.Errorf("thinking = %v, want type disabled", gotBody["thinking"])
	}
}

func TestRawThinkingFlag(t *testing.T) {
	withFastRetry(t)
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer srv.Close()

	l := Lane{BaseURL: srv.URL, Model: "m"}
	_, err := Raw(context.Background(), l, "k", "", "u", 10, true, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := gotBody["thinking"]; ok {
		t.Errorf("thinking present in body %v, want omitted when thinking=true", gotBody)
	}
}

func TestRawRetry429(t *testing.T) {
	withFastRetry(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer srv.Close()

	l := Lane{BaseURL: srv.URL, Model: "m"}
	res, err := Raw(context.Background(), l, "k", "", "u", 10, false, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "ok" {
		t.Errorf("Text = %q", res.Text)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (one retry)", calls)
	}
}

func TestRawEmptyRetryThenNoAnswer(t *testing.T) {
	withFastRetry(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"content":[]}`))
	}))
	defer srv.Close()

	l := Lane{BaseURL: srv.URL, Model: "m"}
	_, err := Raw(context.Background(), l, "k", "", "u", 10, false, time.Second)
	if !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("err = %v, want ErrNoAnswer", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (one retry)", calls)
	}
}

func TestRawUpstreamError(t *testing.T) {
	withFastRetry(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request: missing field"))
	}))
	defer srv.Close()

	l := Lane{BaseURL: srv.URL, Model: "m"}
	_, err := Raw(context.Background(), l, "k", "", "u", 10, false, time.Second)
	var up ErrUpstream
	if !errors.As(err, &up) {
		t.Fatalf("err = %v, want ErrUpstream", err)
	}
	if up.Status != 400 {
		t.Errorf("Status = %d, want 400", up.Status)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (400 is not retried)", calls)
	}
}

func TestRawTimeout(t *testing.T) {
	withFastRetry(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte(`{"content":[{"type":"text","text":"late"}]}`))
	}))
	defer srv.Close()

	l := Lane{BaseURL: srv.URL, Model: "m"}
	_, err := Raw(context.Background(), l, "k", "", "u", 10, false, 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want it to wrap context.DeadlineExceeded", err)
	}
}

func TestRawKeyNeverLogged(t *testing.T) {
	withFastRetry(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	oldStdout, oldStderr := os.Stdout, os.Stderr
	r1, w1, _ := os.Pipe()
	r2, w2, _ := os.Pipe()
	os.Stdout, os.Stderr = w1, w2
	done := make(chan struct{})
	go func() {
		io.Copy(&stdout, r1)
		close(done)
	}()
	done2 := make(chan struct{})
	go func() {
		io.Copy(&stderr, r2)
		close(done2)
	}()

	l := Lane{BaseURL: srv.URL, Model: "m"}
	_, err := Raw(context.Background(), l, "super-secret-key", "", "u", 10, false, time.Second)

	w1.Close()
	w2.Close()
	os.Stdout, os.Stderr = oldStdout, oldStderr
	<-done
	<-done2

	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "super-secret-key") || strings.Contains(stderr.String(), "super-secret-key") {
		t.Errorf("key leaked into stdout/stderr: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
