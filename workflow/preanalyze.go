package workflow

import (
	"math"
	"strings"

	"risk_control/config"
	"risk_control/tools"
)

// PreAnalysis 预分析结果。
type PreAnalysis struct {
	Bucket   string              `json:"bucket"`
	Features tools.FeatureVector `json:"features"`
	Reasons  []string            `json:"reasons"`
}

// PreAnalyzeCrossBorder 跨境预分析。
func PreAnalyzeCrossBorder(txn tools.CrossBorderTransaction, party *tools.NormalizedParty, rules config.CrossBorderRules, orch config.OrchestratorConfig) PreAnalysis {
	_ = orch
	if party == nil {
		party = tools.NormalizePartyName(txn.Counterparty, txn.Country)
	}
	fv := tools.FeatureVector{
		HasAccount:     txn.AccountID != "",
		HasDevice:      txn.DeviceID != "",
		TextLen:        len(txn.PaymentPurpose),
		PurposeLen:     len(txn.PaymentPurpose),
		SubjectKey:     party.NormalizedKey,
		PartyKey:       party.NormalizedKey,
		Attr:           party.CountryNormalized,
		Country:        party.CountryNormalized,
		Cardinality:    len(party.Tokens),
		NameTokenCount: len(party.Tokens),
		NameLen:        len(party.NormalizedKey),
	}
	if rules.MaxAmountMinorUnit > 0 {
		fv.AmountNorm = float64(txn.AmountMinorUnit) / float64(rules.MaxAmountMinorUnit)
		if fv.AmountNorm > 1 {
			fv.AmountNorm = 1
		}
	}
	fv.BlockFlag = setInList(rules.BlockedCountries, fv.Country)
	fv.CountryBlocked = fv.BlockFlag
	fv.HighRiskFlag = setInList(rules.HighRiskCountries, fv.Country)
	fv.CountryHighRisk = fv.HighRiskFlag
	fv.NormalizeFeatures()

	pa := PreAnalysis{Features: fv, Bucket: tools.BucketLight}
	if setInList(rules.WhitelistKeys, fv.PartyKey) {
		pa.Bucket = tools.BucketFast
		pa.Reasons = append(pa.Reasons, "whitelist_candidate")
		return pa
	}
	if fv.BlockFlag || setInList(rules.BlacklistKeys, fv.PartyKey) {
		pa.Bucket = tools.BucketFast
		pa.Reasons = append(pa.Reasons, "hard_rule_candidate")
		return pa
	}
	if fv.HighRiskFlag || fv.AmountNorm >= 0.6 {
		pa.Bucket = tools.BucketDeep
		pa.Reasons = append(pa.Reasons, "elevated_risk_features")
		return pa
	}
	if fv.AmountNorm >= 0.25 || fv.Cardinality >= 4 {
		pa.Bucket = tools.BucketLight
		pa.Reasons = append(pa.Reasons, "moderate_features")
		return pa
	}
	pa.Bucket = tools.BucketFast
	pa.Reasons = append(pa.Reasons, "low_feature_risk")
	return pa
}

// PreAnalyzeStock 股票预分析。
func PreAnalyzeStock(order tools.StockOrder, norm *tools.NormalizedStockOrder, rules config.StockRules, orch config.OrchestratorConfig) PreAnalysis {
	_ = orch
	if norm == nil {
		norm, _ = tools.NormalizeStockOrder(order)
	}
	notional := order.Price * float64(order.Quantity)
	fv := tools.FeatureVector{
		HasAccount:   order.AccountID != "",
		SubjectKey:   norm.SymbolKey,
		Cardinality:  len(norm.SymbolKey),
		TextLen:      len(order.NewsSummary) + len(order.DisciplineRules),
		HighRiskFlag: order.Flags.BeforeEarnings || setInList(rules.WatchlistSymbols, norm.SymbolKey),
		BlockFlag:    setInList(rules.AbsoluteBanSymbols, norm.SymbolKey) || strings.Contains(norm.SymbolKey, "ST"),
	}
	if rules.MaxNotional > 0 {
		fv.AmountNorm = notional / rules.MaxNotional
		if fv.AmountNorm > 1 {
			fv.AmountNorm = 1
		}
	} else if notional > 0 {
		// 无阈值时用对数压缩到 (0,1)
		fv.AmountNorm = 1 - 1/(1+notional/100000)
	}
	fv.CountryHighRisk = fv.HighRiskFlag
	fv.CountryBlocked = fv.BlockFlag
	fv.PurposeLen = fv.TextLen
	fv.NormalizeFeatures()

	pa := PreAnalysis{Features: fv, Bucket: tools.BucketLight}
	if setInList(rules.TrustedAccounts, order.AccountID) && !fv.BlockFlag {
		pa.Bucket = tools.BucketFast
		pa.Reasons = append(pa.Reasons, "trusted_account")
		return pa
	}
	if fv.BlockFlag {
		pa.Bucket = tools.BucketFast
		pa.Reasons = append(pa.Reasons, "hard_ban_candidate")
		return pa
	}
	if fv.HighRiskFlag || fv.AmountNorm >= 0.6 || len(order.NewsSummary) > 80 {
		pa.Bucket = tools.BucketDeep
		pa.Reasons = append(pa.Reasons, "elevated_stock_risk")
		return pa
	}
	if fv.AmountNorm >= 0.25 || len(order.DisciplineRules) > 0 {
		pa.Bucket = tools.BucketLight
		pa.Reasons = append(pa.Reasons, "moderate_stock")
		return pa
	}
	pa.Bucket = tools.BucketFast
	pa.Reasons = append(pa.Reasons, "low_stock_risk")
	return pa
}

// PreAnalyze 兼容旧名（跨境）。
func PreAnalyze(txn tools.CrossBorderTransaction, party *tools.NormalizedParty, rules config.CrossBorderRules, orch config.OrchestratorConfig) PreAnalysis {
	return PreAnalyzeCrossBorder(txn, party, rules, orch)
}

// ScoreLightML sigmoid(w·x)。
func ScoreLightML(fv tools.FeatureVector, w config.LightMLWeights, graphScore float64) float64 {
	fv.NormalizeFeatures()
	z := w.Bias
	z += w.Amount * fv.AmountNorm
	if fv.HighRiskFlag || fv.CountryHighRisk {
		z += w.CountryHighRisk
	}
	if fv.BlockFlag || fv.CountryBlocked {
		z += w.CountryBlocked
	}
	card := fv.Cardinality
	if card == 0 {
		card = fv.NameTokenCount
	}
	z += w.NameTokens * float64(card)
	text := fv.TextLen
	if text == 0 {
		text = fv.PurposeLen
	}
	z += w.PurposeLen * float64(text)
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
