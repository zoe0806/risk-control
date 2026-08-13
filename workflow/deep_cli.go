package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"risk_control/config"
)

type cliExecFunc func(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error)

type cliRuntime struct {
	command string
	args    []string
	timeout time.Duration
	exec    cliExecFunc
}

func newCLIRuntime(cfg config.DeepCLIConfig) (DeepRuntime, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("deepRuntime.cli.command is empty")
	}
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &cliRuntime{
		command: cfg.Command,
		args:    append([]string{}, cfg.Args...),
		timeout: timeout,
		exec:    defaultCLIExec,
	}, nil
}

func (r *cliRuntime) Name() string { return DeepRuntimeCLI }

func (r *cliRuntime) Invoke(ctx context.Context, in DeepInput) (DeepOutput, error) {
	if in.ProtocolVersion == "" {
		in.ProtocolVersion = DeepProtocolV1
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return DeepOutput{}, fmt.Errorf("marshal DeepInput: %w", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	stdout, err := r.exec(runCtx, r.command, r.args, payload)
	if err != nil {
		return DeepOutput{ProtocolVersion: DeepProtocolV1, RuntimeName: r.Name(), Error: err.Error(), Degraded: true}, err
	}
	out, err := parseDeepOutputJSON(stdout)
	if err != nil {
		return DeepOutput{}, err
	}
	out.RuntimeName = r.Name()
	if out.TraceID == "" {
		out.TraceID = in.TraceID
	}
	return out, nil
}

func defaultCLIExec(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		if msg == "" {
			msg = stdout.String()
		}
		return stdout.Bytes(), fmt.Errorf("cli %s: %w: %s", name, err, msg)
	}
	return stdout.Bytes(), nil
}
