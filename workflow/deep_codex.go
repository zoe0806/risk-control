package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"risk_control/config"
)

// Codex 非交互：prompt 是指令，stdin 是 DeepInput 上下文（官方 prompt-plus-stdin）。
const codexDeepInstruction = `You are the deep screening runtime of a risk engine.
Read the JSON on stdin (protocol risk.deep.v1 DeepInput). Evidence and prompts are already computed — do not re-run hard rules or list matching.
Fill primary / secondary / report using prompts.* when present.
Reply with ONLY one JSON object (no markdown fence, no extra text):
{"protocol_version":"risk.deep.v1","decision":"APPROVE|REVIEW|REJECT","score":0.0,"primary":{"risk_score":0.0,"matched_names":[],"rationale":"","needs_secondary_review":false},"secondary":{"confirmed":false,"final_risk_score":0.0,"rationale":"","skipped":true},"report_markdown":""}`

type codexRuntime struct {
	command string
	args    []string
	timeout time.Duration
	exec    cliExecFunc
}

func newCodexRuntime(cfg config.DeepCLIConfig) (DeepRuntime, error) {
	cmd := cfg.Command
	if cmd == "" {
		cmd = "codex"
	}
	args := append([]string{}, cfg.Args...)
	if len(args) == 0 {
		args = []string{"exec", "--sandbox", "read-only", "--ephemeral"}
	}
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	return &codexRuntime{
		command: cmd,
		args:    args,
		timeout: timeout,
		exec:    defaultCLIExec,
	}, nil
}

func (r *codexRuntime) Name() string { return DeepRuntimeCodex }

func (r *codexRuntime) Invoke(ctx context.Context, in DeepInput) (DeepOutput, error) {
	if in.ProtocolVersion == "" {
		in.ProtocolVersion = DeepProtocolV1
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return DeepOutput{}, fmt.Errorf("marshal DeepInput: %w", err)
	}
	args := append(append([]string{}, r.args...), codexDeepInstruction)
	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	stdout, err := r.exec(runCtx, r.command, args, payload)
	if err != nil {
		if lookErr := execLookPathHint(r.command, err); lookErr != "" {
			return DeepOutput{ProtocolVersion: DeepProtocolV1, RuntimeName: r.Name(), Error: lookErr, Degraded: true}, fmt.Errorf("%s", lookErr)
		}
		return DeepOutput{ProtocolVersion: DeepProtocolV1, RuntimeName: r.Name(), Error: err.Error(), Degraded: true}, err
	}
	out, err := parseDeepOutputJSON(stdout)
	if err != nil {
		return DeepOutput{}, fmt.Errorf("codex output: %w", err)
	}
	out.RuntimeName = r.Name()
	if out.TraceID == "" {
		out.TraceID = in.TraceID
	}
	return out, nil
}

func execLookPathHint(command string, err error) string {
	if err == nil {
		return ""
	}
	if _, lookErr := exec.LookPath(command); lookErr != nil {
		return fmt.Sprintf("%s not found on PATH; install Codex CLI, run `codex` once to login, then set deepRuntime.kind=codex", command)
	}
	return ""
}
