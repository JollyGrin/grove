package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/JollyGrin/grove/internal/config"
	"github.com/JollyGrin/grove/internal/openrouter"
	"github.com/JollyGrin/grove/internal/state"
	"github.com/JollyGrin/grove/internal/sub"
)

// gv sub is the one verb in this repo that calls os.Exit directly — it
// owns a small vocabulary of process exit codes (usage=2, no-answer=3,
// paid-lane=4, upstream=5) so a calling agent's Bash tool can branch on
// them without parsing stderr. Every other verb returns an error to
// main()'s generic "gv: <err>" / exit 1 path; this one does not.
const (
	subExitUsage    = 2
	subExitNoAnswer = 3
	subExitPaid     = 4
	subExitUpstream = 5
)

// subFail prints a "gv sub: …" message and exits with code. It returns
// an error only so callers can `return subFail(...)` and satisfy
// cmdSub's error-returning signature; os.Exit never lets that value
// propagate.
func subFail(code int, format string, a ...any) error {
	fmt.Fprintf(os.Stderr, "gv sub: "+format+"\n", a...)
	os.Exit(code)
	return nil
}

func cmdSub(args []string) error {
	fs := flag.NewFlagSet("sub", flag.ExitOnError)
	flagLane := fs.String("lane", "", "model_profiles lane (or GV_SUB_LANE, or sub.lane)")
	flagModel := fs.String("model", "", "model slug override")
	agentic := fs.Bool("agentic", false, "run a read-only claude -p --bare session on the lane instead of one raw call")
	start := fs.String("start", "", "agentic: the first command to run")
	maxTurnsFlag := fs.Int("max-turns", 0, "agentic: turn cap (default sub.max_turns)")
	maxTokens := fs.Int("max-tokens", 4096, "raw: max_tokens for the completion")
	timeoutFlag := fs.String("timeout", "", "per-call timeout (default sub.timeout)")
	thinking := fs.Bool("thinking", false, "raw: leave thinking enabled (some backends return empty text with it on)")
	allowPaid := fs.Bool("allow-paid", false, "allow a lane that bills per token (openrouter-*)")
	allowWrite := fs.Bool("allow-write", false, "agentic: also allow Edit/Write tools")
	asJSON := fs.Bool("json", false, "machine-readable output")
	dryRun := fs.Bool("dry-run", false, "print the resolved plan, make no call")
	quiet := fs.Bool("quiet", false, "suppress the stderr summary line")
	lanesFlag := fs.Bool("lanes", false, "list usable lanes")
	ledgerFlag := fs.Bool("ledger", false, "print this workspace's sub.jsonl as a table")
	rest := parseAnywhere(fs, args)

	cfg, cfgErr := loadCfg()

	if *lanesFlag {
		if cfgErr != nil {
			return cfgErr
		}
		return subLanes(cfg, *asJSON)
	}
	if *ledgerFlag {
		return subLedgerTable(*asJSON)
	}
	if cfgErr != nil {
		return cfgErr
	}

	if len(rest) == 0 {
		return subFail(subExitUsage, `usage: gv sub "<prompt>" [path …] [--lane L] [--model M] [--agentic [--start C]] [flags]`)
	}
	prompt, paths := rest[0], rest[1:]

	lane, err := sub.ResolveLane(cfg, *flagLane, *flagModel, os.Getenv)
	if err != nil {
		if errors.Is(err, sub.ErrNoLane) {
			return subFail(subExitUsage, "no lane configured — set sub.lane in ~/.config/grove/config.yaml or .grove/config.yaml, or pass --lane; see gv sub --lanes")
		}
		return subFail(subExitUsage, "%s", err)
	}
	if lane.Paid && !*allowPaid {
		return subFail(subExitPaid, "%s bills per token; pass --allow-paid", lane.Name)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	maxChars := cfg.Sub.MaxInputChars
	var inputs []sub.Input
	if len(paths) > 0 {
		inputs, err = sub.Collect(paths)
		if err != nil {
			return subFail(subExitUsage, "%s", err)
		}
	} else if st, statErr := os.Stdin.Stat(); statErr == nil && st.Mode()&os.ModeCharDevice == 0 {
		data, rerr := io.ReadAll(io.LimitReader(os.Stdin, int64(maxChars)+1))
		if rerr != nil {
			return rerr
		}
		inputs = []sub.Input{{Path: "stdin", Content: data}}
	}

	userMsg, err := sub.Assemble(prompt, inputs, maxChars)
	if err != nil {
		return subFail(subExitUsage, "%s", err)
	}

	turns := cfg.Sub.MaxTurns
	if *maxTurnsFlag > 0 {
		turns = *maxTurnsFlag
	}
	timeoutStr := cfg.Sub.Timeout
	if *timeoutFlag != "" {
		timeoutStr = *timeoutFlag
	}
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return subFail(subExitUsage, "--timeout: %s", err)
	}

	mode := "raw"
	if *agentic {
		mode = "agentic"
	}

	secretsPath := config.SecretsPath()
	key, err := lane.Key(secretsPath)
	if err != nil {
		return subFail(subExitUsage, "%s", err)
	}

	if *dryRun {
		fmt.Printf("lane=%s model=%s mode=%s inputs=%d files, %d chars key=%s url=%s\n",
			lane.Name, lane.Model, mode, len(inputs), inputChars(inputs),
			openrouter.Mask(key), strings.TrimRight(lane.BaseURL, "/")+"/v1/messages")
		return nil
	}

	system := sub.Preamble
	if cfg.Sub.SystemFile != "" {
		b, rerr := os.ReadFile(cfg.Sub.SystemFile)
		if rerr != nil {
			return subFail(subExitUsage, "sub.system_file: %s", rerr)
		}
		system = string(b)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var res sub.Result
	var callErr error
	if *agentic {
		res, callErr = sub.Agentic(ctx, lane, key, "claude", cwd, userMsg, *start, turns, *allowWrite, timeout)
	} else {
		res, callErr = sub.Raw(ctx, lane, key, system, userMsg, *maxTokens, *thinking, timeout)
	}

	exitCode := 0
	if callErr != nil {
		exitCode = subExitFor(callErr)
	}
	if lerr := appendSubLedger(cfg, cwd, lane, mode, prompt, inputChars(inputs), res, exitCode); lerr != nil {
		fmt.Fprintf(os.Stderr, "gv sub: ledger append failed: %v\n", lerr)
	}

	if callErr != nil {
		if errors.Is(callErr, sub.ErrNoAnswer) {
			if mode == "agentic" {
				return subFail(subExitNoAnswer, `no answer within %d turns — add --start "<grep …>" or raise --max-turns`, turns)
			}
			return subFail(subExitNoAnswer, "no answer (empty response)")
		}
		return subFail(subExitUpstream, "%s", callErr)
	}

	if *asJSON {
		return emitJSON("sub", struct {
			Lane         string `json:"lane"`
			Model        string `json:"model"`
			Mode         string `json:"mode"`
			InputChars   int    `json:"input_chars"`
			InputTokens  int    `json:"input_tokens"`
			OutputTokens int    `json:"output_tokens"`
			CachedTokens int    `json:"cached_tokens"`
			Turns        int    `json:"turns"`
			Millis       int64  `json:"ms"`
			Answer       string `json:"answer"`
		}{lane.Name, lane.Model, mode, inputChars(inputs), res.InputTokens, res.OutputTokens, res.CacheReadTokens, res.Turns, res.Millis, res.Text})
	}

	fmt.Println(res.Text)
	if !*quiet {
		fmt.Fprintf(os.Stderr, "gv sub: %s/%s %s in=%sk out=%sk %.1fs\n",
			lane.Name, lane.Model, mode, kilo(res.InputTokens), kilo(res.OutputTokens), float64(res.Millis)/1000)
	}
	return nil
}

// subExitFor maps a call error to gv sub's exit vocabulary: a turn-capped
// or empty-response call is 3 (no answer), anything else — a bad
// response, a wrapped timeout, a transport failure — is 5 (upstream).
func subExitFor(err error) int {
	if errors.Is(err, sub.ErrNoAnswer) {
		return subExitNoAnswer
	}
	return subExitUpstream
}

func inputChars(inputs []sub.Input) int {
	n := 0
	for _, in := range inputs {
		n += len(in.Content)
	}
	return n
}

func kilo(n int) string { return fmt.Sprintf("%.1f", float64(n)/1000) }

// promptHead is the ledger's short prompt column: first 80 runes,
// newlines flattened to spaces so a jsonl row stays one line to read.
func promptHead(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) > 80 {
		r = r[:80]
	}
	return string(r)
}

