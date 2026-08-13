package workflow

import (
	"strings"
	"sync"

	"risk_control/config"
	"risk_control/tools"
)

// EntityGraph 简易关联图：账户/设备/IP/对手/银行多跳聚集（阶段4）。
type EntityGraph struct {
	mu    sync.Mutex
	adj   map[string]map[string]int // undirected weighted
	partyDevices map[string]map[string]struct{} // party -> devices
}

func NewEntityGraph() *EntityGraph {
	return &EntityGraph{
		adj:          make(map[string]map[string]int),
		partyDevices: make(map[string]map[string]struct{}),
	}
}

func (g *EntityGraph) link(a, b string) {
	if a == "" || b == "" || a == b {
		return
	}
	if g.adj[a] == nil {
		g.adj[a] = map[string]int{}
	}
	if g.adj[b] == nil {
		g.adj[b] = map[string]int{}
	}
	g.adj[a][b]++
	g.adj[b][a]++
}

// Observe 写入一笔交易的实体边，返回聚集风险信号。
func (g *EntityGraph) Observe(txn tools.CrossBorderTransaction, party *tools.NormalizedParty, orch config.OrchestratorConfig) (score float64, decision string, detail string, policies []string) {
	if g == nil {
		return 0, tools.DecisionApprove, "graph_disabled", nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	partyKey := ""
	if party != nil {
		partyKey = "party:" + party.NormalizedKey
	}
	acct := prefix("acct", txn.AccountID)
	dev := prefix("dev", txn.DeviceID)
	ip := prefix("ip", txn.ClientIP)
	bank := prefix("bank", strings.ToUpper(strings.TrimSpace(txn.BankName)))

	nodes := []string{partyKey, acct, dev, ip, bank}
	for i := 0; i < len(nodes); i++ {
		for j := i + 1; j < len(nodes); j++ {
			g.link(nodes[i], nodes[j])
		}
	}

	sharedParties := 0
	if txn.DeviceID != "" && party != nil {
		dk := txn.DeviceID
		if g.partyDevices[dk] == nil {
			g.partyDevices[dk] = map[string]struct{}{}
		}
		g.partyDevices[dk][party.NormalizedKey] = struct{}{}
		sharedParties = len(g.partyDevices[dk])
	}

	degree := 0
	if partyKey != "" {
		degree = len(g.adj[partyKey])
	}
	// 1-hop cluster size around account
	cluster := degree
	if acct != "" {
		cluster = max(cluster, len(g.adj[acct]))
	}

	score = 0.05
	decision = tools.DecisionApprove
	detail = "graph_ok"
	if sharedParties >= orch.GraphSharedDeviceReview {
		score = 0.45
		decision = tools.DecisionReview
		detail = "shared_device_multi_party"
		policies = append(policies, tools.PolicyEntityGraph)
	}
	if cluster >= orch.GraphClusterReject {
		score = 0.85
		decision = tools.DecisionReject
		detail = "entity_cluster_too_dense"
		policies = []string{tools.PolicyEntityGraph}
	}
	return score, decision, detail, policies
}

func prefix(kind, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return kind + ":" + strings.ToLower(id)
}
