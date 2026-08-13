package workflow

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"risk_control/config"
	"risk_control/store"
	"risk_control/tools"
)

func TestPreAnalyzeBuckets(t *testing.T) {
	rules := config.Config{}.CBRules()
	orch := config.Config{}.Orch()
	p := tools.NormalizePartyName("Acme Corp", "US")
	pa := PreAnalyze(tools.CrossBorderTransaction{Counterparty: "Acme Corp", Country: "US"}, p, rules, orch)
	if pa.Bucket != tools.BucketFast {
		t.Fatalf("whitelist bucket=%s", pa.Bucket)
	}
	p2 := tools.NormalizePartyName("Someone", "RU")
	pa2 := PreAnalyze(tools.CrossBorderTransaction{Counterparty: "Someone", Country: "RU", AmountMinorUnit: 100}, p2, rules, orch)
	if pa2.Bucket != tools.BucketDeep {
		t.Fatalf("high risk bucket=%s", pa2.Bucket)
	}
}

func TestArbitrateRejectWins(t *testing.T) {
	dec, score, _, conflict, _ := Arbitrate([]EngineResult{
		{Engine: "a", Decision: tools.DecisionApprove, Score: 0.1},
		{Engine: "b", Decision: tools.DecisionReject, Score: 0.9},
	})
	if dec != tools.DecisionReject || score < 0.65 || !conflict {
		t.Fatalf("dec=%s score=%f conflict=%v", dec, score, conflict)
	}
}

func TestLightMLScore(t *testing.T) {
	w := config.DefaultLightWeights()
	low := ScoreLightML(tools.FeatureVector{AmountNorm: 0.05}, w, 0)
	high := ScoreLightML(tools.FeatureVector{AmountNorm: 1, CountryBlocked: true}, w, 0.8)
	if low >= high {
		t.Fatalf("low=%f high=%f", low, high)
	}
}

func TestEntityGraphSharedDevice(t *testing.T) {
	g := NewEntityGraph()
	orch := config.Config{}.Orch()
	orch.GraphSharedDeviceReview = 2
	txn := tools.CrossBorderTransaction{DeviceID: "d1", AccountID: "a1", BankName: "B"}
	p1 := tools.NormalizePartyName("Alpha", "US")
	_, dec1, _, _ := g.Observe(txn, p1, orch)
	if dec1 != tools.DecisionApprove {
		t.Fatalf("first=%s", dec1)
	}
	p2 := tools.NormalizePartyName("Beta", "US")
	_, dec2, detail, pols := g.Observe(txn, p2, orch)
	if dec2 != tools.DecisionReview {
		t.Fatalf("second=%s detail=%s", dec2, detail)
	}
	if len(pols) == 0 {
		t.Fatal("expected policy")
	}
}

func TestCircuitBreaker(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)
	if !cb.Allow() {
		t.Fatal()
	}
	cb.Fail()
	cb.Fail()
	if cb.Allow() {
		t.Fatal("should be open")
	}
	time.Sleep(60 * time.Millisecond)
	if !cb.Allow() {
		t.Fatal("should half-open/allow")
	}
}

func TestPolicyPackLoad(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..")
	pack, err := LoadPolicyPack(filepath.Join(root, "policies", "cross_border.json"))
	if err != nil {
		t.Fatal(err)
	}
	if pack.Version == "" || pack.CrossBorder.VelocityMaxCount <= 0 {
		t.Fatalf("%+v", pack)
	}
}

func TestRunRuleEngineWhitelist(t *testing.T) {
	cfg := config.Config{}
	pack := packFromConfig(cfg)
	pack.CrossBorder.WhitelistKeys = []string{"ACME_CORP"}
	res := RunRuleEngine(context.Background(), tools.CrossBorderTransaction{
		TransactionID: "t1", Counterparty: "Acme Corp", Country: "US",
	}, pack, NewVelocityTracker(), store.Noop{}, false)
	if !res.EarlyExit || res.Decision != tools.DecisionApprove {
		t.Fatalf("%+v", res)
	}
}
