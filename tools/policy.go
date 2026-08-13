package tools

// 对外稳定决策码：上游支付系统只认这些值，不认自由文本。
const (
	DecisionApprove = "APPROVE"
	DecisionReview  = "REVIEW"
	DecisionReject  = "REJECT"
)

// 策略 ID（写入审计与 ScreeningResult.PolicyIDs，便于合规回溯）。
const (
	PolicyWhitelistPass     = "POLICY_WHITELIST_PASS"
	PolicyBlacklistHit      = "POLICY_BLACKLIST_HIT"
	PolicyCountryBlock      = "POLICY_COUNTRY_BLOCK"
	PolicyCountryHighRisk   = "POLICY_COUNTRY_HIGH_RISK"
	PolicyAmountLimit       = "POLICY_AMOUNT_LIMIT"
	PolicyVelocity          = "POLICY_VELOCITY"
	PolicyFuzzyAutoReject   = "POLICY_FUZZY_AUTO_REJECT"
	PolicyNoCandidateClear  = "POLICY_NO_CANDIDATE_CLEAR"
	PolicyLowMatchClear     = "POLICY_LOW_MATCH_CLEAR"
	PolicySanctionsAI       = "POLICY_SANCTIONS_AI"
	PolicyCacheHit          = "POLICY_CACHE_HIT"
	PolicyManualCaseResolve = "POLICY_MANUAL_CASE_RESOLVE"
	PolicyLightML           = "POLICY_LIGHT_ML"
	PolicyEntityGraph       = "POLICY_ENTITY_GRAPH"
	PolicyDeepTimeout       = "POLICY_DEEP_TIMEOUT"
	PolicyCircuitOpen       = "POLICY_CIRCUIT_OPEN"
	PolicyPreAnalyze        = "POLICY_PRE_ANALYZE"
	PolicyShadowDiff        = "POLICY_SHADOW_DIFF"
)

// DecisionFromScore 将风险分映射为决策码（无本地硬规则时的默认映射）。
func DecisionFromScore(score float64) string {
	if score >= 0.65 {
		return DecisionReject
	}
	if score >= 0.35 {
		return DecisionReview
	}
	return DecisionApprove
}

// LevelFromScore 风险等级。
func LevelFromScore(score float64) string {
	if score >= 0.65 {
		return "HIGH"
	}
	if score >= 0.35 {
		return "MEDIUM"
	}
	return "LOW"
}
