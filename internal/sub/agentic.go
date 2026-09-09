package sub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"time"
)

// ReadOnlyTools is the --allowedTools list an agentic gv sub call runs
// under: read-only inspection commands, no mutation.
const ReadOnlyTools = "Read,Grep,Glob,Bash(rg:*),Bash(grep:*),Bash(sed:*),Bash(cat:*),Bash(head:*),Bash(tail:*),Bash(git log:*),Bash(git diff:*),Bash(git show:*),Bash(ls:*),Bash(find:*)"

type agenticOutput struct {
	Result        *string `json:"result"`
	NumTurns      int     `json:"num_turns"`
	DurationAPIMs int64   `json:"duration_api_ms"`
	Usage         struct {
		InputTokens          int `json:"input_tokens"`
		OutputTokens         int `json:"output_tokens"`
		CacheReadInputTokens int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

// Agentic runs claudeBin -p --bare against the lane, read-only tools only,
// and returns the final result text plus turn/usage accounting.
func Agentic(ctx context.Context, l Lane, key, claudeBin, cwd, user, start string, maxTurns int, allowWrite bool, timeout time.Duration) (Result, error) {
	tools := ReadOnlyTools
	if allowWrite {
		tools += ",Edit,Write"
	}

	prompt := Preamble + "\n\n" + user
	if start != "" {
		prompt += "\n\nStart by running: " + start
	}

	// The prompt goes right after -p, NOT last: --allowedTools takes a
	// variadic list (`--allowedTools, --allowed-tools <tools...>` per
	// `claude --help`) and a trailing bare-string prompt gets silently
	// swallowed into it — verified live 2026-09-06, claude then refuses
	// with "Input must be provided either through stdin or as a prompt
	// argument when using --print" because no positional prompt survived.
	args := []string{
		"-p", prompt,
		"--bare",
		"--model", l.Model,
		"--max-turns", strconv.Itoa(maxTurns),
		"--output-format", "json",
		"--allowedTools", tools,
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, claudeBin, args...)
	cmd.Dir = cwd
	if devnull, err := os.Open(os.DevNull); err == nil {
		cmd.Stdin = devnull
		defer devnull.Close()
	}

	env := os.Environ()
	keys := make([]string, 0, len(l.Env))
	for k := range l.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+l.Env[k])
	}
	env = append(env,
		"ANTHROPIC_BASE_URL="+l.BaseURL,
		"ANTHROPIC_AUTH_TOKEN="+key,
		"ANTHROPIC_MODEL="+l.Model,
	)
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start2 := time.Now()
	runErr := cmd.Run()
	millis := time.Since(start2).Milliseconds()

	// claude prints "[claude-code:unrecognized_model] …" style warning
	// lines before the JSON on third-party lanes — skip to the first '{'.
	out := stdout.Bytes()
	if i := bytes.IndexByte(out, '{'); i >= 0 {
		out = out[i:]
	}

	var parsed agenticOutput
	if jsonErr := json.Unmarshal(out, &parsed); jsonErr != nil {
		if runErr != nil {
			tail := stderr.String()
			if len(tail) > 500 {
				tail = tail[len(tail)-500:]
			}
			return Result{Millis: millis}, ErrUpstream{Status: exitCode(runErr), Body: tail}
		}
		return Result{Millis: millis}, fmt.Errorf("gv sub: parse claude output: %w", jsonErr)
	}

	if parsed.Result == nil || *parsed.Result == "" {
		return Result{Turns: parsed.NumTurns, Millis: millis}, ErrNoAnswer
	}

	return Result{
		Text:            *parsed.Result,
		InputTokens:     parsed.Usage.InputTokens,
		OutputTokens:    parsed.Usage.OutputTokens,
		CacheReadTokens: parsed.Usage.CacheReadInputTokens,
		Turns:           parsed.NumTurns,
		Millis:          millis,
	}, nil
}

func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
