# risk_control

跨境支付场景下的 **制裁名单筛查**：用 **CloudWeGo Eino** 把「清洗 → 本地粗筛 → AI 初筛 → 条件二验 → 报告 → 审计」编排成可替换、可观测的 Graph；模型走 **DeepSeek**（OpenAI 兼容），数据层可选 **MySQL**。

---

## 一分钟理解架构

| 层次 | 职责 |
|------|------|
| **`workflow/graph.go`** | 真正的业务编排：`compose.NewGraph` + `AddLambdaNode` + `AddEdge` + `AddBranch`。 |
| **`llm.Router`** | 仅做 **模型路由**（初筛 / 二验 / 报告可绑定不同 `ChatModel`），**不是** Graph 本身。 |
| **`workflow/observability.go`** | `compose.WithCallbacks`：节点级 **OnStart / OnEnd / OnError**；结构化轨迹经 **`graph_observation`** 写入 `audit_log`（按 `trace_id` 查）。 |
| **`store.FlushAudit`** | **仅**将 `audit_log`、`ai_decision` 在同一 **InnoDB 事务**中落库；名单查询不在该事务内。 |
| **`llm/retry.go`** | 对 LLM `Generate` 做有限次重试 + 指数退避（可重试错误启发式判断）。 |

---

## 执行路径与 LLM 调用

```text
START → ingest → normalize → local_candidates → ai_primary ─┬→ ai_secondary ─┐
                                                             └→ skip_secondary ┘
                                                                        ↓
                                                                  ai_report → persist → END
```

- **不调 LLM**：`ingest`、`normalize`、`local_candidates`、`persist`。  
- **调 LLM**：`ai_primary`、`ai_report`；**`ai_secondary` 仅当**初筛 `needs_secondary_review` 且 `risk_score ≥ primaryRiskScore`（默认阈值见配置）。  
- **优雅降级**：`ai_secondary` 在超时、解析失败等情况下 **不中断整条流水线**，写入 `secondary.technical_degraded=true`，分数回退初筛，报告与审计照常生成。

---

## 配置（`config.json`）

在**项目根目录**启动进程，配置在 `init` 中读取一次（见 `config/config.go`）。

---

## 运行

```bash
cd risk_control
go build -o demo .
./demo
```


---

## HTTP API

| 方法 | 路径 | Body |
|------|------|------|
| `GET` | `/health` | — |
| `POST` | `/v1/screen` | 单笔 `tools.ScreeningRequest` JSON |
| `POST` | `/v1/screen/batch` | `ScreeningRequest` 的 JSON **数组** |

单笔示例：

```json
{
  "transaction_id": "TXN-2026-001",
  "counterparty": "ROSNEFT OIL COMPANY",
  "country": "RU",
  "bank_name": "Example Bank Ltd",
  "payment_purpose": "goods payment",
  "amount_minor_unit": 1000000,
  "currency": "USD"
}
```

`transaction_id` 可省略，服务会自动生成。

响应体为 `tools.ScreeningResult`（分数、等级、初筛/二验、报告、`persisted_audit_rows` 等），**不含**图执行观测明细。

### 观测数据去哪看？

- **开发期**：标准输出仍有 `[graph cb]` 日志，便于本地跟一条请求。  
- **库内**：配置了 MySQL 时，每次筛查会在 `audit_log` 中写入 `step_name = graph_observation` 的一行，`detail_json` 为节点跨度与边的 JSON，与 **`trace_id`** 对齐（响应里的 `trace_id` 可用来查）。  
- **后续可做**：单独做一个只读 **可视化入口**（小 Web 页或内网 Grafana/自建 Trace 列表），按 `trace_id` 拉 `audit_log` + `ai_decision` 画 DAG/时间线；与业务 `/v1/screen` 响应解耦更清晰。

---

## 目录说明

```text
risk_control/
├── main.go                 # HTTP、组装 Store / Router / Graph
├── config/                 # config.json（init 加载）
├── tools/                  # 领域类型：请求/状态/结果、审计缓冲、标准化
├── workflow/
│   ├── graph.go            # Eino Graph 编排
│   └── observability.go  # WithRunTrace + WithCallbacks 观测
├── llm/                    # Router、Prompt、Retry、Mock
├── store/                  # MySQL + Noop；FlushAudit 事务写审计表
└── batch/                  # 批量筛查（并发 + 统一 Invoke Option）
```

---

## 免责声明

仓库内名单与策略均为 **演示用途**，不构成任何正式合规结论；上线前须经过法务与风控独立评审。
