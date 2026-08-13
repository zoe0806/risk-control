package workflow

import (
	"testing"

	"risk_control/config"
	"risk_control/tools"
)

func TestApplyFastFilterWhitelist(t *testing.T) {
	st := &tools.PipelineState{
		Transaction: tools.CrossBorderTransaction{Counterparty: "Acme Corp", Country: "US"},
		Party:       tools.NormalizePartyName("Acme Corp", "US"),
		Gate:        &tools.CBLocalGate{},
	}
	rules := config.CrossBorderRules{WhitelistKeys: []string{"ACME_CORP"}}
	ApplyFastFilter(st, rules, NewVelocityTracker())
	if !st.Gate.WhitelistPass || !st.Gate.EarlyExit {
		t.Fatalf("gate=%+v", st.Gate)
	}
	ApplyRuleDecision(st)
	if st.Decision != tools.DecisionApprove || !st.SkippedAI {
		t.Fatalf("decision=%s skipped=%v", st.Decision, st.SkippedAI)
	}
}

func TestApplyFastFilterCountryBlock(t *testing.T) {
	st := &tools.PipelineState{
		Transaction: tools.CrossBorderTransaction{Counterparty: "Someone", Country: "KP"},
		Party:       tools.NormalizePartyName("Someone", "KP"),
		Gate:        &tools.CBLocalGate{},
	}
	rules := config.Config{}.CBRules()
	ApplyFastFilter(st, rules, NewVelocityTracker())
	if !st.Gate.HardBlock || st.Gate.BlockReason != tools.PolicyCountryBlock {
		t.Fatalf("gate=%+v", st.Gate)
	}
}

func TestApplyMatchRoutingSkipAI(t *testing.T) {
	st := &tools.PipelineState{
		Gate:       &tools.CBLocalGate{},
		Candidates: nil,
	}
	rules := config.Config{}.CBRules()
	ApplyMatchRouting(st, rules)
	if !st.Gate.SkipAI {
		t.Fatal("expected skip AI")
	}
	ApplyRuleDecision(st)
	if st.Decision != tools.DecisionApprove {
		t.Fatalf("got %s", st.Decision)
	}
}

func TestApplyMatchRoutingAutoReject(t *testing.T) {
	st := &tools.PipelineState{
		Gate: &tools.CBLocalGate{},
		Candidates: []tools.SanctionCandidate{
			{NameOriginal: "X", NameNormalized: "X", MatchScore: 0.95},
		},
	}
	rules := config.Config{}.CBRules()
	ApplyMatchRouting(st, rules)
	if !st.Gate.AutoReject || !st.Gate.SkipAI {
		t.Fatalf("gate=%+v", st.Gate)
	}
	ApplyRuleDecision(st)
	if st.Decision != tools.DecisionReject {
		t.Fatalf("got %s", st.Decision)
	}
}

func TestVelocity(t *testing.T) {
	v := NewVelocityTracker()
	rules := config.CrossBorderRules{VelocityWindowSec: 60, VelocityMaxCount: 3}
	for i := 0; i < 3; i++ {
		st := &tools.PipelineState{
			Transaction: tools.CrossBorderTransaction{AccountID: "A1", Counterparty: "X", Country: "US"},
			Party:       tools.NormalizePartyName("X", "US"),
			Gate:        &tools.CBLocalGate{},
		}
		ApplyFastFilter(st, rules, v)
		if st.Gate.HardBlock {
			t.Fatalf("blocked too early at %d", i)
		}
	}
	st := &tools.PipelineState{
		Transaction: tools.CrossBorderTransaction{AccountID: "A1", Counterparty: "X", Country: "US"},
		Party:       tools.NormalizePartyName("X", "US"),
		Gate:        &tools.CBLocalGate{},
	}
	ApplyFastFilter(st, rules, v)
	if !st.Gate.HardBlock || st.Gate.BlockReason != tools.PolicyVelocity {
		t.Fatalf("expected velocity block, gate=%+v", st.Gate)
	}
}
