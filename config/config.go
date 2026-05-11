package config

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"
)

// Config 演示环境变量。
type Config struct {
	HTTPAddr string `json:"httpaddr"`

	MySQLDSN string `json:"mysqldsn"`

	DeepSeekAPIKey  string        `json:"deepseekapikey"`
	DeepSeekBaseURL string        `json:"deepseekbaseurl"`
	ModelPrimary    string        `json:"modelprimary"`
	ModelVerify     string        `json:"modelverify"`
	ModelReport     string        `json:"modelreport"`
	LLMTimeout      time.Duration `json:"llmtimeout"`
	SysPrompt       string        `json:"sysprompt"`
	UserPrompt      string        `json:"userprompt"`
	VerifyPrompt    string        `json:"verifyprompt"`
	ReportPrompt    string        `json:"reportprompt"`
}

func Load() Config {
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
	var config Config
	err = json.Unmarshal(cfg, &config)
	if err != nil {
		log.Fatalf("unmarshal config: %v", err)
	}
	config.LLMTimeout = time.Duration(config.LLMTimeout) * time.Second
	return config
}
