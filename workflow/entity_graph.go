package workflow

import (
	"strings"
	"sync"

	"risk_control/config"
	"risk_control/tools"
)

// EntityGraph 简易关联图：多跳实体共现（业务无关）。
type EntityGraph struct {
	mu           sync.Mutex
	adj          map[string]map[string]int
	deviceSubjects map[string]map[string]struct{} // device -> subject keys
}

func NewEntityGraph() *EntityGraph {
	return &EntityGraph{
		adj:           make(map[string]map[string]int),
		deviceSubjects: make(map[string]map[string]struct{}),
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

// ObserveLinks 写入节点边并评估聚集风险。
// deviceID 非空时统计「同设备多主体」；subjectKey 为对手方或标的。
func (g *EntityGraph) ObserveLinks(nodes []string, deviceID, subjectKey string, orch config.OrchestratorConfig) (score float64, decision string, detail string, policies []string) {
	if g == nil {
		return 0, tools.DecisionApprove, "graph_disabled", nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	clean := make([]string, 0, len(nodes))
	for _, n := range nodes {
		n = strings.TrimSpace(n)
		if n != "" {
			clean = append(clean, n)
		}
	}
	for i := 0; i < len(clean); i++ {
		for j := i + 1; j < len(clean); j++ {
			g.link(clean[i], clean[j])
		}
	}

	sharedSubjects := 0
	if deviceID != "" && subjectKey != "" {
		if g.deviceSubjects[deviceID] == nil {
			g.deviceSubjects[deviceID] = map[string]struct{}{}
		}
		g.deviceSubjects[deviceID][subjectKey] = struct{}{}
		sharedSubjects = len(g.deviceSubjects[deviceID])
	}

	cluster := 0
	for _, n := range clean {
		if d := len(g.adj[n]); d > cluster {
			cluster = d
		}
	}

	score = 0.05
	decision = tools.DecisionApprove
	detail = "graph_ok"
	if sharedSubjects >= orch.GraphSharedDeviceReview {
		score = 0.45
		decision = tools.DecisionReview
		detail = "shared_device_multi_subject"
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

// ObserveCB 跨境便捷封装（兼容旧调用）。
func (g *EntityGraph) ObserveCB(txn tools.CrossBorderTransaction, party *tools.NormalizedParty, orch config.OrchestratorConfig) (float64, string, string, []string) {
	partyKey := ""
	subject := ""
	if party != nil {
		partyKey = "party:" + party.NormalizedKey
		subject = party.NormalizedKey
	}
	nodes := []string{
		partyKey,
		prefixNode("acct", txn.AccountID),
		prefixNode("dev", txn.DeviceID),
		prefixNode("ip", txn.ClientIP),
		prefixNode("bank", strings.ToUpper(strings.TrimSpace(txn.BankName))),
	}
	return g.ObserveLinks(nodes, txn.DeviceID, subject, orch)
}

func prefixNode(kind, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return kind + ":" + strings.ToLower(id)
}
