# risk_control

通用多业务风控编排：跨境制裁与股票订单共用同一 Orchestrator，领域差异在 Domain Profile / 策略包。

深度 LLM 已从编排内核拆出。默认 `native`（本进程调 DeepSeek）。**不会自动扫描本机有没有 Codex**；要接 Codex，把 `deepRuntime.kind` 改成 `codex` 后重启服务。

## 架构

```text
EvaluateScreeningRequest
  → DomainProfile(cross_border | stock)
  → Orchestrate：预分析 → rule / light_ml / entity_graph → 仲裁
  → 可选 DeepRuntime（native | eino | cli | codex）+ 熔断降级 + 影子
```

## 深度 Runtime（怎么接 Codex）

系统**不识别「正在运行的 Codex」**。每次灰区请求会 **新起一个子进程** 调 CLI。

1. 本机已安装并登录：`codex --version` 能跑通。
2. 改 `config.json`：

```json
"deepRuntime": {
  "kind": "codex",
  "cli": {
    "command": "codex",
    "args": ["exec", "--sandbox", "read-only", "--ephemeral"],
    "timeoutMs": 180000
  }
}
```

3. 重启风控进程。灰区才会调用；白名单/禁运等本地闸门仍不走 Codex。

实际命令等价于：

```text
codex exec --sandbox read-only --ephemeral "<固定指令：只输出 DeepOutput JSON>"
# stdin = DeepInput JSON（规则证据 + 已渲染 prompts）
# stdout = 最终回复（从中解析 JSON）
```

| kind | 说明 |
|------|------|
| `native` | 默认。进程内 DeepSeek，不调用 Codex |
| `eino` | 仅 AI 节点的 Eino 图 |
| `codex` | 调本机 `codex exec`（PATH 上的二进制，需已登录） |
| `cli` | 自研 CLI：stdin/stdout 必须已是 `risk.deep.v1` JSON，**不能**直接填 `codex` |
| `off` | 永不进深度（`riskctl eval`） |

`kind=cli` 和 `kind=codex` 的差别：自研 CLI 已经会说我们的协议；Codex 只吃自然语言，所以由本仓库把 DeepInput 包装成 `codex exec` 的 prompt+stdin。

## 运行

```bash
cd risk_control
go build -o demo .
./demo
```

策略包：`policies/cross_border.json`、`policies/stock.json`（及对应 shadow）。

### 策略 CLI（无 LLM）

```bash
go run ./cmd/riskctl eval -i testdata/eval/cb_whitelist.json -expect APPROVE
go run ./cmd/riskctl eval -i testdata/eval/cb_block_country.json -expect REJECT
go run ./cmd/riskctl eval -i testdata/eval/stock_ban.json -expect REJECT
```

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
