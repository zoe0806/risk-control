package workflow

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"

	"risk_control/tools"
)

// runTraceKey 将一次 Invoke 的观测数据挂在 context 上，供 persist 合并进 ScreeningResult。
type runTraceKey struct{}

// RunTrace 记录节点级 OnStart/OnEnd/OnError（Eino 回调）；边信息用「上一节点 → 当前节点」近似实际执行序。
type RunTrace struct {
	mu    sync.Mutex
	last  string
	Spans []tools.NodeSpanObservation
	Edges []tools.EdgeObservation
}

type spanStartKey struct{}

// WithRunTrace 在调用 run.Invoke 前注入；persist 中通过 ExportFromContext 取出。
func WithRunTrace(ctx context.Context) (context.Context, *RunTrace) {
	tr := &RunTrace{}
	return context.WithValue(ctx, runTraceKey{}, tr), tr
}

// ExportFromContext 从 context 取出 RunTrace（无则 nil）。
func ExportFromContext(ctx context.Context) *RunTrace {
	v, _ := ctx.Value(runTraceKey{}).(*RunTrace)
	return v
}

// ToObservation 拷贝为结构化快照（写入审计等，不放入筛查业务 JSON 响应）。
func (tr *RunTrace) ToObservation() *tools.GraphObservation {
	if tr == nil {
		return nil
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	out := &tools.GraphObservation{
		NodeSpans: append([]tools.NodeSpanObservation(nil), tr.Spans...),
		Edges:     append([]tools.EdgeObservation(nil), tr.Edges...),
	}
	return out
}

// GraphInvokeCallbacks 返回挂到 compose.WithCallbacks 的 Handler，覆盖 OnStart / OnEnd / OnError。
// 说明：Lambda 节点的 RunInfo.Name 来自 compose.WithNodeName；图根 Runnable 也会有回调，可按 Component 过滤。
func GraphInvokeCallbacks() callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
			if info == nil {
				return ctx
			}
			name := info.Name
			if name == "" {
				return ctx
			}
			tr, ok := ctx.Value(runTraceKey{}).(*RunTrace)
			if !ok || tr == nil {
				return context.WithValue(ctx, spanStartKey{}, time.Now())
			}
			tr.mu.Lock()
			from := tr.last
			if from != "" && from != name {
				tr.Edges = append(tr.Edges, tools.EdgeObservation{From: from, To: name})
			}
			tr.last = name
			tr.mu.Unlock()
			log.Printf("[graph cb] OnStart node=%s component=%s type=%s", name, info.Component, info.Type)
			return context.WithValue(ctx, spanStartKey{}, time.Now())
		}).
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
			if info == nil {
				return ctx
			}
			name := info.Name
			if name == "" {
				return ctx
			}
			var durMs int64
			if t0, ok := ctx.Value(spanStartKey{}).(time.Time); ok {
				durMs = time.Since(t0).Milliseconds()
			}
			tr, ok := ctx.Value(runTraceKey{}).(*RunTrace)
			if !ok || tr == nil {
				return ctx
			}
			span := tools.NodeSpanObservation{
				Node:       name,
				Component:  string(info.Component),
				Type:       info.Type,
				DurationMs: durMs,
			}
			if mi := model.ConvCallbackOutput(output); mi != nil && mi.Message != nil && mi.Message.ResponseMeta != nil {
				log.Printf("[graph cb] OnEnd node=%s duration_ms=%d tokens=%d", name, durMs, mi.Message.ResponseMeta.Usage.TotalTokens)
			} else {
				log.Printf("[graph cb] OnEnd node=%s duration_ms=%d", name, durMs)
			}
			tr.mu.Lock()
			tr.Spans = append(tr.Spans, span)
			tr.mu.Unlock()
			return ctx
		}).
		OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			if err == nil {
				return ctx
			}
			name := ""
			if info != nil {
				name = info.Name
			}
			log.Printf("[graph cb] OnError node=%s err=%v", name, err)
			tr, ok := ctx.Value(runTraceKey{}).(*RunTrace)
			if !ok || tr == nil {
				return ctx
			}
			tr.mu.Lock()
			tr.Spans = append(tr.Spans, tools.NodeSpanObservation{
				Node:      name,
				Component: safeComponent(info),
				Type:      safeType(info),
				Error:     err.Error(),
			})
			tr.mu.Unlock()
			return ctx
		}).
		Build()
}

func safeComponent(info *callbacks.RunInfo) string {
	if info == nil {
		return ""
	}
	return string(info.Component)
}

func safeType(info *callbacks.RunInfo) string {
	if info == nil {
		return ""
	}
	return info.Type
}

// InvokeScreeningOptions 单次筛查在 Invoke 上统一挂载的 compose.Option（回调 + 步数上限）。
func InvokeScreeningOptions() []compose.Option {
	return []compose.Option{
		compose.WithCallbacks(GraphInvokeCallbacks()),
		compose.WithRuntimeMaxSteps(64),
	}
}
