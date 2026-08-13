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
)

// PolicyPack 可热更新的跨境策略包（阶段3）。
type PolicyPack struct {
	Version      string                    `json:"version"`
	CrossBorder  config.CrossBorderRules   `json:"crossBorder"`
	LightWeights config.LightMLWeights     `json:"lightWeights"`
	Orchestrator config.OrchestratorConfig `json:"orchestrator"`
	LoadedAt     time.Time                 `json:"loaded_at"`
	SourcePath   string                    `json:"source_path,omitempty"`
}

// PolicyHub 原子切换主策略 / 影子策略。
type PolicyHub struct {
	primary atomic.Pointer[PolicyPack]
	shadow  atomic.Pointer[PolicyPack]
	mu      sync.Mutex
	primaryPath string
	shadowPath  string
}

func NewPolicyHub(cfg config.Config) *PolicyHub {
	h := &PolicyHub{
		primaryPath: cfg.PolicyPackPath,
		shadowPath:  cfg.ShadowPackPath,
	}
	pack := packFromConfig(cfg)
	h.primary.Store(pack)
	if cfg.ShadowPackPath != "" {
		if sp, err := LoadPolicyPack(cfg.ShadowPackPath); err == nil {
			h.shadow.Store(sp)
		}
	}
	return h
}

func packFromConfig(cfg config.Config) *PolicyPack {
	w := config.DefaultLightWeights()
	return &PolicyPack{
		Version:      "config_embedded",
		CrossBorder:  cfg.CBRules(),
		LightWeights: w,
		Orchestrator: cfg.Orch(),
		LoadedAt:     time.Now(),
		SourcePath:   "config.json",
	}
}

// Primary 当前主策略。
func (h *PolicyHub) Primary() *PolicyPack {
	if h == nil {
		return nil
	}
	return h.primary.Load()
}

// Shadow 影子策略（可空）。
func (h *PolicyHub) Shadow() *PolicyPack {
	if h == nil {
		return nil
	}
	return h.shadow.Load()
}

// ReloadPrimary 从路径热加载主策略。
func (h *PolicyHub) ReloadPrimary(path string) (*PolicyPack, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if path == "" {
		path = h.primaryPath
	}
	if path == "" {
		return nil, fmt.Errorf("empty policy pack path")
	}
	pack, err := LoadPolicyPack(path)
	if err != nil {
		return nil, err
	}
	h.primaryPath = path
	h.primary.Store(pack)
	return pack, nil
}

// ReloadShadow 热加载影子策略。
func (h *PolicyHub) ReloadShadow(path string) (*PolicyPack, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if path == "" {
		path = h.shadowPath
	}
	if path == "" {
		return nil, fmt.Errorf("empty shadow pack path")
	}
	pack, err := LoadPolicyPack(path)
	if err != nil {
		return nil, err
	}
	h.shadowPath = path
	h.shadow.Store(pack)
	return pack, nil
}

// LoadPolicyPack 从 JSON 文件加载。
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
	// 补默认
	cfg := config.Config{CrossBorder: pack.CrossBorder, Orchestrator: pack.Orchestrator}
	pack.CrossBorder = cfg.CBRules()
	pack.Orchestrator = cfg.Orch()
	if pack.LightWeights == (config.LightMLWeights{}) {
		pack.LightWeights = config.DefaultLightWeights()
	}
	pack.LoadedAt = time.Now()
	pack.SourcePath = path
	return &pack, nil
}

// Snapshot 管理端查看。
func (h *PolicyHub) Snapshot() map[string]any {
	out := map[string]any{}
	if p := h.Primary(); p != nil {
		out["primary"] = p
	}
	if s := h.Shadow(); s != nil {
		out["shadow"] = s
	}
	return out
}
