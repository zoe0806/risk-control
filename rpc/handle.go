package rpc

import (
	"encoding/json"
	"net/http"
	"risk_control/tools"
	"risk_control/workflow"
	"strings"
	"time"

	"log"
)

func RegisterRoutes(mux *http.ServeMux, eng *workflow.RiskEngine) http.Handler {
	mux.HandleFunc("/health", HealthCheck)
	mux.HandleFunc("/v1/screen", func(w http.ResponseWriter, r *http.Request) { Screen(w, r, eng) })
	mux.HandleFunc("/v1/cases", func(w http.ResponseWriter, r *http.Request) { Cases(w, r, eng) })
	mux.HandleFunc("/v1/cases/", func(w http.ResponseWriter, r *http.Request) { CaseByPath(w, r, eng) })
	mux.HandleFunc("/v1/admin/policies", func(w http.ResponseWriter, r *http.Request) { Policies(w, r, eng) })
	mux.HandleFunc("/v1/admin/policies/reload", func(w http.ResponseWriter, r *http.Request) { ReloadPolicies(w, r, eng) })
	mux.HandleFunc("/v1/admin/metrics", func(w http.ResponseWriter, r *http.Request) { Metrics(w, r, eng) })
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

func Cases(w http.ResponseWriter, r *http.Request, eng *workflow.RiskEngine) {
	if eng == nil || eng.Store() == nil {
		http.Error(w, "store not configured", http.StatusInternalServerError)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	list, err := eng.Store().ListOpenReviewCases(r.Context(), 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []tools.ReviewCase{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(list)
}

func CaseByPath(w http.ResponseWriter, r *http.Request, eng *workflow.RiskEngine) {
	if eng == nil || eng.Store() == nil {
		http.Error(w, "store not configured", http.StatusInternalServerError)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/cases/")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "case id required", http.StatusBadRequest)
		return
	}
	caseID := parts[0]

	if len(parts) == 1 && r.Method == http.MethodGet {
		c, err := eng.Store().GetReviewCase(r.Context(), caseID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if c == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(c)
		return
	}

	if len(parts) == 2 && parts[1] == "resolve" && r.Method == http.MethodPost {
		var req tools.ResolveCaseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c, err := eng.Store().ResolveReviewCase(r.Context(), caseID, req.Decision, req.Resolver, req.Note)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(c)
		return
	}

	http.Error(w, "not found", http.StatusNotFound)
}

// Policies GET 当前主/影子策略快照。
func Policies(w http.ResponseWriter, r *http.Request, eng *workflow.RiskEngine) {
	if eng == nil || eng.Policies() == nil {
		http.Error(w, "policies not configured", http.StatusInternalServerError)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(eng.Policies().Snapshot())
}

type reloadReq struct {
	Target string `json:"target"` // primary | shadow
	Path   string `json:"path"`
}

// ReloadPolicies POST 热加载策略包，无需重启。
func ReloadPolicies(w http.ResponseWriter, r *http.Request, eng *workflow.RiskEngine) {
	if eng == nil || eng.Policies() == nil {
		http.Error(w, "policies not configured", http.StatusInternalServerError)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req reloadReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	target := strings.ToLower(strings.TrimSpace(req.Target))
	if target == "" {
		target = "primary"
	}
	var (
		pack *workflow.PolicyPack
		err  error
	)
	switch target {
	case "primary":
		pack, err = eng.Policies().ReloadPrimary(req.Path)
	case "shadow":
		pack, err = eng.Policies().ReloadShadow(req.Path)
	default:
		http.Error(w, "target must be primary or shadow", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(pack)
}

func Metrics(w http.ResponseWriter, r *http.Request, eng *workflow.RiskEngine) {
	if eng == nil {
		http.Error(w, "engine not configured", http.StatusInternalServerError)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(eng.Metrics())
}
