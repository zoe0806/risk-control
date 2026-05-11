package llm

// Task 用于模型分层路由：未来可将轻量任务映射到更便宜模型。
type Task string

const (
	TaskSanctionsPrimary Task = "sanctions_primary"
	TaskSanctionsVerify  Task = "sanctions_verify"
	TaskReport           Task = "sanctions_report"
)
