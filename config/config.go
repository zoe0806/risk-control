package config

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Config 演示环境变量。
type Config struct {
	HTTPAddr string `json:"httpaddr"`

	MySQLDSN string `json:"mysqldsn"`

	DeepSeekAPIKey        string        `json:"deepSeekAPIKey"`
	DeepSeekBaseURL       string        `json:"deepSeekBaseURL"`
	ModelPrimary          string        `json:"modelPrimary"`
	ModelVerify           string        `json:"modelVerify"`
	ModelReport           string        `json:"modelReport"`
	LLMTimeout            time.Duration `json:"llmTimeout"`
	SysPrompt             string        `json:"sysPrompt"`
	UserPrompt            string        `json:"userPrompt"`
	VerifyPrompt          string        `json:"verifyPrompt"`
	ReportPrompt          string        `json:"reportPrompt"`
	PrimaryRiskScore      float64       `json:"primaryRiskScore"`
	PrimaryStockRiskScore float64       `json:"primaryStockRiskScore"`
	Workers               int           `json:"workers"`
	StockSysPrompt        string        `json:"stockSysPrompt"`
	StockUserPrompt       string        `json:"stockUserPrompt"`
	StockReportPrompt     string        `json:"stockReportPrompt"`
	StockVerifyPrompt     string        `json:"stockVerifyPrompt"`

	// CrossBorder 阶段1/2 本地规则与匹配阈值（可缺省，见 CBRules）。
	CrossBorder CrossBorderRules `json:"crossBorder"`

	// Orchestrator 阶段3/4 多引擎编排（也可被 policies 热更新覆盖）。
	Orchestrator   OrchestratorConfig `json:"orchestrator"`
	PolicyPackPath string             `json:"policyPackPath"` // 兼容：默认跨境主包
	ShadowPackPath string             `json:"shadowPackPath"`
	// DomainPolicies 按业务线配置主/影子策略包（优先于上面的兼容字段）。
	DomainPolicies map[string]DomainPolicyPaths `json:"domainPolicies"`

	// DeepRuntime 深度执行器：native | eino | cli | codex | off。不会自动发现本机 Codex，需把 kind 设为 codex。
	DeepRuntime DeepRuntimeConfig `json:"deepRuntime"`
}

// DeepRuntimeConfig 可插拔深度 runtime。kind=cli 须遵守 risk.deep.v1；kind=codex 调用本机 `codex exec`。
type DeepRuntimeConfig struct {
	Kind string        `json:"kind"` // native | eino | cli | codex | off
	CLI  DeepCLIConfig `json:"cli"`
}

// DeepCLIConfig 外部 CLI。kind=cli 时 command 必填；kind=codex 时 command 默认 "codex"。
type DeepCLIConfig struct {
	Command   string   `json:"command"`
	Args      []string `json:"args"`
	TimeoutMs int      `json:"timeoutMs"`
}

// OrchestratorConfig 预分析路由 / 深度超时 / 熔断 / 影子。
type OrchestratorConfig struct {
	DeepTimeoutMs           int     `json:"deepTimeoutMs"`
	CircuitFailureThreshold int     `json:"circuitFailureThreshold"`
	CircuitOpenSec          int     `json:"circuitOpenSec"`
	ShadowEnabled           bool    `json:"shadowEnabled"`
	AsyncCaseDraft          bool    `json:"asyncCaseDraft"`
	LightReviewThreshold    float64 `json:"lightReviewThreshold"`
	LightRejectThreshold    float64 `json:"lightRejectThreshold"`
	GraphSharedDeviceReview int     `json:"graphSharedDeviceReview"`
	GraphClusterReject      int     `json:"graphClusterReject"`
}

// LightMLWeights 轻量线性模型权重（演示用，可热更新）。
type LightMLWeights struct {
	Bias            float64 `json:"bias"`
	Amount          float64 `json:"amount"`
	CountryHighRisk float64 `json:"countryHighRisk"`
	CountryBlocked  float64 `json:"countryBlocked"`
	NameTokens      float64 `json:"nameTokens"`
	PurposeLen      float64 `json:"purposeLen"`
	GraphCluster    float64 `json:"graphCluster"`
}