func appendSubLedger(cfg *config.Config, cwd string, lane sub.Lane, mode, prompt string, chars int, res sub.Result, exit int) error {
	sd := stateDir()
	ticket := ""
	if t := state.FindByCwd(state.ReadTasks(sd), cwd); t != nil {
		ticket = t.Ticket
	}
	ws := ""
	if cfg != nil {
		ws = cfg.Workspace.Label
	}
	return sub.Append(sd, sub.Record{
		Time:         time.Now().UTC(),
		Workspace:    ws,
		Ticket:       ticket,
		Lane:         lane.Name,
		Model:        lane.Model,
		Mode:         mode,
		InputChars:   chars,
		InputTokens:  res.InputTokens,
		OutputTokens: res.OutputTokens,
		CachedTokens: res.CacheReadTokens,
		Turns:        res.Turns,
		Millis:       res.Millis,
		Exit:         exit,
		PromptHead:   promptHead(prompt),
	})
}

type subLaneRow struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	Haiku   string `json:"haiku,omitempty"`
	Sonnet  string `json:"sonnet,omitempty"`
	Opus    string `json:"opus,omitempty"`
	Billing string `json:"billing"`
	KeyEnv  string `json:"key_env"`
	KeyOK   bool   `json:"key_present"`
}

func subLanes(cfg *config.Config, asJSON bool) error {
	secretsPath := config.SecretsPath()
	names := make([]string, 0, len(cfg.ModelProfiles))
	for n := range cfg.ModelProfiles {
		names = append(names, n)
	}
	sort.Strings(names)

	var rows []subLaneRow
	for _, n := range names {
		p := cfg.ModelProfiles[n]
		if p == nil {
			continue
		}
		billing := "flat"
		if strings.HasPrefix(n, "openrouter-") {
			billing = "paid"
		}
		key := openrouter.Key(secretsPath, p.AuthTokenEnv)
		rows = append(rows, subLaneRow{
			Name: n, Host: p.BaseURL, Haiku: p.Haiku, Sonnet: p.Sonnet, Opus: p.Opus,
			Billing: billing, KeyEnv: p.AuthTokenEnv, KeyOK: key != "",
		})
	}
	anthropicKey := openrouter.Key(secretsPath, "ANTHROPIC_API_KEY")
	rows = append(rows, subLaneRow{
		Name: "anthropic", Host: "https://api.anthropic.com", Haiku: "claude-haiku-4-5",
		Billing: "flat", KeyEnv: "ANTHROPIC_API_KEY", KeyOK: anthropicKey != "",
	})

	if asJSON {
		return emitJSON("lanes", rows)
	}

	fmt.Printf("%-20s %-32s %-30s %-8s %s\n", "NAME", "HOST", "HAIKU/SONNET/OPUS", "BILLING", "KEY")
	for _, r := range rows {
		var models []string
		for _, m := range []string{r.Haiku, r.Sonnet, r.Opus} {
			if m != "" {
				models = append(models, m)
			}
		}
		keyStr := "yes"
		if !r.KeyOK {
			keyStr = "no: $" + r.KeyEnv
		}
		fmt.Printf("%-20s %-32s %-30s %-8s %s\n", r.Name, r.Host, strings.Join(models, "/"), r.Billing, keyStr)
	}
	return nil
}

func subLedgerTable(asJSON bool) error {
	rows, err := sub.Read(stateDir())
	if err != nil {
		return err
	}
	if asJSON {
		return emitJSON("rows", rows)
	}
	fmt.Printf("%-20s %-11s %-24s %-8s %-6s %-6s %-6s %-8s %-4s %s\n",
		"TIME", "TICKET", "LANE/MODEL", "MODE", "IN", "OUT", "TURNS", "MS", "EXIT", "PROMPT")
	for _, r := range rows {
		fmt.Printf("%-20s %-11s %-24s %-8s %-6d %-6d %-6d %-8d %-4d %s\n",
			r.Time.Format(time.RFC3339), r.Ticket, r.Lane+"/"+r.Model, r.Mode,
			r.InputTokens, r.OutputTokens, r.Turns, r.Millis, r.Exit, r.PromptHead)
	}
	return nil
}
