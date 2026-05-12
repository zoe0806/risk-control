package rpc

import (
	"encoding/json"
	"net/http"
	"risk_control/batch"
	"risk_control/tools"
	"risk_control/workflow"
	"time"

	"log"
)

func RegisterRoutes(mux *http.ServeMux, eng *workflow.RiskEngine) http.Handler {
	mux.HandleFunc("/health", HealthCheck)
	mux.HandleFunc("/v1/screen", func(w http.ResponseWriter, r *http.Request) { Screen(w, r, eng) })
	mux.HandleFunc("/v1/screen/batch", func(w http.ResponseWriter, r *http.Request) { ScreenCrossBorderBatch(w, r, eng) })
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

// Screen POST body: tools.ScreeningRequest。
func Screen(w http.ResponseWriter, r *http.Request, eng *workflow.RiskEngine) {
	if eng == nil {
		http.Error(w, "risk engine not configured", http.StatusInternalServerError)
		return
	}
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
	res, err := eng.EvaluateScreeningRequest(invokeCtx, req, workflow.InvokeScreeningOptions()...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	res.TotalDurationMs = time.Since(t0).Milliseconds()
	_ = json.NewEncoder(w).Encode(res)
}

func ScreenCrossBorderBatch(w http.ResponseWriter, r *http.Request, eng *workflow.RiskEngine) {
	if eng == nil {
		http.Error(w, "risk engine not configured", http.StatusInternalServerError)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var txns []tools.CrossBorderTransaction
	if err := json.NewDecoder(r.Body).Decode(&txns); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	reqs := make([]tools.ScreeningRequest, len(txns))
	for i := range txns {
		reqs[i] = tools.NewCrossBorderScreeningRequest(txns[i])
	}
	t0 := time.Now()
	results, errs := batch.ScreenConcurrent(r.Context(), eng.CrossBorderRunnable(), reqs, workflow.InvokeScreeningOptions()...)
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
