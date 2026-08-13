package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"risk_control/config"
	"risk_control/tools"
)

// PolicyPack 可热更新策略包（可含跨境与/或股票域字段）。
type PolicyPack struct {
	Domain       string                    `json:"domain,omitempty"` // cross_border | stock
	Version      string                    `json:"version"`
	CrossBorder  config.CrossBorderRules   `json:"crossBorder"`
	Stock        config.StockRules         `json:"stock"`
	LightWeights config.LightMLWeights     `json:"lightWeights"`
	Orchestrator config.OrchestratorConfig `json:"orchestrator"`
	LoadedAt     time.Time                 `json:"loaded_at"`
	SourcePath   string                    `json:"source_path,omitempty"`
}

// domainHub 单业务主/影子策略。
type domainHub struct {
	primary     atomic.Pointer[PolicyPack]
	shadow      atomic.Pointer[PolicyPack]
	primaryPath string
	shadowPath  string
}

// PolicyRegistry 多业务策略注册表。
type PolicyRegistry struct {
	mu    sync.Mutex
	hubs  map[string]*domainHub
	cfg   config.Config
}

// PolicyHub 兼容旧 API：默认指向跨境域。
type PolicyHub struct {
	reg *PolicyRegistry
}

func NewPolicyRegistry(cfg config.Config) *PolicyRegistry {
	r := &PolicyRegistry{
		hubs: make(map[string]*domainHub),
		cfg:  cfg,
	}
	// 跨境
	cbPaths := domainPaths(cfg, tools.BusinessCrossBorder)
	cb := &domainHub{primaryPath: cbPaths.Primary, shadowPath: cbPaths.Shadow}
	cb.primary.Store(packFromConfigDomain(cfg, tools.BusinessCrossBorder))
	if cbPaths.Primary != "" {
		if p, err := LoadPolicyPack(cbPaths.Primary); err == nil {
			cb.primary.Store(p)
			cb.primaryPath = cbPaths.Primary
		}
	}
	if cbPaths.Shadow != "" {
		if p, err := LoadPolicyPack(cbPaths.Shadow); err == nil {
			cb.shadow.Store(p)
			cb.shadowPath = cbPaths.Shadow
		}
	}
	r.hubs[tools.BusinessCrossBorder] = cb

	// 股票
	stPaths := domainPaths(cfg, tools.BusinessStock)
	st := &domainHub{primaryPath: stPaths.Primary, shadowPath: stPaths.Shadow}
	st.primary.Store(packFromConfigDomain(cfg, tools.BusinessStock))
	if stPaths.Primary != "" {
		if p, err := LoadPolicyPack(stPaths.Primary); err == nil {
			st.primary.Store(p)
			st.primaryPath = stPaths.Primary
		}
	}
	if stPaths.Shadow != "" {
		if p, err := LoadPolicyPack(stPaths.Shadow); err == nil {
			st.shadow.Store(p)
			st.shadowPath = stPaths.Shadow
		}
	}
	r.hubs[tools.BusinessStock] = st
	return r
}

func domainPaths(cfg config.Config, domain string) config.DomainPolicyPaths {
	if cfg.DomainPolicies != nil {
		if p, ok := cfg.DomainPolicies[domain]; ok {
			return p
		}
	}
	if domain == tools.BusinessCrossBorder {
		return config.DomainPolicyPaths{Primary: cfg.PolicyPackPath, Shadow: cfg.ShadowPackPath}
	}
	if domain == tools.BusinessStock {
		return config.DomainPolicyPaths{Primary: "policies/stock.json", Shadow: "policies/stock_shadow.json"}
	}
	return config.DomainPolicyPaths{}
}

func packFromConfigDomain(cfg config.Config, domain string) *PolicyPack {
	w := config.DefaultLightWeights()
	pack := &PolicyPack{
		Domain:       domain,
		Version:      "config_embedded_" + domain,
		CrossBorder:  cfg.CBRules(),
		Stock:        cfg.StockRulesOrDefault(config.StockRules{}),
		LightWeights: w,
		Orchestrator: cfg.Orch(),
		LoadedAt:     time.Now(),
		SourcePath:   "config.json",
	}
	return pack
}

