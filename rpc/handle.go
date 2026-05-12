package rpc

import (
	"encoding/json"
	"net/http"
	"risk_control/tools"
	"risk_control/workflow"
	"time"

	"log"
)

func RegisterRoutes(mux *http.ServeMux, eng *workflow.RiskEngine) http.Handler {
	mux.HandleFunc("/health", HealthCheck)
	mux.HandleFunc("/v1/screen", func(w http.ResponseWriter, r *http.Request) { Screen(w, r, eng) })
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
