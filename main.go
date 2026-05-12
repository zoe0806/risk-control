package main

import (
	"context"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"fmt"
	"risk_control/config"
	"risk_control/llm"
	"risk_control/rpc"
	"risk_control/store"
	"risk_control/workflow"
)

func main() {
	cfg := config.Load()
	if cfg.DeepSeekAPIKey == "" || cfg.MySQLDSN == "" || cfg.HTTPAddr == "" || cfg.ModelPrimary == "" {
		panic(fmt.Errorf("please check your config"))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	st, err := store.OpenMySQL(cfg.MySQLDSN)
	if err != nil {
		panic(fmt.Errorf("open mysql: %v", err))
	}
	defer st.Close()

	router, err := llm.NewRouter(ctx, *cfg)
	if err != nil {
		panic(fmt.Errorf("llm router: %v", err))
	}

	run, err := workflow.BuildScreeningGraph(ctx, &workflow.GraphDeps{Store: st, Router: router, Cfg: *cfg})
	if err != nil {
		panic(fmt.Errorf("compile graph: %v", err))
	}

	mux := http.NewServeMux()
	handler := rpc.RegisterRoutes(mux, run)
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: handler}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			panic(fmt.Errorf("listen and serve: %v", err))
		}
	}()
	<-ctx.Done()
	shutdownCtx, c2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer c2()
	_ = srv.Shutdown(shutdownCtx)
}
