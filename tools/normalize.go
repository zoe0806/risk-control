package tools

import (
	"regexp"
	"strings"
	"time"
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

// NormalizeStockOrder 清洗：代码、方向、时间戳等。
func NormalizeStockOrder(in StockOrder) (*NormalizedStockOrder, error) {
	raw := strings.TrimSpace(strings.ToUpper(in.Symbol))
	raw = strings.ReplaceAll(raw, " ", "")
	parts := strings.Split(raw, ".")
	base := parts[0]
	mkt := "UNKNOWN"
	if len(parts) > 1 {
		switch parts[1] {
		case "SH", "SSE":
			mkt = "SH"
		case "SZ", "SZSE":
			mkt = "SZ"
		}
	}
	if mkt == "UNKNOWN" && len(base) >= 1 {
		switch base[0] {
		case '6':
			mkt = "SH"
		case '0', '3':
			mkt = "SZ"
		}
	}
	side := strings.ToUpper(strings.TrimSpace(in.Side))
	if side != "BUY" && side != "SELL" {
		side = "UNKNOWN"
	}
	ts := in.Timestamp
	if ts <= 0 {
		ts = time.Now().UnixMilli()
	}
	return &NormalizedStockOrder{
		SymbolKey:   base,
		Market:      mkt,
		SideNorm:    side,
		Quantity:    in.Quantity,
		Price:       in.Price,
		TimestampMs: ts,
	}, nil
}
