// Package config 加载通用 Agent 服务的配置。
//
// 配置文件沿用 configs/config.env（YAML 格式）。加载时先展开 ${VAR} 占位符，
// 再由环境变量覆盖若干运行参数；模型 API Key 可直接写 api_key，或通过
// api_key_env 引用环境变量名。密钥不会写入日志或接口响应。
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是配置文件根结构。
type Config struct {
	Server       ServerConfig       `yaml:"server"`
	LLM          LLMConfig          `yaml:"llm"`
	Models       ModelsConfig       `yaml:"models"`
	Embedding    EmbeddingConfig    `yaml:"embedding"`
	Prometheus   PrometheusConfig   `yaml:"prometheus"`
	RabbitMQ     RabbitMQConfig     `yaml:"rabbitmq"`
	Qdrant       QdrantConfig       `yaml:"qdrant"`
	VectorStore  VectorStoreConfig  `yaml:"vector_store"`
	RAG          RAGConfig          `yaml:"rag"`
	Agent        AgentConfig        `yaml:"agent"`
	AgentService AgentServiceConfig `yaml:"agent_service"`
	Auth         AuthConfig         `yaml:"auth"`
	Tool         ToolConfig         `yaml:"tool"`
}

// ServerConfig 保留旧监听字段，新服务使用 agent_service.listen_addr。
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// LLMConfig 是单模型配置；未配置 models 时作为默认连接。
type LLMConfig struct {
	Provider string `yaml:"provider"`
	BaseURL  string `yaml:"base_url"`
	APIKey   string `yaml:"api_key"`
	Model    string `yaml:"model"`
}

// ModelsConfig 定义多个具名模型连接，调用方显式选择，不自动路由。
type ModelsConfig struct {
	Default     string                 `yaml:"default"`
	Connections map[string]ModelConfig `yaml:"connections"`
}

// ModelConfig 是单个 OpenAI 兼容连接。
type ModelConfig struct {
	BaseURL         string `yaml:"base_url"`
	APIKey          string `yaml:"api_key"`
	APIKeyEnv       string `yaml:"api_key_env"`
	Model           string `yaml:"model"`
	ContextWindow   int    `yaml:"context_window"`
	MaxOutputTokens int    `yaml:"max_output_tokens"`
}

// EmbeddingConfig 是向量模型配置。
type EmbeddingConfig struct {
	Provider string `yaml:"provider"`
	BaseURL  string `yaml:"base_url"`
	APIKey   string `yaml:"api_key"`
	Model    string `yaml:"model"`
}

// PrometheusConfig 是只读监控查询配置。
type PrometheusConfig struct {
	Enabled bool   `yaml:"enabled"`
	URL     string `yaml:"url"`
}

// RabbitMQConfig 是只读队列状态查询配置；URL 字段仅为旧配置兼容保留。
type RabbitMQConfig struct {
	Enabled       bool   `yaml:"enabled"`
	URL           string `yaml:"url"`
	ManagementURL string `yaml:"management_url"`
	Username      string `yaml:"username"`
	Password      string `yaml:"password"`
}

// QdrantConfig 是向量库配置。
type QdrantConfig struct {
	URL        string `yaml:"url"`
	Collection string `yaml:"collection"`
	Dim        uint64 `yaml:"dim"`
}

// VectorStoreConfig 是向量存储类型；首版仅支持 qdrant。
type VectorStoreConfig struct {
	Provider string `yaml:"provider"`
}

// RAGConfig 是 Runbook 混合检索配置。
type RAGConfig struct {
	Enabled    bool           `yaml:"enabled"`
	TopK       int            `yaml:"top_k"`
	CandidateK int            `yaml:"candidate_k"`
	DocsDir    string         `yaml:"docs_dir"`
	Reranker   RerankerConfig `yaml:"reranker"`
}

// RerankerConfig 是可选候选重排配置。
type RerankerConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Provider string `yaml:"provider"`
	BaseURL  string `yaml:"base_url"`
	APIKey   string `yaml:"api_key"`
	Model    string `yaml:"model"`
}

