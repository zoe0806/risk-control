# risk_control

基于 **CloudWeGo Eino** 的风控编排示例：**跨境制裁筛查**与**股票订单**两条流水线；模型 **DeepSeek**（OpenAI 兼容），审计与名单可走 **MySQL**。

## 架构要点

- **`workflow.RiskEngine`**：`EvaluateStockOrder`、`EvaluateCrossBorderTransaction`、`EvaluateScreeningRequest`（按 `business_type` 分发）。
- **`workflow/cb_graph.go`** / **`workflow/stock_graph.go`**：`compose.NewGraph` + Lambda + 分支；观测见 **`workflow/observability.go`**（`graph_observation` 落 `audit_log`）。
- **统一响应**：`tools.ScreeningResult`（含 `business_type`、`transaction_id`；股票场景下 `transaction_id` 为订单号，阻断见 `blocked` / `block_reason`）。

## 配置与运行

配置见根目录 **`config.json`**（`config` 包加载）。在项目根目录：

```bash
cd risk_control
go build -o demo .
./demo
```

## HTTP

| 方法 | 路径 | Body |
|------|------|------|
| `GET` | `/health` | — |
| `POST` | `/v1/screen` | `ScreeningRequest`：`business_type` 为 `cross_border` 或 `stock`，对应填 `transaction` 或 `stock_order` |

**跨境**单笔示例：

```json
{
  "business_type": "cross_border",
  "transaction": {
    "counterparty": "Example Corp",
    "country": "US"
  }
}
```

**股票**需在 `stock_order` 中至少提供 `symbol`；`business_type` 可省略（仅一方有负载时会自动推断）。

响应均为 **`ScreeningResult`**；图级细粒度观测在审计库中按 **`trace_id`** 查询，不在响应 JSON 里展开。

## 目录（节选）

```text
workflow/   risk_engine.go, cb_graph.go, stock_graph.go, stock_subgraph.go, observability.go
tools/      ScreeningRequest、ScreeningResult、领域类型
llm/        Router、Prompt、Retry
store/      MySQL、FlushAudit
rpc/        HTTP
batch/      批量 Invoke
```

## 免责声明

演示数据与策略不构成正式合规结论；上线前须独立评审。
