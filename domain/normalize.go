package domain

import (
	"regexp"
	"strings"
	"unicode"
)

var spaceRe = regexp.MustCompile(`\s+`)

// NormalizePartyName 规则化清洗（轻量任务：无模型、可并发）；生产可替换为专用 NLP 管道。
func NormalizePartyName(raw string, country string) *NormalizedParty {
	display := spaceRe.ReplaceAllString(strings.TrimSpace(raw), " ")
	var b strings.Builder
	for _, r := range display {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			b.WriteRune(unicode.ToUpper(r))
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		default:
			// 保留常见连接符，便于多语言名称对齐
			if r == '-' || r == '_' || r == '.' {
				b.WriteRune(r)
			}
		}
	}
	key := spaceRe.ReplaceAllString(strings.TrimSpace(b.String()), " ")
	key = strings.ReplaceAll(key, " ", "_")

	tokens := strings.Fields(strings.ReplaceAll(key, "_", " "))
	cn := strings.ToUpper(strings.TrimSpace(country))
	return &NormalizedParty{
		DisplayName:       display,
		NormalizedKey:     key,
		Tokens:            tokens,
		CountryNormalized: cn,
	}
}