// Orch 返回带默认值的编排配置。
func (c Config) Orch() OrchestratorConfig {
	o := c.Orchestrator
	if o.DeepTimeoutMs <= 0 {
		o.DeepTimeoutMs = 12000
	}
	if o.CircuitFailureThreshold <= 0 {
		o.CircuitFailureThreshold = 5
	}
	if o.CircuitOpenSec <= 0 {
		o.CircuitOpenSec = 30
	}
	if o.LightReviewThreshold <= 0 {
		o.LightReviewThreshold = 0.40
	}
	if o.LightRejectThreshold <= 0 {
		o.LightRejectThreshold = 0.78
	}
	if o.GraphSharedDeviceReview <= 0 {
		o.GraphSharedDeviceReview = 3
	}
	if o.GraphClusterReject <= 0 {
		o.GraphClusterReject = 10
	}
	return o
}

// DefaultLightWeights 默认轻量模型权重。
func DefaultLightWeights() LightMLWeights {
	return LightMLWeights{
		Bias:            -1.2,
		Amount:          1.4,
		CountryHighRisk: 0.9,
		CountryBlocked:  3.0,
		NameTokens:      0.05,
		PurposeLen:      0.002,
		GraphCluster:    0.8,
	}
}

// CrossBorderRules 跨境本地漏斗与名单精排参数。
type CrossBorderRules struct {
	WhitelistKeys        []string `json:"whitelistKeys"`
	BlacklistKeys        []string `json:"blacklistKeys"`
	BlockedCountries     []string `json:"blockedCountries"`
	HighRiskCountries    []string `json:"highRiskCountries"`
	MaxAmountMinorUnit   int64    `json:"maxAmountMinorUnit"` // 0=不启用金额硬限
	VelocityWindowSec    int      `json:"velocityWindowSec"`
	VelocityMaxCount     int      `json:"velocityMaxCount"`
	FuzzyMatchMinScore   float64  `json:"fuzzyMatchMinScore"`   // 候选保留下限
	SkipAIBelowScore     float64  `json:"skipAIBelowScore"`     // 最佳匹配低于此则跳过 LLM 放行
	AutoRejectAboveScore float64  `json:"autoRejectAboveScore"` // 最佳匹配高于此则本地 REJECT
	CacheTTLSec          int      `json:"cacheTTLSec"`
	CandidateLimit       int      `json:"candidateLimit"`
}

// CBRules 返回带默认值的跨境规则副本。
func (c Config) CBRules() CrossBorderRules {
	r := c.CrossBorder
	if r.FuzzyMatchMinScore <= 0 {
		r.FuzzyMatchMinScore = 0.55
	}
	if r.SkipAIBelowScore <= 0 {
		r.SkipAIBelowScore = 0.45
	}
	if r.AutoRejectAboveScore <= 0 {
		r.AutoRejectAboveScore = 0.92
	}
	if r.VelocityWindowSec <= 0 {
		r.VelocityWindowSec = 60
	}
	if r.VelocityMaxCount <= 0 {
		r.VelocityMaxCount = 20
	}
	if r.CacheTTLSec <= 0 {
		r.CacheTTLSec = 300
	}
	if r.CandidateLimit <= 0 {
		r.CandidateLimit = 16
	}
	if len(r.BlockedCountries) == 0 {
		r.BlockedCountries = []string{"KP", "IR", "SY", "CU"}
	}
	if len(r.HighRiskCountries) == 0 {
		r.HighRiskCountries = []string{"RU", "BY"}
	}
	return r
}

var config Config
var once sync.Once

func init() {
	once.Do(func() {
		cfgPath := findConfigFile()
		log.Printf("config file path: %s", cfgPath)
		cfg, err := os.ReadFile(cfgPath)
		if err != nil {
			log.Fatalf("read config file: %v", err)
		}
		err = json.Unmarshal(cfg, &config)
		if err != nil {
			log.Fatalf("unmarshal config: %v", err)
		}
		config.LLMTimeout = time.Duration(config.LLMTimeout) * time.Second
	})
}

func findConfigFile() string {
	dir, err := os.Getwd()
	if err != nil {
		return "config.json"
	}
	for i := 0; i < 6; i++ {
		p := filepath.Join(dir, "config.json")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	dir, _ = os.Getwd()
	return filepath.Join(dir, "config.json")
}

func Load() *Config {
	return &config
}
