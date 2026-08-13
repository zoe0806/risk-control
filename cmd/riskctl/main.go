package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"risk_control/config"
	"risk_control/llm"
	"risk_control/store"
	"risk_control/tools"
	"risk_control/workflow"
)

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "eval":
		if err := runEval(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "deep":
		if err := runDeep(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `riskctl — 风控内核 CLI（与 HTTP 服务共用同一套规则/仲裁）

  riskctl eval  -i event.json [-expect APPROVE]   仅本地引擎（无 LLM）
  riskctl deep                                     stdin DeepInput JSON → stdout DeepOutput JSON

eval 用于策略 golden / CI。deep 实现 risk.deep.v1，可被 deepRuntime.kind=cli 调用，
也可换成 Codex 或自研 runtime CLI（stdin/stdout 同一契约）。
`)
}

func runEval(args []string) error {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	input := fs.String("i", "", "ScreeningRequest JSON 文件（- 表示 stdin）")
	expect := fs.String("expect", "", "期望决策 APPROVE|REVIEW|REJECT")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *input == "" {
		return fmt.Errorf("eval: -i is required")
	}
	raw, err := readInput(*input)
	if err != nil {
		return err
	}
	var req tools.ScreeningRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return fmt.Errorf("parse request: %w", err)
	}

	cfg := *config.Load()
	cfg.DeepRuntime.Kind = workflow.DeepRuntimeOff
	eng, err := workflow.NewRiskEngine(context.Background(), &workflow.GraphDeps{
		Store: store.Noop{},
		Cfg:   cfg,
	})
	if err != nil {
		return err
	}
	res, err := eng.EvaluateLocal(context.Background(), req)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		return err
	}
	if *expect != "" && !strings.EqualFold(res.Decision, strings.TrimSpace(*expect)) {
		return fmt.Errorf("expect %s got %s", *expect, res.Decision)
	}
	return nil
}

func runDeep(args []string) error {
	fs := flag.NewFlagSet("deep", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	var in workflow.DeepInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("parse DeepInput: %w", err)
	}
	if in.ProtocolVersion == "" {
		in.ProtocolVersion = workflow.DeepProtocolV1
	}

	cfg := *config.Load()
	cfg.DeepRuntime.Kind = workflow.DeepRuntimeNative
	ctx := context.Background()
	router, err := llm.NewRouter(ctx, cfg)
	if err != nil {
		return err
	}
	rt, err := workflow.NewDeepRuntime(ctx, &workflow.GraphDeps{
		Store:  store.Noop{},
		Router: router,
		Cfg:    cfg,
	})
	if err != nil {
		return err
	}
	out, err := rt.Invoke(ctx, in)
	if err != nil {
		enc := json.NewEncoder(os.Stdout)
		_ = enc.Encode(workflow.DeepOutput{
			ProtocolVersion: workflow.DeepProtocolV1,
			Decision:        tools.DecisionReview,
			Score:           0.5,
			Degraded:        true,
			Error:           err.Error(),
			RuntimeName:     rt.Name(),
		})
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}
