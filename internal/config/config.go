// Package config 负责加载与解析 GoOnCall Agent 的运行配置。
//
// 配置使用 YAML 文件，并支持 ${ENV_VAR} 形式的环境变量替换。
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config 是 GoOnCall Agent 的全局配置。
type Config struct {
	Server       ServerConfig       `yaml:"server"`
	LLM          LLMConfig          `yaml:"llm"`
	Embedding    EmbeddingConfig    `yaml:"embedding"`
	Postgres     PostgresConfig     `yaml:"postgres"`
	Redis        RedisConfig        `yaml:"redis"`
	RabbitMQ     RabbitMQConfig     `yaml:"rabbitmq"`
	Prometheus   PrometheusConfig   `yaml:"prometheus"`
	Qdrant       QdrantConfig       `yaml:"qdrant"`
	RAG          RAGConfig          `yaml:"rag"`
	Agent        AgentConfig        `yaml:"agent"`
	Approval     ApprovalConfig     `yaml:"approval"`
	Auth         AuthConfig         `yaml:"auth"`
	Execution    ExecutionConfig    `yaml:"execution"`
	Verification VerificationConfig `yaml:"verification"`
	VectorStore  VectorStoreConfig  `yaml:"vector_store"`
	Tool         ToolConfig         `yaml:"tool"`
}

// AuthConfig 是三类最小认证配置。
//
// Token 为空表示该类认证未启用，这**只允许**出现在显式 Demo 配置里（execution.mode=mock）。
// execution.mode=http 时三个 Token 都必须非空，否则 Validate 拒绝启动，
// 避免"真实处置 + 无认证"的危险组合。
type AuthConfig struct {
	// WebhookToken 校验 Alertmanager webhook（X-GoOnCall-Webhook-Token）。
	WebhookToken string `yaml:"webhook_token"`
	// ReadToken 校验读接口（Authorization: Bearer <token>）。
	ReadToken string `yaml:"read_token"`
	// AdminToken 校验批准 / 拒绝；admin 同时拥有读权限。
	AdminToken string `yaml:"admin_token"`
	// AdminActor 是批准 / 拒绝落库的 approved_by：服务端配置，不信任请求体。
	AdminActor string `yaml:"admin_actor"`
}

// ExecutionConfig 是真实处置（restart_worker）配置。
type ExecutionConfig struct {
	// Mode 为 mock（模拟重启，本地 Demo）或 http（固定 URL 的真实处置适配器）。
	Mode string `yaml:"mode"`
	// RestartURL 是固定的处置 endpoint：不可由 LLM 提供。
	RestartURL string `yaml:"restart_url"`
	// Token 是处置 endpoint 的 Bearer Token，不写入日志、错误或响应。
	Token string `yaml:"token"`
	// AllowedTargets 是允许重启的 target 白名单；http 模式必须非空。
	AllowedTargets CSVList `yaml:"allowed_targets"`
	// TimeoutSeconds 是单次处置 HTTP 调用超时。
	TimeoutSeconds int `yaml:"timeout_seconds"`
}

// CSVList 同时接受 YAML 列表与逗号分隔字符串。
//
// ${VAR} 展开后必然是字符串（如 `worker-a,worker-b`），因此列表形式在只用
// 环境变量配置时必须也能解析成切片。
type CSVList []string

// UnmarshalYAML 解析 `a,b` 或 `[a, b]`。
func (l *CSVList) UnmarshalYAML(value *yaml.Node) error {
	var raw string
	if err := value.Decode(&raw); err == nil {
		*l = nil
		for _, item := range strings.Split(raw, ",") {
			if item = strings.TrimSpace(item); item != "" {
				*l = append(*l, item)
			}
		}
		return nil
	}
	var items []string
	if err := value.Decode(&items); err != nil {
		return err
	}
	*l = items
	return nil
}

// ServerConfig 是 HTTP 服务配置。
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// LLMConfig 是诊断大模型配置。
type LLMConfig struct {
	Provider string `yaml:"provider"`
	BaseURL  string `yaml:"base_url"`
	APIKey   string `yaml:"api_key"`
	Model    string `yaml:"model"`
}

// EmbeddingConfig 是向量模型配置，与诊断大模型独立。
type EmbeddingConfig struct {
	Provider string `yaml:"provider"`
	BaseURL  string `yaml:"base_url"`
	APIKey   string `yaml:"api_key"`
	Model    string `yaml:"model"`
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
	CandidateK int            `yaml:"candidate_k"`
	Reranker   RerankerConfig `yaml:"reranker"`
}