// 重排服务类型。
const (
	RerankerProviderTEI  = "tei"
	RerankerProviderQwen = "qwen"
)

// AgentConfig 是旧诊断链路的预算字段，仅作为新服务默认值来源。
type AgentConfig struct {
	MaxSteps       int `yaml:"max_steps"`
	MaxToolCalls   int `yaml:"max_tool_calls"`
	TimeoutSeconds int `yaml:"timeout_seconds"`
}

// AgentServiceConfig 是通用 Agent 服务自身配置。
type AgentServiceConfig struct {
	ListenAddr             string `yaml:"listen_addr"`
	DBPath                 string `yaml:"db_path"`
	DemoDir                string `yaml:"demo_dir"`
	DemoToolsEnabled       bool   `yaml:"demo_tools_enabled"`
	SystemPrompt           string `yaml:"system_prompt"`
	MaxRounds              int    `yaml:"max_rounds"`
	RunBudgetSeconds       int    `yaml:"run_budget_seconds"`
	ApprovalTTLSeconds     int    `yaml:"approval_ttl_seconds"`
	SubscriberBuffer       int    `yaml:"subscriber_buffer"`
	ShutdownTimeoutSeconds int    `yaml:"shutdown_timeout_seconds"`
	UserStatusPollSeconds  int    `yaml:"user_status_poll_seconds"`
}

// AuthConfig 是登录会话与登录限流配置。旧固定 Token 配置已移除。
type AuthConfig struct {
	SessionTTLSeconds    int `yaml:"session_ttl_seconds"`
	LoginMaxAttempts     int `yaml:"login_max_attempts"`
	LoginWindowSeconds   int `yaml:"login_window_seconds"`
	LoginLimiterCapacity int `yaml:"login_limiter_capacity"`
}

// ToolConfig 是业务工具执行限制。
type ToolConfig struct {
	TimeoutSeconds int `yaml:"timeout_seconds"`
	MaxResultBytes int `yaml:"max_result_bytes"`
}

// Connection 是解析后的模型连接。
type Connection struct {
	Name            string
	BaseURL         string
	APIKey          string
	Model           string
	ContextWindow   int
	MaxOutputTokens int
}

const defaultSystemPrompt = "你是一个通用运维助手。回答简洁准确；需要获取或修改外部信息时使用提供的工具。" +
	"工具返回错误时先说明原因，再决定是否调整参数。检索知识时说明来源。"

