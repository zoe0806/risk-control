# risk_control

基于 **CloudWeGo Eino** 的风控编排示例：**跨境制裁筛查**与**股票订单**两条流水线；模型 **DeepSeek**（OpenAI 兼容），审计与名单可走 **MySQL**。

## 架构要点

- **阶段1–2**：本地漏斗、决策码、名单版本、人工案例。
- **阶段3**：预分析路由 → `rule` / `light_ml` / `entity_graph` → 仲裁 → 可选深度 `cb_graph`（超时熔断降级）；策略包热更新。
- **阶段4**：轻量线性模型、实体关联图、影子策略对比、REVIEW 异步案例草稿。

## 配置与运行

根目录 **`config.json`** + **`policies/cross_border.json`**（主策略）/ **`policies/shadow.json`**（影子）：

```bash
cd risk_control
go build -o demo .
./demo
```

## HTTP

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/health` | 健康检查 |
| `POST` | `/v1/screen` | 统一筛查 |
| `GET` | `/v1/cases` | 待复核案例 |
| `GET` | `/v1/cases/{id}` | 案例详情（含 `draft_markdown`） |
| `POST` | `/v1/cases/{id}/resolve` | 人工终裁 |
| `GET` | `/v1/admin/policies` | 主/影子策略快照 |
| `POST` | `/v1/admin/policies/reload` | 热加载 `{"target":"primary\|shadow","path":"..."}` |
| `GET` | `/v1/admin/metrics` | 路由/深度/熔断/影子计数 |

响应含 `decision`、`route_bucket`、`engines`、`pack_version`、`degraded` 等字段。

## 免责声明

演示数据与策略不构成正式合规结论；上线前须独立评审。
