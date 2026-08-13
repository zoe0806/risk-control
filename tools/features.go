package tools

// RouteBucket 预分析路由分桶。
const (
	BucketFast  = "fast"
	BucketLight = "light"
	BucketDeep  = "deep"
)

// EngineName 引擎标识。
const (
	EngineRule  = "rule"
	EngineLight = "light_ml"
	EngineGraph = "entity_graph"
	EngineDeep  = "deep"
)

// FeatureVector 预分析 / 轻量模型特征（阶段3/4）。
type FeatureVector struct {
	AmountNorm       float64 `json:"amount_norm"` // 相对限额归一
	CountryBlocked   bool    `json:"country_blocked"`
	CountryHighRisk  bool    `json:"country_high_risk"`
	HasAccount       bool    `json:"has_account"`
	HasDevice        bool    `json:"has_device"`
	NameTokenCount   int     `json:"name_token_count"`
	NameLen          int     `json:"name_len"`
	PurposeLen       int     `json:"purpose_len"`
	PartyKey         string  `json:"party_key,omitempty"`
	Country          string  `json:"country,omitempty"`
}