// Load 读取并校验配置。path 为空时依次尝试 AGENT_CONFIG、configs/config.env。
func Load(path string) (*Config, error) {
	if path == "" {
		path = os.Getenv("AGENT_CONFIG")
	}
	if path == "" {
		path = "configs/config.env"
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件 %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal([]byte(os.ExpandEnv(string(raw))), &cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件 %s: %w", path, err)
	}

	cfg.applyEnv()
	cfg.applyDefaults()
	return &cfg, nil
}

// Validate 校验新服务必需配置与已启用业务能力的配置完整性。
func (c *Config) Validate() error {
	if _, _, err := c.Connections(); err != nil {
		return err
	}
	if c.Auth.SessionTTLSeconds <= 0 || c.Auth.LoginMaxAttempts <= 0 ||
		c.Auth.LoginWindowSeconds <= 0 || c.Auth.LoginLimiterCapacity <= 0 {
		return errors.New("auth 会话与登录限制配置必须为正数")
	}
	if c.AgentService.UserStatusPollSeconds <= 0 {
		return errors.New("agent_service.user_status_poll_seconds 必须为正数")
	}

	if c.Prometheus.Enabled && strings.TrimSpace(c.Prometheus.URL) == "" {
		return errors.New("prometheus.enabled=true 时必须配置 prometheus.url")
	}
	if c.RabbitMQ.Enabled && strings.TrimSpace(c.RabbitMQ.ManagementURL) == "" {
		return errors.New("rabbitmq.enabled=true 时必须配置 rabbitmq.management_url")
	}
	if c.RAG.Enabled {
		return c.ValidateRAG()
	}
	return nil
}

// ValidateRAG 只校验知识检索相关配置，供导入与评测命令复用。
func (c *Config) ValidateRAG() error {
	if !c.RAG.Enabled {
		return errors.New("rag.enabled=true 后才能使用知识检索或导入命令")
	}
	if strings.TrimSpace(c.Embedding.BaseURL) == "" || strings.TrimSpace(c.Embedding.Model) == "" {
		return errors.New("rag.enabled=true 时必须配置 embedding.base_url 和 embedding.model")
	}
	if strings.TrimSpace(c.Qdrant.URL) == "" || c.Qdrant.Dim == 0 {
		return errors.New("rag.enabled=true 时必须配置 qdrant.url 和 qdrant.dim")
	}
	if c.VectorStore.Provider != "qdrant" {
		return errors.New("rag.enabled=true 时 vector_store.provider 必须为 qdrant")
	}
	if c.RAG.Reranker.Enabled {
		switch strings.ToLower(strings.TrimSpace(c.RAG.Reranker.Provider)) {
		case "", RerankerProviderTEI:
			if strings.TrimSpace(c.RAG.Reranker.BaseURL) == "" {
				return errors.New("启用 TEI 重排时必须配置 rag.reranker.base_url")
			}
		case RerankerProviderQwen:
			if strings.TrimSpace(c.RAG.Reranker.BaseURL) == "" ||
				strings.TrimSpace(c.RAG.Reranker.Model) == "" ||
				strings.TrimSpace(c.RAG.Reranker.APIKey) == "" {
				return errors.New("启用 Qwen 重排时必须配置 rag.reranker.base_url、model 和 api_key")
			}
		default:
			return errors.New("rag.reranker.provider 必须为 tei 或 qwen")
		}
	}
	return nil
}

// Connections 返回模型连接和默认连接名。
func (c *Config) Connections() (map[string]Connection, string, error) {
	if len(c.Models.Connections) > 0 {
		out := make(map[string]Connection, len(c.Models.Connections))
		for name, mc := range c.Models.Connections {
			conn, err := mc.resolve(name)
			if err != nil {
				return nil, "", err
			}
			out[name] = conn
		}

		def := c.Models.Default
		if def == "" && len(out) == 1 {
			for name := range out {
				def = name
			}
		}
		if def == "" {
			return nil, "", errors.New("配置了多个模型连接时必须指定 models.default")
		}
		if _, ok := out[def]; !ok {
			return nil, "", fmt.Errorf("models.default 指向不存在的连接 %q", def)
		}
		return out, def, nil
	}

	if c.LLM.BaseURL == "" || c.LLM.Model == "" {
		return nil, "", errors.New("未配置 models.connections，也没有可用的 llm 默认连接")
	}
	conn := Connection{
		Name:            "default",
		BaseURL:         c.LLM.BaseURL,
		APIKey:          c.LLM.APIKey,
		Model:           c.LLM.Model,
		ContextWindow:   32768,
		MaxOutputTokens: 2048,
	}
	return map[string]Connection{"default": conn}, "default", nil
}

// SessionTTL 返回登录会话有效期。
func (c *Config) SessionTTL() time.Duration {
	return time.Duration(c.Auth.SessionTTLSeconds) * time.Second
}

// LoginWindow 返回登录失败计数窗口。
func (c *Config) LoginWindow() time.Duration {
	return time.Duration(c.Auth.LoginWindowSeconds) * time.Second
}

// ToolTimeout 返回单次业务工具调用超时。
func (c *Config) ToolTimeout() time.Duration {
	return time.Duration(c.Tool.TimeoutSeconds) * time.Second
}

// UserStatusPoll 返回禁用用户活动运行的检测周期。
func (c *Config) UserStatusPoll() time.Duration {
	return time.Duration(c.AgentService.UserStatusPollSeconds) * time.Second
}

// RunBudget 返回整轮执行预算。
func (c *Config) RunBudget() time.Duration {
	return time.Duration(c.AgentService.RunBudgetSeconds) * time.Second
}

// ApprovalTTL 返回审批有效期。
func (c *Config) ApprovalTTL() time.Duration {
	return time.Duration(c.AgentService.ApprovalTTLSeconds) * time.Second
}

// ShutdownTimeout 返回优雅退出等待时间。
func (c *Config) ShutdownTimeout() time.Duration {
	return time.Duration(c.AgentService.ShutdownTimeoutSeconds) * time.Second
}

func (m ModelConfig) resolve(name string) (Connection, error) {
	key := m.APIKey
	if m.APIKeyEnv != "" {
		key = os.Getenv(m.APIKeyEnv)
		if key == "" {
			return Connection{}, fmt.Errorf("模型连接 %q 引用的环境变量 %s 未设置", name, m.APIKeyEnv)
		}
	}
	if m.BaseURL == "" {
		return Connection{}, fmt.Errorf("模型连接 %q 缺少 base_url", name)
	}
	if m.Model == "" {
		return Connection{}, fmt.Errorf("模型连接 %q 缺少 model", name)
	}

	ctxWindow := m.ContextWindow
	if ctxWindow <= 0 {
		ctxWindow = 32768
	}
	maxOutput := m.MaxOutputTokens
	if maxOutput <= 0 {
		maxOutput = 2048
	}
	if maxOutput >= ctxWindow {
		return Connection{}, fmt.Errorf("模型连接 %q 的 max_output_tokens 必须小于 context_window", name)
	}

	return Connection{
		Name:            name,
		BaseURL:         m.BaseURL,
		APIKey:          key,
		Model:           m.Model,
		ContextWindow:   ctxWindow,
		MaxOutputTokens: maxOutput,
	}, nil
}

func (c *Config) applyEnv() {
	setString(&c.AgentService.ListenAddr, os.Getenv("AGENT_LISTEN_ADDR"))
	setString(&c.AgentService.DBPath, os.Getenv("AGENT_DB_PATH"))
	setString(&c.AgentService.SystemPrompt, os.Getenv("AGENT_SYSTEM_PROMPT"))
	setString(&c.Models.Default, os.Getenv("AGENT_DEFAULT_MODEL"))

	if v := os.Getenv("AGENT_MAX_ROUNDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.AgentService.MaxRounds = n
		}
	}
	if v := os.Getenv("AGENT_RUN_BUDGET"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.AgentService.RunBudgetSeconds = int(d.Seconds())
		}
	}
	if v := os.Getenv("AGENT_APPROVAL_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.AgentService.ApprovalTTLSeconds = int(d.Seconds())
		}
	}
	if v := os.Getenv("AGENT_SESSION_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Auth.SessionTTLSeconds = int(d.Seconds())
		}
	}
	if v := os.Getenv("AGENT_LOGIN_MAX_ATTEMPTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Auth.LoginMaxAttempts = n
		}
	}
	if v := os.Getenv("AGENT_TOOL_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Tool.TimeoutSeconds = int(d.Seconds())
		}
	}
	if v := os.Getenv("AGENT_RAG_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.RAG.Enabled = b
		}
	}
	if v := os.Getenv("AGENT_PROMETHEUS_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.Prometheus.Enabled = b
		}
	}
	if v := os.Getenv("AGENT_RABBITMQ_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.RabbitMQ.Enabled = b
		}
	}

	setString(&c.Prometheus.URL, os.Getenv("PROMETHEUS_URL"))
	setString(&c.RabbitMQ.ManagementURL, os.Getenv("RABBITMQ_MANAGEMENT_URL"))
	setString(&c.RabbitMQ.Username, os.Getenv("RABBITMQ_USERNAME"))
	setString(&c.RabbitMQ.Password, os.Getenv("RABBITMQ_PASSWORD"))
	setString(&c.Embedding.BaseURL, os.Getenv("EMBEDDING_BASE_URL"))
	setString(&c.Embedding.APIKey, os.Getenv("EMBEDDING_API_KEY"))
	setString(&c.Embedding.Model, os.Getenv("EMBEDDING_MODEL"))
	setString(&c.Qdrant.URL, os.Getenv("QDRANT_URL"))
	setString(&c.RAG.Reranker.BaseURL, os.Getenv("RERANK_BASE_URL"))
	setString(&c.RAG.Reranker.APIKey, os.Getenv("RERANK_API_KEY"))
	setString(&c.RAG.Reranker.Model, os.Getenv("RERANK_MODEL"))
}