// RerankerConfig 是可选的候选重排配置。
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
	// Mode 为 mock（本地 Demo 固定指标）或 prometheus（实时查询验证指标）。
	Mode string `yaml:"mode"`
	// Attempts 是最多采集验证次数。
	Attempts int `yaml:"attempts"`
	// IntervalSeconds 是两次采集之间的间隔。
	IntervalSeconds int `yaml:"interval_seconds"`
	// RequiredSuccesses 是判定恢复所需的**连续**通过次数。
	RequiredSuccesses int `yaml:"required_successes"`
	// Queries 是验证指标对应的 PromQL（Mode=prometheus 时生效）。
	Queries VerificationQueries `yaml:"queries"`
}

// 验证默认值（契约 3.5）。required_successes 超过 attempts 会被 Validate 拒绝，
// 不会静默降低安全强度。
const (
	// DefaultVerifyAttempts 是默认最多采集次数。
	DefaultVerifyAttempts = 3
	// DefaultVerifyIntervalSeconds 是默认采集间隔（秒）。
	DefaultVerifyIntervalSeconds = 10
	// DefaultVerifyRequiredSuccesses 是默认所需连续通过次数。
	DefaultVerifyRequiredSuccesses = 3
)

// 处置模式取值（execution.mode）。
const (
	// ExecutionModeMock 使用模拟重启（仅本地 Demo）。
	ExecutionModeMock = "mock"
	// ExecutionModeHTTP 使用固定 URL 的真实处置适配器。
	ExecutionModeHTTP = "http"
)

