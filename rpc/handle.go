package rpc

import (
	"encoding/json"
	"net/http"
	"risk_control/batch"
	"risk_control/tools"
	"risk_control/workflow"
	"time"

	"log"

	"github.com/cloudwego/eino/compose"
)

func RegisterRoutes(mux *http.ServeMux, run compose.Runnable[tools.ScreeningRequest, tools.ScreeningResult]) http.Handler {
	mux.HandleFunc("/health", HealthCheck)
	mux.HandleFunc("/v1/screen", func(w http.ResponseWriter, r *http.Request) { Screen(w, r, run) })
	mux.HandleFunc("/v1/screen/batch", func(w http.ResponseWriter, r *http.Request) { ScreenBatch(w, r, run) })
	return logReq(mux)
}

func logReq(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t0 := time.Now()
		mux.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(t0))
	})
}

func HealthCheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func Screen(w http.ResponseWriter, r *http.Request, run compose.Runnable[tools.ScreeningRequest, tools.ScreeningResult]) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req tools.ScreeningRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	t0 := time.Now()
	invokeCtx, _ := workflow.WithRunTrace(r.Context())
	out, err := run.Invoke(invokeCtx, req, workflow.InvokeScreeningOptions()...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out.TotalDurationMs = time.Since(t0).Milliseconds()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(out)
}

func ScreenBatch(w http.ResponseWriter, r *http.Request, run compose.Runnable[tools.ScreeningRequest, tools.ScreeningResult]) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var reqs []tools.ScreeningRequest
	if err := json.NewDecoder(r.Body).Decode(&reqs); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	t0 := time.Now()
	results, errs := batch.ScreenConcurrent(r.Context(), run, reqs, workflow.InvokeScreeningOptions()...)
	type row struct {
		Result tools.ScreeningResult `json:"result,omitempty"`
		Error  string                `json:"error,omitempty"`
	}
	out := make([]row, len(reqs))
	for i := range reqs {
		if errs[i] != nil {
			out[i].Error = errs[i].Error()
			continue
		}
		out[i].Result = results[i]
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items":             out,
		"batch_duration_ms": time.Since(t0).Milliseconds(),
		"concurrency":       4,
	})

}
