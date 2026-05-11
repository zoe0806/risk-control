package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"risk_control/batch"
	"risk_control/config"
	"risk_control/domain"
	"risk_control/llm"
	"risk_control/store"
	"risk_control/workflow"
)

func main() {
	cfg := config.Load()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var st store.Store = store.Noop{}
	if cfg.MySQLDSN != "" {
		db, err := store.OpenMySQL(cfg.MySQLDSN)
		if err != nil {
			log.Fatalf("mysql: %v", err)
		}
		defer db.Close()
		if err := db.EnsureSchema(context.Background()); err != nil {
			log.Fatalf("schema: %v", err)
		}
		st = db
	} else {
		log.Printf("MYSQL_DSN empty: using Noop store (no list / audit persistence)")
	}

	router, err := llm.NewRouter(ctx, cfg)
	if err != nil {
		log.Fatalf("llm router: %v", err)
	}
	if cfg.DeepSeekAPIKey == "" {
		log.Printf("DEEPSEEK_API_KEY empty: using mock LLM outputs")
	}

	run, err := workflow.BuildScreeningGraph(ctx, &workflow.GraphDeps{Store: st, Router: router})
	if err != nil {
		log.Fatalf("compile graph: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/screen", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req domain.ScreeningRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		t0 := time.Now()
		out, err := run.Invoke(r.Context(), req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out.TotalDurationMs = time.Since(t0).Milliseconds()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/v1/screen/batch", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var reqs []domain.ScreeningRequest
		if err := json.NewDecoder(r.Body).Decode(&reqs); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		t0 := time.Now()
		results, errs := batch.ScreenConcurrent(r.Context(), run, reqs, 4)
		type row struct {
			Result domain.ScreeningResult `json:"result,omitempty"`
			Error  string                 `json:"error,omitempty"`
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
	})

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: logReq(mux)}
	go func() {
		log.Printf("listening %s", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	shutdownCtx, c2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer c2()
	_ = srv.Shutdown(shutdownCtx)
}

func logReq(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t0 := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(t0))
	})
}
