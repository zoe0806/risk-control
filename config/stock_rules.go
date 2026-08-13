package config

// StockRules 股票域本地规则（策略包可热更新）。
type StockRules struct {
	TrustedAccounts    []string `json:"trustedAccounts"`
	AbsoluteBanSymbols []string `json:"absoluteBanSymbols"`
	WatchlistSymbols   []string `json:"watchlistSymbols"`
	VelocityWindowSec  int      `json:"velocityWindowSec"`
	VelocityMaxCount   int      `json:"velocityMaxCount"`
	MaxNotional        float64  `json:"maxNotional"` // price*qty，0=不启用
}

// StockRulesOrDefault 补默认演示清单。
func (c Config) StockRulesOrDefault(r StockRules) StockRules {
	if len(r.AbsoluteBanSymbols) == 0 {
		r.AbsoluteBanSymbols = []string{"300136"}
	}
	if len(r.WatchlistSymbols) == 0 {
		r.WatchlistSymbols = []string{"300346"}
	}
	if r.VelocityWindowSec <= 0 {
		r.VelocityWindowSec = 60
	}
	if r.VelocityMaxCount <= 0 {
		r.VelocityMaxCount = 30
	}
	return r
}

// DomainPolicyPaths 单业务策略路径。
type DomainPolicyPaths struct {
	Primary string `json:"primary"`
	Shadow  string `json:"shadow"`
}
