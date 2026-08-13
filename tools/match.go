package tools

import (
	"fmt"
	"strings"
	"unicode"
)

// 公司后缀（剥离后再做模糊匹配，降低 LLC/Ltd 噪声）。
var companySuffixes = []string{
	"LIMITED", "LTD", "LLC", "INCORPORATED", "INC", "CORPORATION", "CORP",
	"COMPANY", "CO", "PLC", "GMBH", "AG", "SA", "BV", "NV", "OY", "AB",
	"有限公司", "股份有限公司", "公司", "集团",
}

// StripCompanySuffix 去掉常见法人后缀，返回规范化键风格（空格→下划线前的大写串）。
func StripCompanySuffix(normalizedKey string) string {
	s := strings.ReplaceAll(normalizedKey, "_", " ")
	s = strings.TrimSpace(s)
	upper := strings.ToUpper(s)
	changed := true
	for changed {
		changed = false
		for _, suf := range companySuffixes {
			suf = strings.ToUpper(suf)
			if strings.HasSuffix(upper, " "+suf) {
				upper = strings.TrimSpace(upper[:len(upper)-len(suf)-1])
				changed = true
				break
			}
			if upper == suf {
				upper = ""
				changed = true
				break
			}
		}
	}
	upper = spaceRe.ReplaceAllString(upper, " ")
	return strings.ReplaceAll(strings.TrimSpace(upper), " ", "_")
}

// SimilarityRatio 基于编辑距离的相似度 [0,1]。
func SimilarityRatio(a, b string) float64 {
	a = strings.ToUpper(strings.TrimSpace(a))
	b = strings.ToUpper(strings.TrimSpace(b))
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	// 统一分隔符便于比较
	a = strings.ReplaceAll(a, "_", " ")
	b = strings.ReplaceAll(b, "_", " ")
	dist := levenshtein(a, b)
	maxLen := len([]rune(a))
	if lb := len([]rune(b)); lb > maxLen {
		maxLen = lb
	}
	if maxLen == 0 {
		return 0
	}
	return 1 - float64(dist)/float64(maxLen)
}

func levenshtein(a, b string) int {
	ra := []rune(a)
	rb := []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	cur := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			ins := cur[j-1] + 1
			del := prev[j] + 1
			sub := prev[j-1] + cost
			cur[j] = min(ins, del, sub)
		}
		prev, cur = cur, prev
	}
	return prev[lb]
}

// ScoreSanctionCandidate 对单条名单计算匹配分，并写回 MatchExplanation / MatchScore。
func ScoreSanctionCandidate(party *NormalizedParty, c *SanctionCandidate) float64 {
	if party == nil || c == nil {
		return 0
	}
	partyKey := party.NormalizedKey
	partyCore := StripCompanySuffix(partyKey)
	candKey := c.NameNormalized
	candCore := StripCompanySuffix(candKey)

	scores := []float64{
		SimilarityRatio(partyKey, candKey),
		SimilarityRatio(partyCore, candCore),
		SimilarityRatio(partyKey, candCore),
		SimilarityRatio(partyCore, candKey),
	}
	// 别名
	for _, al := range c.Aliases {
		alNorm := normalizeForMatch(al)
		scores = append(scores,
			SimilarityRatio(partyKey, alNorm),
			SimilarityRatio(partyCore, StripCompanySuffix(alNorm)),
		)
	}
	best := 0.0
	for _, s := range scores {
		if s > best {
			best = s
		}
	}
	// token 覆盖加成（粗召回后精排）
	if len(party.Tokens) > 0 {
		hit := 0
		candUpper := strings.ToUpper(c.NameNormalized + " " + strings.Join(c.Aliases, " "))
		for _, t := range party.Tokens {
			if len(t) < 2 {
				continue
			}
			if strings.Contains(candUpper, strings.ToUpper(t)) {
				hit++
			}
		}
		cover := float64(hit) / float64(len(party.Tokens))
		if cover > best {
			best = (best + cover) / 2
		} else {
			best = best*0.85 + cover*0.15
		}
	}
	if best > 1 {
		best = 1
	}
	c.MatchScore = best
	c.MatchExplanation = fmt.Sprintf("fuzzy=%.3f list=%s", best, c.ListCode)
	return best
}

// RankCandidates 就地按 MatchScore 降序，并裁剪低于 minScore 的项。
func RankCandidates(party *NormalizedParty, cands []SanctionCandidate, minScore float64, limit int) []SanctionCandidate {
	if limit <= 0 {
		limit = 16
	}
	out := make([]SanctionCandidate, 0, len(cands))
	for i := range cands {
		ScoreSanctionCandidate(party, &cands[i])
		if cands[i].MatchScore >= minScore {
			out = append(out, cands[i])
		}
	}
	// 简单插入排序（候选通常很少）
	for i := 1; i < len(out); i++ {
		j := i
		for j > 0 && out[j].MatchScore > out[j-1].MatchScore {
			out[j], out[j-1] = out[j-1], out[j]
			j--
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func normalizeForMatch(raw string) string {
	display := spaceRe.ReplaceAllString(strings.TrimSpace(raw), " ")
	var b strings.Builder
	for _, r := range display {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			b.WriteRune(unicode.ToUpper(r))
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		default:
			if r == '-' || r == '_' || r == '.' {
				b.WriteRune(r)
			}
		}
	}
	key := spaceRe.ReplaceAllString(strings.TrimSpace(b.String()), " ")
	return strings.ReplaceAll(key, " ", "_")
}