func (c *Config) applyDefaults() {
	if c.AgentService.ListenAddr == "" {
		c.AgentService.ListenAddr = "127.0.0.1:8091"
	}
	if c.AgentService.DBPath == "" {
		c.AgentService.DBPath = filepath.Join("data", "agent.db")
	}
	if c.AgentService.DemoDir == "" {
		c.AgentService.DemoDir = filepath.Join(os.TempDir(), "gooncall-agent-demo")
	}
	if c.AgentService.SystemPrompt == "" {
		c.AgentService.SystemPrompt = defaultSystemPrompt
	}
	if c.AgentService.MaxRounds <= 0 {
		c.AgentService.MaxRounds = c.Agent.MaxSteps
		if c.AgentService.MaxRounds <= 0 {
			c.AgentService.MaxRounds = 8
		}
	}
	if c.AgentService.RunBudgetSeconds <= 0 {
		c.AgentService.RunBudgetSeconds = c.Agent.TimeoutSeconds
		if c.AgentService.RunBudgetSeconds <= 0 {
			c.AgentService.RunBudgetSeconds = 180
		}
	}
	if c.AgentService.ApprovalTTLSeconds <= 0 {
		c.AgentService.ApprovalTTLSeconds = 300
	}
	if c.AgentService.SubscriberBuffer <= 0 {
		c.AgentService.SubscriberBuffer = 64
	}
	if c.AgentService.ShutdownTimeoutSeconds <= 0 {
		c.AgentService.ShutdownTimeoutSeconds = 10
	}
	if c.AgentService.UserStatusPollSeconds <= 0 {
		c.AgentService.UserStatusPollSeconds = 2
	}
	if c.Auth.SessionTTLSeconds <= 0 {
		c.Auth.SessionTTLSeconds = 24 * 60 * 60
	}
	if c.Auth.LoginMaxAttempts <= 0 {
		c.Auth.LoginMaxAttempts = 5
	}
	if c.Auth.LoginWindowSeconds <= 0 {
		c.Auth.LoginWindowSeconds = 5 * 60
	}
	if c.Auth.LoginLimiterCapacity <= 0 {
		c.Auth.LoginLimiterCapacity = 1024
	}
	if c.Tool.TimeoutSeconds <= 0 {
		c.Tool.TimeoutSeconds = 30
	}
	if c.Tool.MaxResultBytes <= 0 {
		c.Tool.MaxResultBytes = 256 << 10
	}
	if c.RAG.TopK <= 0 {
		c.RAG.TopK = 8
	}
	if c.RAG.CandidateK <= 0 {
		c.RAG.CandidateK = 20
	}
	if c.RAG.DocsDir == "" {
		c.RAG.DocsDir = "docs"
	}
	if c.Qdrant.Collection == "" {
		c.Qdrant.Collection = "gooncall_knowledge"
	}
	if c.Qdrant.Dim == 0 {
		c.Qdrant.Dim = 768
	}
	if c.VectorStore.Provider == "" {
		c.VectorStore.Provider = "qdrant"
	}
}

func setString(dst *string, value string) {
	if value != "" {
		*dst = value
	}
}
