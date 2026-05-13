package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"fmt"
	"risk_control/config"
	"risk_control/llm"
	"risk_control/store"
	"risk_control/workflow"

	"risk_control/rpc"

	rpc2 "github.com/gorilla/rpc/v2"
	"github.com/gorilla/rpc/v2/json2"
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

	if err := st.EnsureSchema(context.Background()); err != nil {
		panic(fmt.Errorf("ensure schema: %v", err))
	}

	router, err := llm.NewRouter(ctx, *cfg)
	if err != nil {
		panic(fmt.Errorf("llm router: %v", err))
	}

	deps := &workflow.GraphDeps{Store: st, Router: router, Cfg: *cfg}
	eng, err := workflow.NewRiskEngine(ctx, deps)
	if err != nil {
		panic(fmt.Errorf("risk engine: %v", err))
	}

	s := rpc2.NewServer()
	s.RegisterCodec(json2.NewCodec(), "application/json")
	service := &rpc.Risk{Eng: eng}
	s.RegisterService(service, "")
	http.Handle("/rpc", s)

	server := &http.Server{
		Addr:    ":8080",
		Handler: s,
	}

	go func() {
		log.Println("JSON-RPC 2.0 server starting on :8080/rpc")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen and serve: %v", err)
		}
	}()

	// 等待取消信号
	<-ctx.Done()
	log.Println("shutting down server...")

	// 优雅关闭，等待正在处理的请求完成（设置超时）
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}
	log.Println("server stopped")

}
