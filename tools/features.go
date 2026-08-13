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

// FeatureVector 通用风险特征（各业务域映射到同一向量，供轻量模型使用）。
// 语义约定（跨域复用权重字段）：
//   AmountNorm     — 金额/名义本金相对阈值 [0,1]
//   HighRiskFlag   — 高风险走廊 / 财报窗口 / 强制复核候选
//   BlockFlag      — 禁运国 / 绝对禁买等硬信号
//   Cardinality    — 名称 token 数 / 标的复杂度等
//   TextLen        — 用途/舆情/纪律文本长度
//   SubjectKey     — 对手方键或标的代码
type FeatureVector struct {
	AmountNorm    float64 `json:"amount_norm"`
	HighRiskFlag  bool    `json:"high_risk_flag"`
	BlockFlag     bool    `json:"block_flag"`
	HasAccount    bool    `json:"has_account"`
	HasDevice     bool    `json:"has_device"`
	Cardinality   int     `json:"cardinality"`
	TextLen       int     `json:"text_len"`
	SubjectKey    string  `json:"subject_key,omitempty"`
	Attr          string  `json:"attr,omitempty"` // 国家 ISO2 等附属属性

	// 兼容旧字段名（跨境 JSON / 测试）
	CountryBlocked  bool   `json:"country_blocked,omitempty"`
	CountryHighRisk bool   `json:"country_high_risk,omitempty"`
	NameTokenCount  int    `json:"name_token_count,omitempty"`
	NameLen         int    `json:"name_len,omitempty"`
	PurposeLen      int    `json:"purpose_len,omitempty"`
	PartyKey        string `json:"party_key,omitempty"`
	Country         string `json:"country,omitempty"`
}

// NormalizeFeatures 将兼容字段折叠到通用字段。
func (f *FeatureVector) NormalizeFeatures() {
	if f == nil {
		return
	}
	if f.BlockFlag || f.CountryBlocked {
		f.BlockFlag = true
		f.CountryBlocked = true
	}
	if f.HighRiskFlag || f.CountryHighRisk {
		f.HighRiskFlag = true
		f.CountryHighRisk = true
	}
	if f.Cardinality == 0 && f.NameTokenCount > 0 {
		f.Cardinality = f.NameTokenCount
	}
	if f.TextLen == 0 && f.PurposeLen > 0 {
		f.TextLen = f.PurposeLen
	}
	if f.SubjectKey == "" && f.PartyKey != "" {
		f.SubjectKey = f.PartyKey
	}
	if f.Attr == "" && f.Country != "" {
		f.Attr = f.Country
	}
}
