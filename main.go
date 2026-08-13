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
	if cfg.MySQLDSN == "" || cfg.HTTPAddr == "" {
		panic(fmt.Errorf("please check mysqlDSN / httpAddr in config"))
	}
	kind := workflow.NormalizeDeepKind(cfg.DeepRuntime.Kind)
	needLLM := kind == workflow.DeepRuntimeNative || kind == workflow.DeepRuntimeEino
	if needLLM && (cfg.DeepSeekAPIKey == "" || cfg.ModelPrimary == "") {
		panic(fmt.Errorf("deep runtime %q requires deepSeekAPIKey and modelPrimary", kind))
	}
	if kind == workflow.DeepRuntimeCLI && cfg.DeepRuntime.CLI.Command == "" {
		panic(fmt.Errorf("deepRuntime.kind=cli requires deepRuntime.cli.command"))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	st, err := store.OpenMySQL(cfg.MySQLDSN)
	if err != nil {
		panic(fmt.Errorf("open mysql: %v", err))
	}
	defer st.Close()

	if err := st.EnsureSchema(context.Background()); err != nil {
		panic(fmt.Errorf("ensure schema: %v", err))
	}

	var router *llm.Router
	if needLLM {
		router, err = llm.NewRouter(ctx, *cfg)
		if err != nil {
			panic(fmt.Errorf("llm router: %v", err))
		}
	}

	deps := &workflow.GraphDeps{Store: st, Router: router, Cfg: *cfg}
	eng, err := workflow.NewRiskEngine(ctx, deps)
	if err != nil {
		panic(fmt.Errorf("risk engine: %v", err))
	}

	mux := http.NewServeMux()
	handler := rpc.RegisterRoutes(mux, eng)
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
