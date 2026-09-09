package sub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Result is the outcome of one Raw or Agentic call.
type Result struct {
	Text                                       string
	InputTokens, OutputTokens, CacheReadTokens int
	Turns                                      int // agentic only
	Millis                                     int64
}

// ErrNoAnswer means the call completed but produced no usable text (an
// empty response after retry, or an agentic turn cap with a nil result).
var ErrNoAnswer = errors.New("no answer")

// ErrUpstream wraps a non-2xx HTTP response (Raw) or an unparseable
// non-zero exit (Agentic).
type ErrUpstream struct {
	Status int
	Body   string
}

func (e ErrUpstream) Error() string {
	return fmt.Sprintf("upstream error (status %d): %s", e.Status, e.Body)
}

// maxBodyBytes bounds how much of an upstream response Raw ever reads.
const maxBodyBytes = 4 << 20

var httpClient = &http.Client{}

// retryDelay is a var (not a const) so tests can shrink it.
var retryDelay = 2 * time.Second

type rawRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    string          `json:"system,omitempty"`
	Messages  []rawMessage    `json:"messages"`
	Thinking  *rawThinkingCfg `json:"thinking,omitempty"`
}

type rawMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type rawThinkingCfg struct {
	Type string `json:"type"`
}

type rawResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens          int `json:"input_tokens"`
		OutputTokens         int `json:"output_tokens"`
		CacheReadInputTokens int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

// Raw calls the lane's /v1/messages endpoint directly (the Anthropic
// Messages API shape) with a single user turn. It retries once (after a
// 2s sleep) on a transport error, HTTP 429/5xx, or an empty Text; a
// still-empty result after that retry is ErrNoAnswer.
func Raw(ctx context.Context, l Lane, key, system, user string, maxTokens int, thinking bool, timeout time.Duration) (Result, error) {
	body := rawRequest{
		Model:     l.Model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  []rawMessage{{Role: "user", Content: user}},
	}
	if !thinking {
		body.Thinking = &rawThinkingCfg{Type: "disabled"}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Result{}, err
	}

	start := time.Now()
	res, err := doRaw(ctx, l, key, raw, timeout)
	if shouldRetryRaw(res, err) {
		time.Sleep(retryDelay)
		res, err = doRaw(ctx, l, key, raw, timeout)
	}
	res.Millis = time.Since(start).Milliseconds()
	if err == nil && res.Text == "" {
		err = ErrNoAnswer
	}
	return res, err
}

// shouldRetryRaw decides whether one retry is warranted: a transport
// error, an HTTP 429/5xx, or a clean response with empty text. Any other
// non-2xx (e.g. 400) is a caller mistake, not worth retrying.
func shouldRetryRaw(res Result, err error) bool {
	if err == nil {
		return res.Text == ""
	}
	var up ErrUpstream
	if errors.As(err, &up) {
		return up.Status == http.StatusTooManyRequests || up.Status >= 500
	}
	return true // transport error
}

func doRaw(ctx context.Context, l Lane, key string, body []byte, timeout time.Duration) (Result, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	url := strings.TrimRight(l.BaseURL, "/") + "/v1/messages"
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("gv sub: request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return Result{}, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b := string(respBody)
		if len(b) > 200 {
			b = b[:200]
		}
		return Result{}, ErrUpstream{Status: resp.StatusCode, Body: b}
	}

	var parsed rawResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return Result{}, fmt.Errorf("gv sub: parse response: %w", err)
	}

	var text strings.Builder
	for _, c := range parsed.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	return Result{
		Text:            text.String(),
		InputTokens:     parsed.Usage.InputTokens,
		OutputTokens:    parsed.Usage.OutputTokens,
		CacheReadTokens: parsed.Usage.CacheReadInputTokens,
	}, nil
}
