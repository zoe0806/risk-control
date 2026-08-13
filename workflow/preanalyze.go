package workflow

import (
	"math"
	"strings"

	"risk_control/config"
	"risk_control/tools"
)

// PreAnalysis 预分析结果。
type PreAnalysis struct {
	Bucket   string             `json:"bucket"`
	Features tools.FeatureVector `json:"features"`
	Reasons  []string           `json:"reasons"`
}

// PreAnalyze 毫秒级特征提取与路由分桶（阶段3）。
func PreAnalyze(txn tools.CrossBorderTransaction, party *tools.NormalizedParty, rules config.CrossBorderRules, orch config.OrchestratorConfig) PreAnalysis {
	fv := tools.FeatureVector{
		HasAccount: txn.AccountID != "",
		HasDevice:  txn.DeviceID != "",
		PurposeLen: len(txn.PaymentPurpose),
		Country:    "",
		PartyKey:   "",
	}
	if party != nil {
		fv.PartyKey = party.NormalizedKey
		fv.Country = party.CountryNormalized
		fv.NameTokenCount = len(party.Tokens)
		fv.NameLen = len(party.NormalizedKey)
	} else {
		p := tools.NormalizePartyName(txn.Counterparty, txn.Country)
		fv.PartyKey = p.NormalizedKey
		fv.Country = p.CountryNormalized
		fv.NameTokenCount = len(p.Tokens)
		fv.NameLen = len(p.NormalizedKey)
		party = p
	}
	if rules.MaxAmountMinorUnit > 0 {
		fv.AmountNorm = float64(txn.AmountMinorUnit) / float64(rules.MaxAmountMinorUnit)
		if fv.AmountNorm > 1 {
			fv.AmountNorm = 1
		}
	}
	fv.CountryBlocked = setInList(rules.BlockedCountries, fv.Country)
	fv.CountryHighRisk = setInList(rules.HighRiskCountries, fv.Country)

	pa := PreAnalysis{Features: fv, Bucket: tools.BucketLight}
	// 白名单倾向 fast
	if setInList(rules.WhitelistKeys, fv.PartyKey) {
		pa.Bucket = tools.BucketFast
		pa.Reasons = append(pa.Reasons, "whitelist_candidate")
		return pa
	}
	if fv.CountryBlocked || setInList(rules.BlacklistKeys, fv.PartyKey) {
		pa.Bucket = tools.BucketFast // 本地硬规则即可
		pa.Reasons = append(pa.Reasons, "hard_rule_candidate")
		return pa
	}
	if fv.CountryHighRisk || fv.AmountNorm >= 0.6 || strings.TrimSpace(txn.DeviceID) == "" && txn.AmountMinorUnit > 0 {
		pa.Bucket = tools.BucketDeep
		pa.Reasons = append(pa.Reasons, "elevated_risk_features")
		return pa
	}
	if fv.AmountNorm >= 0.25 || fv.NameTokenCount >= 4 {
		pa.Bucket = tools.BucketLight
		pa.Reasons = append(pa.Reasons, "moderate_features")
		return pa
	}
	pa.Bucket = tools.BucketFast
	pa.Reasons = append(pa.Reasons, "low_feature_risk")
	_ = orch
	return pa
}

// ScoreLightML sigmoid(w·x) 异常分。
func ScoreLightML(fv tools.FeatureVector, w config.LightMLWeights, graphScore float64) float64 {
	z := w.Bias
	z += w.Amount * fv.AmountNorm
	if fv.CountryHighRisk {
		z += w.CountryHighRisk
	}
	if fv.CountryBlocked {
		z += w.CountryBlocked
	}
	z += w.NameTokens * float64(fv.NameTokenCount)
	z += w.PurposeLen * float64(fv.PurposeLen)
	z += w.GraphCluster * graphScore
	return sigmoid(z)
}

func sigmoid(z float64) float64 {
	if z > 20 {
		return 1
	}
	if z < -20 {
		return 0
	}
	return 1 / (1 + math.Exp(-z))
}
