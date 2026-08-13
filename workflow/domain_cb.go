package workflow

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"

	"risk_control/tools"
)

type crossBorderProfile struct{}

func (crossBorderProfile) ID() string { return tools.BusinessCrossBorder }

func (crossBorderProfile) PreAnalyze(pack *PolicyPack, req any) (PreAnalysis, error) {
	txn, err := asCB(req)
	if err != nil {
		return PreAnalysis{}, err
	}
	party := tools.NormalizePartyName(txn.Counterparty, txn.Country)
	return PreAnalyzeCrossBorder(txn, party, pack.CrossBorder, pack.Orchestrator), nil
}

func (crossBorderProfile) RunRules(ctx context.Context, e *RiskEngine, pack *PolicyPack, req any, pre PreAnalysis) EngineResult {
	txn, _ := asCB(req)
	withSanctions := pre.Bucket != tools.BucketFast
	return RunRuleEngine(ctx, txn, pack, e.velocity, e.store, withSanctions)
}

func (crossBorderProfile) RunGraph(e *RiskEngine, pack *PolicyPack, req any) EngineResult {
	txn, _ := asCB(req)
	party := tools.NormalizePartyName(txn.Counterparty, txn.Country)
	return RunGraphEngineCB(txn, party, e.graph, pack)
}

func (crossBorderProfile) RunLight(pack *PolicyPack, pre PreAnalysis, graphScore float64) EngineResult {
	return RunLightEngine(pre.Features, pack, graphScore)
}

func (crossBorderProfile) NeedDeep(pre PreAnalysis, rule EngineResult, arbDecision string) bool {
	if rule.EarlyExit {
		return false
	}
	if arbDecision == tools.DecisionReject {
		return false
	}
	if pre.Bucket == tools.BucketFast && arbDecision == tools.DecisionApprove {
		return false
	}
	if pre.Bucket == tools.BucketDeep || arbDecision == tools.DecisionReview {
		return true
	}
	return !rule.SkipAI
}

func (crossBorderProfile) InvokeDeep(ctx context.Context, e *RiskEngine, req any, opts []compose.Option) (tools.ScreeningResult, error) {
	txn, err := asCB(req)
	if err != nil {
		return tools.ScreeningResult{}, err
	}
	return e.cbGraph.Invoke(ctx, txn, opts...)
}

func (crossBorderProfile) ShadowLocal(ctx context.Context, e *RiskEngine, shadow *PolicyPack, req any) (string, string) {
	txn, _ := asCB(req)
	party := tools.NormalizePartyName(txn.Counterparty, txn.Country)
	pre := PreAnalyzeCrossBorder(txn, party, shadow.CrossBorder, shadow.Orchestrator)
	ruleRes := RunRuleEngine(ctx, txn, shadow, e.velocity, e.store, true)
	lightRes := RunLightEngine(pre.Features, shadow, 0)
	dec, _, _, _, detail := Arbitrate([]EngineResult{ruleRes, lightRes})
	return dec, detail
}

func (crossBorderProfile) SubjectLabel(req any) string {
	txn, _ := asCB(req)
	return txn.Counterparty
}

func (crossBorderProfile) TransactionID(req any) string {
	txn, _ := asCB(req)
	return txn.TransactionID
}

func asCB(req any) (tools.CrossBorderTransaction, error) {
	switch v := req.(type) {
	case tools.CrossBorderTransaction:
		return v, nil
	case *tools.CrossBorderTransaction:
		return *v, nil
	default:
		return tools.CrossBorderTransaction{}, fmt.Errorf("invalid cross_border payload %T", req)
	}
}
