# risk_control — 跨境支付制裁筛查

基于 **CloudWeGo Eino** 编排的服务：把「清洗 → 本地名单粗筛 → AI 初筛 → 条件二验 → 报告 → 审计」拆成图上的多个节点，便于单独替换与观测。

## 技术栈（简要）

| 模块 | 说明 |
|------|------|
| **Eino `compose.Graph`** | 工作流编排：`AddLambdaNode` 定义步骤，`AddEdge` 串联，`AddBranch` 做条件分支。 |
| **`llm.Router`** | **不是**工作流本身；只负责按任务类型（初筛 / 二验 / 报告）绑定不同的 ChatModel，便于以后换模型、控成本。 |
| **MySQL（可选）** | `sanctions_entry` 等表：名单粗筛与审计；不配 DSN 时使用内存 Noop，名单查询为空。 |
| **DeepSeek** | 通过 OpenAI 兼容接口（`eino-ext` OpenAI ChatModel）；未配置 API Key 时用内置 Mock 输出。 |

## 工作流里谁在调 LLM？

并非每一步都调模型。典型路径：

1. **ingest / normalize / local_candidates** — 规则与 SQL，**不调 LLM**。  
2. **ai_primary** — **调 LLM**（初筛）。  
3. **分支**：若初筛建议复核且分数 ≥ 阈值 → **ai_secondary**（**调 LLM**）；否则 **skip_secondary**（不调）。  
4. **ai_report** — **调 LLM**（生成 Markdown 报告）。  
5. **persist** — 汇总写审计，**不调 LLM**。

## 配置

从**进程当前工作目录**读取 `config.json`（请在项目根目录启动服务）。主要字段示例：

- `httpaddr`：监听地址，如 `:8080`  
- `mysqldsn`：MySQL DSN；留空则不做本地名单与持久化  
- `deepseekapikey` / `deepseekbaseurl` / `modelprimary` / `modelverify` / `modelreport`  
- `llmtimeout`：LLM  HTTP 超时（配置里为数字，具体含义以 `config/config.go` 中 `Load` 的换算为准）  
- `sysprompt` / `userprompt` / `verifyprompt` / `reportprompt`：可按需在代码中与 `llm/prompts.go` 联动扩展  

**勿将真实 API Key 提交到公开仓库**；生产环境建议用私密配置或密钥管理。

## 运行

```bash
cd risk_control
go build -o demo .
./demo
```

Windows：`demo.exe`。

## HTTP 接口

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/health` | 健康检查 |
| POST | `/v1/screen` | 单笔筛查，Body 为 JSON（见 `domain.ScreeningRequest`） |
| POST | `/v1/screen/batch` | 批量筛查，Body 为请求数组 |

**单笔请求示例：**

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

## 目录结构（示意）

```
risk_control/
├── main.go           # HTTP 入口，编译 Graph 并 Invoke
├── config/           # 读取 config.json
├── domain/           # 请求/状态/结果类型
├── workflow/graph.go # Eino Graph 编排
├── llm/              # Router、Prompt、Mock
├── store/            # MySQL / Noop
└── batch/            # 批量并发调用封装
```

## 说明

本仓库名单数据与风控策略皆为测试数据，不代表任何正式合规结论；上线前需独立法务与风控评审。
