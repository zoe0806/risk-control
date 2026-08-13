# risk_control

基于 **CloudWeGo Eino** 的**通用多业务风控编排**：跨境制裁与股票订单共用同一 Orchestrator，领域差异在 Domain Profile / 策略包。

## 架构

```text
EvaluateScreeningRequest
  → DomainProfile(cross_border | stock)
  → Orchestrate：预分析 → rule / light_ml / entity_graph → 仲裁
  → 可选 deep 图（cb_graph / stock_graph）+ 熔断降级 + 影子
```

- 通用：`decision`、案例、热更新、指标、熔断、影子
- 跨境专用：制裁名单、国家走廊、对手方匹配
- 股票专用：禁买/ST、财报窗口、名义本金、账户频次

## 运行

```bash
cd risk_control
go build -o demo .
./demo
```

策略包：`policies/cross_border.json`、`policies/stock.json`（及对应 shadow）。

## HTTP

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/v1/screen` | 统一筛查 |
| `GET/POST` | `/v1/cases...` | 人工复核 |
| `GET` | `/v1/admin/policies` | 各域策略快照 |
| `POST` | `/v1/admin/policies/reload` | `{"domain":"stock\|cross_border","target":"primary\|shadow","path":"..."}` |
| `GET` | `/v1/admin/metrics` | 编排指标 |

## 免责声明

演示数据与策略不构成正式合规结论。