func (r *PolicyRegistry) hub(domain string) *domainHub {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.hubs[domain]; ok {
		return h
	}
	h := &domainHub{}
	h.primary.Store(packFromConfigDomain(r.cfg, domain))
	r.hubs[domain] = h
	return h
}

func (r *PolicyRegistry) Primary(domain string) *PolicyPack {
	return r.hub(domain).primary.Load()
}

func (r *PolicyRegistry) Shadow(domain string) *PolicyPack {
	return r.hub(domain).shadow.Load()
}

func (r *PolicyRegistry) Reload(domain, target, path string) (*PolicyPack, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.hubs[domain]
	if !ok {
		h = &domainHub{}
		r.hubs[domain] = h
	}
	if target == "" {
		target = "primary"
	}
	if path == "" {
		if target == "shadow" {
			path = h.shadowPath
		} else {
			path = h.primaryPath
		}
	}
	if path == "" {
		return nil, fmt.Errorf("empty policy path for domain=%s target=%s", domain, target)
	}
	pack, err := LoadPolicyPack(path)
	if err != nil {
		return nil, err
	}
	pack.Domain = domain
	if target == "shadow" {
		h.shadowPath = path
		h.shadow.Store(pack)
	} else {
		h.primaryPath = path
		h.primary.Store(pack)
	}
	return pack, nil
}

func (r *PolicyRegistry) Snapshot() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]any{}
	for id, h := range r.hubs {
		entry := map[string]any{}
		if p := h.primary.Load(); p != nil {
			entry["primary"] = p
		}
		if s := h.shadow.Load(); s != nil {
			entry["shadow"] = s
		}
		out[id] = entry
	}
	return out
}

// NewPolicyHub 兼容：包装 Registry，默认跨境。
func NewPolicyHub(cfg config.Config) *PolicyHub {
	return &PolicyHub{reg: NewPolicyRegistry(cfg)}
}

func (h *PolicyHub) Registry() *PolicyRegistry {
	if h == nil {
		return nil
	}
	return h.reg
}

func (h *PolicyHub) Primary() *PolicyPack {
	if h == nil || h.reg == nil {
		return nil
	}
	return h.reg.Primary(tools.BusinessCrossBorder)
}

func (h *PolicyHub) Shadow() *PolicyPack {
	if h == nil || h.reg == nil {
		return nil
	}
	return h.reg.Shadow(tools.BusinessCrossBorder)
}

func (h *PolicyHub) ReloadPrimary(path string) (*PolicyPack, error) {
	return h.reg.Reload(tools.BusinessCrossBorder, "primary", path)
}

func (h *PolicyHub) ReloadShadow(path string) (*PolicyPack, error) {
	return h.reg.Reload(tools.BusinessCrossBorder, "shadow", path)
}

func (h *PolicyHub) Snapshot() map[string]any {
	if h == nil || h.reg == nil {
		return nil
	}
	return h.reg.Snapshot()
}

func LoadPolicyPack(path string) (*PolicyPack, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pack PolicyPack
	if err := json.Unmarshal(b, &pack); err != nil {
		return nil, err
	}
	if pack.Version == "" {
		pack.Version = filepath.Base(path)
	}
	cfg := config.Config{CrossBorder: pack.CrossBorder, Orchestrator: pack.Orchestrator}
	pack.CrossBorder = cfg.CBRules()
	pack.Orchestrator = cfg.Orch()
	pack.Stock = cfg.StockRulesOrDefault(pack.Stock)
	if pack.LightWeights == (config.LightMLWeights{}) {
		pack.LightWeights = config.DefaultLightWeights()
	}
	pack.LoadedAt = time.Now()
	pack.SourcePath = path
	return &pack, nil
}

func packFromConfig(cfg config.Config) *PolicyPack {
	return packFromConfigDomain(cfg, tools.BusinessCrossBorder)
}
