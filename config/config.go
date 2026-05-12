package config

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Config 演示环境变量。
type Config struct {
	HTTPAddr string `json:"httpaddr"`

	MySQLDSN string `json:"mysqldsn"`

	DeepSeekAPIKey        string        `json:"deepSeekAPIKey"`
	DeepSeekBaseURL       string        `json:"deepSeekBaseURL"`
	ModelPrimary          string        `json:"modelPrimary"`
	ModelVerify           string        `json:"modelVerify"`
	ModelReport           string        `json:"modelReport"`
	LLMTimeout            time.Duration `json:"llmTimeout"`
	SysPrompt             string        `json:"sysPrompt"`
	UserPrompt            string        `json:"userPrompt"`
	VerifyPrompt          string        `json:"verifyPrompt"`
	ReportPrompt          string        `json:"reportPrompt"`
	PrimaryRiskScore      float64       `json:"primaryRiskScore"`
	PrimaryStockRiskScore float64       `json:"primaryStockRiskScore"`
	Workers               int           `json:"workers"`
	StockSysPrompt        string        `json:"stockSysPrompt"`
	StockUserPrompt       string        `json:"stockUserPrompt"`
	StockReportPrompt     string        `json:"stockReportPrompt"`
	StockVerifyPrompt     string        `json:"stockVerifyPrompt"`
}

var config Config
var once sync.Once

func init() {
	once.Do(func() {
		dir, err := os.Getwd()
		if err != nil {
			log.Fatalf("get current directory: %v", err)
		}
		cfgPath := filepath.Join(dir, "config.json")
		log.Printf("config file path: %s", cfgPath)
		cfg, err := os.ReadFile(cfgPath)
		if err != nil {
			log.Fatalf("read config file: %v", err)
		}
		err = json.Unmarshal(cfg, &config)
		if err != nil {
			log.Fatalf("unmarshal config: %v", err)
		}
		config.LLMTimeout = time.Duration(config.LLMTimeout) * time.Second
	})
}

func Load() *Config {
	return &config
}
