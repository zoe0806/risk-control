package workflow

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"risk_control/tools"
)

func TestParseDeepOutputJSONIgnoresLogs(t *testing.T) {
	raw := []byte("thinking...\n{\"protocol_version\":\"risk.deep.v1\",\"decision\":\"APPROVE\",\"score\":0.12}\n")
	out, err := parseDeepOutputJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision != tools.DecisionApprove || out.Score < 0.1 {
		t.Fatalf("%+v", out)
	}
}

func TestCLIRuntimeJSONProtocol(t *testing.T) {
	rt := &cliRuntime{
		command: "fake-runtime",
		timeout: time.Second,
		exec: func(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error) {
			var in DeepInput
			if err := json.Unmarshal(stdin, &in); err != nil {
				t.Fatal(err)
			}
			if in.ProtocolVersion != DeepProtocolV1 || in.TraceID != "abc" {
				t.Fatalf("input %+v", in)
			}
			return json.Marshal(DeepOutput{
				ProtocolVersion: DeepProtocolV1,
				Decision:        tools.DecisionReview,
				Score:           0.42,
				TraceID:         in.TraceID,
				ReportMarkdown:  "## cli",
			})
		},
	}
	out, err := rt.Invoke(context.Background(), DeepInput{TraceID: "abc", Domain: tools.BusinessCrossBorder})
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision != tools.DecisionReview || out.RuntimeName != DeepRuntimeCLI || out.ReportMarkdown != "## cli" {
		t.Fatalf("%+v", out)
	}
}

func TestParseDeepOutputJSONFenced(t *testing.T) {
	raw := []byte("```json\n{\"decision\":\"REJECT\",\"score\":0.9,\"report_markdown\":\"x\"}\n```")
	out, err := parseDeepOutputJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision != tools.DecisionReject {
		t.Fatalf("%+v", out)
	}
}

func TestCodexRuntimePromptPlusStdin(t *testing.T) {
	rt := &codexRuntime{
		command: "codex",
		args:    []string{"exec", "--sandbox", "read-only", "--ephemeral"},
		timeout: time.Second,
		exec: func(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error) {
			if name != "codex" {
				t.Fatalf("cmd %s", name)
			}
			if len(args) < 4 || args[0] != "exec" {
				t.Fatalf("args %v", args)
			}
			if args[len(args)-1] != codexDeepInstruction {
				t.Fatalf("missing instruction, last=%q", args[len(args)-1])
			}
			var in DeepInput
			if err := json.Unmarshal(stdin, &in); err != nil {
				t.Fatal(err)
			}
			if in.TraceID != "t-codex" {
				t.Fatalf("stdin %+v", in)
			}
			return []byte("```json\n{\"decision\":\"REVIEW\",\"score\":0.5,\"report_markdown\":\"codex\"}\n```"), nil
		},
	}
	out, err := rt.Invoke(context.Background(), DeepInput{TraceID: "t-codex", Domain: tools.BusinessCrossBorder})
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision != tools.DecisionReview || out.RuntimeName != DeepRuntimeCodex || out.ReportMarkdown != "codex" {
		t.Fatalf("%+v", out)
	}
}
