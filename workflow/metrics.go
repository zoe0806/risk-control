package workflow

import "sync/atomic"

// EngineMetrics 引擎级可观测计数。
type EngineMetrics struct {
	PreFast    atomic.Int64
	PreLight   atomic.Int64
	PreDeep    atomic.Int64
	DeepOK     atomic.Int64
	DeepFail   atomic.Int64
	DeepSkip   atomic.Int64
	LocalOnly  atomic.Int64
	ShadowDiff atomic.Int64
}

func (m *EngineMetrics) Snapshot() map[string]int64 {
	if m == nil {
		return nil
	}
	return map[string]int64{
		"pre_fast":    m.PreFast.Load(),
		"pre_light":   m.PreLight.Load(),
		"pre_deep":    m.PreDeep.Load(),
		"deep_ok":     m.DeepOK.Load(),
		"deep_fail":   m.DeepFail.Load(),
		"deep_skip":   m.DeepSkip.Load(),
		"local_only":  m.LocalOnly.Load(),
		"shadow_diff": m.ShadowDiff.Load(),
	}
}
