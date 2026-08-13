package workflow

import (
	"fmt"
	"strings"
	"time"

	"risk_control/config"
	"risk_control/tools"
)

func ensureCBGate(st *tools.PipelineState) *tools.CBLocalGate {
	if st.Gate == nil {
		st.Gate = &tools.CBLocalGate{}
	}
	return st.Gate
}

func appendPolicy(st *tools.PipelineState, id string) {
	for _, p := range st.PolicyIDs {
		if p == id {
			return
		}
	}
	st.PolicyIDs = append(st.PolicyIDs, id)
}

func setInList(list []string, key string) bool {
	key = strings.ToUpper(strings.TrimSpace(key))
	if key == "" {
		return false
	}
	for _, x := range list {
		if strings.ToUpper(strings.TrimSpace(x)) == key {
			return true
		}
	}
	return false
}

// ApplyFastFilter 白/黑名单、国家走廊、金额、频次——确定性闸门。
func ApplyFastFilter(st *tools.PipelineState, rules config.CrossBorderRules, vel *VelocityTracker) {
	g := ensureCBGate(st)
	partyKey := ""
	country := ""
	if st.Party != nil {
		partyKey = st.Party.NormalizedKey
		country = st.Party.CountryNormalized
	}
	txn := st.Transaction

	if setInList(rules.WhitelistKeys, partyKey) {
		g.WhitelistPass = true
		g.EarlyExit = true
		g.LocalRiskScore = 0.05
		g.Hits = append(g.Hits, tools.CBGateHit{PolicyID: tools.PolicyWhitelistPass, Detail: "对手方在白名单"})
		appendPolicy(st, tools.PolicyWhitelistPass)
		return
	}
	if setInList(rules.BlacklistKeys, partyKey) {
		g.HardBlock = true
		g.EarlyExit = true
		g.BlockReason = tools.PolicyBlacklistHit
		g.LocalRiskScore = 1.0
		g.Hits = append(g.Hits, tools.CBGateHit{PolicyID: tools.PolicyBlacklistHit, Detail: "对手方在黑名单"})
		appendPolicy(st, tools.PolicyBlacklistHit)
		return
	}
	if setInList(rules.BlockedCountries, country) {
		g.HardBlock = true
		g.EarlyExit = true
		g.BlockReason = tools.PolicyCountryBlock
		g.LocalRiskScore = 1.0
		g.Hits = append(g.Hits, tools.CBGateHit{PolicyID: tools.PolicyCountryBlock, Detail: "禁运/阻断国家: " + country})
		appendPolicy(st, tools.PolicyCountryBlock)
		return
	}
	if setInList(rules.HighRiskCountries, country) {
		g.ForceAI = true
		g.LocalRiskScore = 0.4
		g.Hits = append(g.Hits, tools.CBGateHit{PolicyID: tools.PolicyCountryHighRisk, Detail: "高风险走廊: " + country})
		appendPolicy(st, tools.PolicyCountryHighRisk)
	}
	if rules.MaxAmountMinorUnit > 0 && txn.AmountMinorUnit > rules.MaxAmountMinorUnit {
		g.HardBlock = true
		g.EarlyExit = true
		g.BlockReason = tools.PolicyAmountLimit
		g.LocalRiskScore = 0.95
		g.Hits = append(g.Hits, tools.CBGateHit{
			PolicyID: tools.PolicyAmountLimit,
			Detail:   fmt.Sprintf("金额超限 %d > %d", txn.AmountMinorUnit, rules.MaxAmountMinorUnit),
		})
		appendPolicy(st, tools.PolicyAmountLimit)
		return
	}

	// 频次：账户 / 设备 / IP+对手
	window := timeDurationSec(rules.VelocityWindowSec)
	maxN := rules.VelocityMaxCount
	keys := velocityKeys(txn, partyKey)
	for _, k := range keys {
		n := vel.Hit(k, window)
		if n > maxN {
			g.HardBlock = true
			g.EarlyExit = true
			g.BlockReason = tools.PolicyVelocity
			g.LocalRiskScore = 0.9
			g.Hits = append(g.Hits, tools.CBGateHit{
				PolicyID: tools.PolicyVelocity,
				Detail:   fmt.Sprintf("频次超限 key=%s count=%d/%d", k, n, maxN),
			})
			appendPolicy(st, tools.PolicyVelocity)
			return
		}
	}
}

func timeDurationSec(sec int) time.Duration {
	if sec <= 0 {
		sec = 60
	}
	return time.Duration(sec) * time.Second
}

func velocityKeys(txn tools.CrossBorderTransaction, partyKey string) []string {
	var keys []string
	if txn.AccountID != "" {
		keys = append(keys, "acct:"+strings.ToLower(txn.AccountID))
	}
	if txn.DeviceID != "" {
		keys = append(keys, "dev:"+strings.ToLower(txn.DeviceID))
	}
	if txn.ClientIP != "" && partyKey != "" {
		keys = append(keys, "ip_party:"+txn.ClientIP+":"+partyKey)
	}
	return keys
}

