// Package config 负责加载与解析 GoOnCall Agent 的运行配置。
//
// 配置使用 YAML 文件，并支持 ${ENV_VAR} 形式的环境变量替换。
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config 是 GoOnCall Agent 的全局配置。
type Config struct {
	Server       ServerConfig       `yaml:"server"`
	LLM          LLMConfig          `yaml:"llm"`
	Postgres     PostgresConfig     `yaml:"postgres"`
	Redis        RedisConfig        `yaml:"redis"`
	RabbitMQ     RabbitMQConfig     `yaml:"rabbitmq"`
	Prometheus   PrometheusConfig   `yaml:"prometheus"`
	Qdrant       QdrantConfig       `yaml:"qdrant"`
	RAG          RAGConfig          `yaml:"rag"`
	Agent        AgentConfig        `yaml:"agent"`
	Approval     ApprovalConfig     `yaml:"approval"`
	Verification VerificationConfig `yaml:"verification"`
	VectorStore  VectorStoreConfig  `yaml:"vector_store"`
	Tool         ToolConfig         `yaml:"tool"`
}

// ServerConfig 是 HTTP 服务配置。
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// LLMConfig 是大模型配置。
type LLMConfig struct {
	Provider       string `yaml:"provider"`
	BaseURL        string `yaml:"base_url"`
	APIKey         string `yaml:"api_key"`
	Model          string `yaml:"model"`
	EmbeddingModel string `yaml:"embedding_model"`
}

// PostgresConfig 是 PostgreSQL 配置。
type PostgresConfig struct {
	DSN string `yaml:"dsn"`
}

// RedisConfig 是 Redis 配置。
type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

// RabbitMQConfig 是 RabbitMQ 配置。
type RabbitMQConfig struct {
	URL           string `yaml:"url"`
	ManagementURL string `yaml:"management_url"`
	Username      string `yaml:"username"`
	Password      string `yaml:"password"`
}

// PrometheusConfig 是 Prometheus 配置。
type PrometheusConfig struct {
	URL string `yaml:"url"`
}

// QdrantConfig 是 Qdrant 向量库配置。
type QdrantConfig struct {
	URL        string `yaml:"url"`
	Collection string `yaml:"collection"`
	Dim        uint64 `yaml:"dim"`
}

// RAGConfig 是 RAG 混合检索配置。
type RAGConfig struct {
	// TopK 最终返回的结果条数（默认 8）。
	TopK int `yaml:"top_k"`
	// CandidateK 向量检索的候选集大小（默认 20）。
	// 远小于全量 chunks，避免每次查询都按整个知识库做向量 topK。
	CandidateK int `yaml:"candidate_k"`
}

// AgentConfig 是 Agent 运行时约束配置。
type AgentConfig struct {
	MaxSteps       int `yaml:"max_steps"`
	MaxToolCalls   int `yaml:"max_tool_calls"`
	TimeoutSeconds int `yaml:"timeout_seconds"`
}

// ApprovalConfig 是人工审批配置。
type ApprovalConfig struct {
	Enabled bool `yaml:"enabled"`
}

// VerificationConfig 是处置后验证配置。
type VerificationConfig struct {
	Mode string `yaml:"mode"`
}

// VectorStoreConfig 是向量存储配置。
type VectorStoreConfig struct {
	Provider string `yaml:"provider"`
}

// ToolConfig 是工具执行配置。
type ToolConfig struct {
	TimeoutSeconds int `yaml:"timeout_seconds"`
}

// Default 返回带安全默认值的配置。
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8080,
		},
		LLM: LLMConfig{
			Provider:       "openai",
			EmbeddingModel: "text-embedding-3-small",
		},
		Qdrant: QdrantConfig{
			Collection: "gooncall_knowledge",
			Dim:        1536,
		},
		RAG: RAGConfig{
			TopK:       8,
			CandidateK: 20,
		},
		Agent: AgentConfig{
			MaxSteps:       15,
			MaxToolCalls:   20,
			TimeoutSeconds: 180,
		},
		Approval: ApprovalConfig{
			Enabled: true,
		},
		Verification: VerificationConfig{
			Mode: "mock",
		},
		VectorStore: VectorStoreConfig{
			Provider: "memory",
		},
		Tool: ToolConfig{
			TimeoutSeconds: 30,
		},
	}
}

// Load 从指定路径加载 YAML 配置。
//
// 读取原始内容后先做 ${VAR} / $VAR 环境变量展开，再解析 YAML，
// 因此未在文件中出现的字段会保留 Default() 中的默认值。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	expanded := os.ExpandEnv(string(data))

	cfg := Default()
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return cfg, nil
}