// VerificationQueries 是验证三项指标（消费者数 / 队列积压 / 5xx 错误率）的 PromQL。
// 对接不同业务系统时按实际指标名覆盖（如 GoCommunity 的 resource_community_http_*）。
type VerificationQueries struct {
	Consumers  string `yaml:"consumers"`
	QueueDepth string `yaml:"queue_depth"`
	ErrorRate  string `yaml:"error_rate"`
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
			Provider: "openai",
		},
		Embedding: EmbeddingConfig{
			Provider: "openai",
			Model:    "nomic-embed-text:latest",
		},
		Qdrant: QdrantConfig{
			Collection: "gooncall_knowledge",
			Dim:        768,
		},
		RAG: RAGConfig{
			TopK:       8,
			CandidateK: 20,
			Reranker: RerankerConfig{
				Provider: RerankerProviderTEI,
			},
		},
		Agent: AgentConfig{
			MaxSteps:       15,
			MaxToolCalls:   20,
			TimeoutSeconds: 180,
		},
		Approval: ApprovalConfig{
			Enabled: true,
		},
		Auth: AuthConfig{
			AdminActor: "gooncall-admin",
		},
		Execution: ExecutionConfig{
			Mode:           ExecutionModeMock,
			TimeoutSeconds: 30,
		},
		Verification: VerificationConfig{
			Mode:              "mock",
			Attempts:          DefaultVerifyAttempts,
			IntervalSeconds:   DefaultVerifyIntervalSeconds,
			RequiredSuccesses: DefaultVerifyRequiredSuccesses,
			Queries: VerificationQueries{
				Consumers:  "sum(rabbitmq_queue_consumers)",
				QueueDepth: "sum(rabbitmq_queue_messages_ready)",
				ErrorRate:  `sum(rate(http_requests_total{status=~"5.."}[1m])) / clamp_min(sum(rate(http_requests_total[1m])), 0.001)`,
			},
		},
		VectorStore: VectorStoreConfig{
			Provider: "qdrant",
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
	var explicit struct {
		Execution struct {
			Mode *string `yaml:"mode"`
		} `yaml:"execution"`
	}
	if err := yaml.Unmarshal([]byte(expanded), &explicit); err != nil {
		return nil, fmt.Errorf("parse execution mode: %w", err)
	}
	if explicit.Execution.Mode == nil || strings.TrimSpace(*explicit.Execution.Mode) == "" {
		return nil, errors.New("execution.mode must be explicitly set to mock or http")
	}

	// 环境变量覆盖：SERVER_PORT 覆盖 server.port。
	// 本机与 GoCommunity（占用 8080）等共存时，用 SERVER_PORT=8082 运行 API。
	// （${VAR} 展开不支持默认值语法，故端口覆盖在代码里显式处理。）
	if v := os.Getenv("SERVER_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p <= 0 || p >= 65536 {
			return nil, fmt.Errorf("parse SERVER_PORT %q: %w", v, err)
		}
		cfg.Server.Port = p
	}

	// verification.mode 留空时只在显式 mock Demo 下使用 mock。
	if cfg.Verification.Mode == "" {
		cfg.Verification.Mode = "mock"
	}

	// 危险组合在启动时直接失败，而不是运行到一半才发现。
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate 校验配置组合，拒绝会削弱闭环可靠性的取值。
//
// 错误信息只描述缺什么、不允许什么，不回显 Token 等敏感值。
func (c *Config) Validate() error {
	switch c.Execution.Mode {
	case ExecutionModeMock:
	case ExecutionModeHTTP:
	default:
		return fmt.Errorf("execution.mode %q is invalid, want %q or %q",
			c.Execution.Mode, ExecutionModeMock, ExecutionModeHTTP)
	}

	switch c.Verification.Mode {
	case "mock":
	case "prometheus":
		if c.Prometheus.URL == "" {
			return fmt.Errorf("verification.mode=prometheus requires prometheus.url")
		}
	default:
		return fmt.Errorf("verification.mode %q is invalid, want \"mock\" or \"prometheus\"", c.Verification.Mode)
	}

	if c.Verification.Attempts < 1 {
		return fmt.Errorf("verification.attempts must be >= 1")
	}
	if c.Verification.RequiredSuccesses < 1 {
		return fmt.Errorf("verification.required_successes must be >= 1")
	}
	if c.Verification.RequiredSuccesses > c.Verification.Attempts {
		// 不能降级成"验证一次就通过"：这类配置必须在启动时被拒绝。
		return fmt.Errorf("verification.required_successes (%d) must not exceed verification.attempts (%d)",
			c.Verification.RequiredSuccesses, c.Verification.Attempts)
	}
	if c.Verification.IntervalSeconds < 0 {
		return fmt.Errorf("verification.interval_seconds must be >= 0")
	}
	if c.RAG.TopK < 1 {
		return fmt.Errorf("rag.top_k must be >= 1")
	}
	if c.RAG.CandidateK < 1 {
		return fmt.Errorf("rag.candidate_k must be >= 1")
	}
	if c.RAG.Reranker.Enabled {
		if strings.TrimSpace(c.RAG.Reranker.BaseURL) == "" {
			return fmt.Errorf("rag.reranker.base_url is required when reranker is enabled")
		}
		switch strings.ToLower(strings.TrimSpace(c.RAG.Reranker.Provider)) {
		case "", RerankerProviderTEI:
		case RerankerProviderQwen:
			if strings.TrimSpace(c.RAG.Reranker.APIKey) == "" {
				return fmt.Errorf("rag.reranker.api_key is required when reranker.provider=qwen")
			}
			if strings.TrimSpace(c.RAG.Reranker.Model) == "" {
				return fmt.Errorf("rag.reranker.model is required when reranker.provider=qwen")
			}
		default:
			return fmt.Errorf("rag.reranker.provider must be tei or qwen")
		}
	}

	if c.Execution.Mode != ExecutionModeHTTP {
		return nil
	}

	// 真实处置路径的硬性要求：没有它们就不允许启动。
	if c.Execution.RestartURL == "" {
		return fmt.Errorf("execution.restart_url is required when execution.mode=%s", ExecutionModeHTTP)
	}
	if len(c.Execution.AllowedTargets) == 0 {
		return fmt.Errorf("execution.allowed_targets is required when execution.mode=%s", ExecutionModeHTTP)
	}
	if c.Verification.Mode != "prometheus" {
		return fmt.Errorf("execution.mode=%s requires verification.mode=prometheus (mock verification must not close a real incident)",
			ExecutionModeHTTP)
	}
	if c.Auth.WebhookToken == "" || c.Auth.ReadToken == "" || c.Auth.AdminToken == "" {
		return fmt.Errorf("auth.webhook_token, auth.read_token and auth.admin_token are required when execution.mode=%s",
			ExecutionModeHTTP)
	}
	if c.Postgres.DSN == "" {
		return fmt.Errorf("postgres.dsn is required when execution.mode=%s", ExecutionModeHTTP)
	}
	if c.RabbitMQ.URL == "" {
		return fmt.Errorf("rabbitmq.url is required when execution.mode=%s", ExecutionModeHTTP)
	}
	if c.LLM.BaseURL == "" || c.LLM.Model == "" {
		return fmt.Errorf("llm.base_url and llm.model are required when execution.mode=%s", ExecutionModeHTTP)
	}
	return nil
}