// ApplyMatchRouting 根据模糊匹配分决定 SkipAI / AutoReject。
func ApplyMatchRouting(st *tools.PipelineState, rules config.CrossBorderRules) {
	g := ensureCBGate(st)
	best := 0.0
	for _, c := range st.Candidates {
		if c.MatchScore > best {
			best = c.MatchScore
		}
	}
	g.BestMatchScore = best

	if best >= rules.AutoRejectAboveScore {
		g.AutoReject = true
		g.SkipAI = true
		g.HardBlock = true
		g.BlockReason = tools.PolicyFuzzyAutoReject
		g.LocalRiskScore = best
		g.Hits = append(g.Hits, tools.CBGateHit{
			PolicyID: tools.PolicyFuzzyAutoReject,
			Detail:   fmt.Sprintf("名单模糊匹配过高 %.3f", best),
		})
		appendPolicy(st, tools.PolicyFuzzyAutoReject)
		return
	}

	// 有足够匹配分 → 进入 AI Top-K 核对（含高风险走廊 ForceAI）
	if best >= rules.SkipAIBelowScore && len(st.Candidates) > 0 {
		g.SkipAI = false
		return
	}

	// 无候选/低分：跳过 AI
	g.SkipAI = true
	if len(st.Candidates) == 0 {
		appendPolicy(st, tools.PolicyNoCandidateClear)
		g.Hits = append(g.Hits, tools.CBGateHit{PolicyID: tools.PolicyNoCandidateClear, Detail: "无名单候选"})
	} else {
		appendPolicy(st, tools.PolicyLowMatchClear)
		g.Hits = append(g.Hits, tools.CBGateHit{
			PolicyID: tools.PolicyLowMatchClear,
			Detail:   fmt.Sprintf("最佳匹配过低 %.3f", best),
		})
	}
	if g.LocalRiskScore < 0.1 {
		g.LocalRiskScore = 0.08
	}
}

// ApplyRuleDecision 无 LLM 路径的裁决与报告。
func ApplyRuleDecision(st *tools.PipelineState) {
	g := ensureCBGate(st)
	st.SkippedAI = true

	switch {
	case g.WhitelistPass:
		st.Decision = tools.DecisionApprove
		score := g.LocalRiskScore
		st.Primary = &tools.PrimaryAssessment{
			RiskScore:            score,
			Rationale:            "白名单快速放行",
			NeedsSecondaryReview: false,
		}
		st.Secondary = &tools.SecondaryAssessment{Skipped: true, FinalRiskScore: score, Rationale: "本地规则路径"}
		st.ReportMarkdown = "## 本地裁决\n- **决策**: APPROVE\n- **原因**: 白名单\n"
	case g.HardBlock || g.AutoReject:
		st.Decision = tools.DecisionReject
		score := g.LocalRiskScore
		if score < 0.65 {
			score = 0.95
		}
		names := matchedNames(st)
		st.Primary = &tools.PrimaryAssessment{
			RiskScore:            score,
			MatchedNames:         names,
			Rationale:            "本地硬规则拦截: " + g.BlockReason,
			NeedsSecondaryReview: false,
		}
		st.Secondary = &tools.SecondaryAssessment{Skipped: true, FinalRiskScore: score, Rationale: "本地规则路径"}
		st.ReportMarkdown = fmt.Sprintf("## 本地裁决\n- **决策**: REJECT\n- **策略**: %s\n- **说明**: 确定性规则拦截，未调用大模型。\n", g.BlockReason)
	default:
		// 低匹配/无候选放行；高风险国家已 ForceAI 不会进此分支默认放行
		score := g.LocalRiskScore
		if score <= 0 {
			score = 0.1
		}
		// 高风险走廊但匹配不足：进 REVIEW 而非直接放行
		needReview := false
		for _, h := range g.Hits {
			if h.PolicyID == tools.PolicyCountryHighRisk {
				needReview = true
				break
			}
		}
		if needReview {
			st.Decision = tools.DecisionReview
			if score < 0.4 {
				score = 0.4
			}
			st.Primary = &tools.PrimaryAssessment{
				RiskScore:            score,
				Rationale:            "高风险走廊且无强名单命中，转人工复核",
				NeedsSecondaryReview: true,
			}
		} else {
			st.Decision = tools.DecisionApprove
			st.Primary = &tools.PrimaryAssessment{
				RiskScore:            score,
				Rationale:            "本地名单无有效命中，跳过 AI 放行",
				NeedsSecondaryReview: false,
			}
		}
		st.Secondary = &tools.SecondaryAssessment{Skipped: true, FinalRiskScore: score, Rationale: "本地规则路径"}
		st.ReportMarkdown = fmt.Sprintf("## 本地裁决\n- **决策**: %s\n- **最佳匹配分**: %.3f\n- **跳过 AI**: true\n", st.Decision, g.BestMatchScore)
	}
}

func matchedNames(st *tools.PipelineState) []string {
	var names []string
	for _, c := range st.Candidates {
		if c.MatchScore >= 0.7 {
			names = append(names, c.NameOriginal)
		}
	}
	return names
}

// ApplyAIDecisionCodes 将 AI 路径结果映射为稳定决策码。
func ApplyAIDecisionCodes(st *tools.PipelineState) {
	appendPolicy(st, tools.PolicySanctionsAI)
	score := 0.0
	if st.Primary != nil {
		score = st.Primary.RiskScore
	}
	if st.Secondary != nil && !st.Secondary.Skipped && !st.Secondary.TechnicalDegraded {
		score = st.Secondary.FinalRiskScore
		if st.Secondary.Confirmed && score < 0.65 {
			score = 0.7
		}
	}
	st.Decision = tools.DecisionFromScore(score)
	st.SkippedAI = false
}
