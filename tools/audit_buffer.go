package tools

func (b *AuditBuffer) AddStep(stepName, detailJSON string, latencyMs int64) {
	if b == nil {
		return
	}
	b.Steps = append(b.Steps, AuditStepDraft{
		StepName:   stepName,
		DetailJSON: detailJSON,
		LatencyMs:  latencyMs,
	})
}

func (b *AuditBuffer) AddDecision(taskKind, modelName, inputSummary, outputText string, latencyMs int64) {
	if b == nil {
		return
	}
	b.Decisions = append(b.Decisions, AIDecisionDraft{
		TaskKind:     taskKind,
		ModelName:    modelName,
		InputSummary: inputSummary,
		OutputText:   outputText,
		LatencyMs:    latencyMs,
	})
}
