package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"sync"

	"gooncall-agent/internal/llm"
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Registry 保存工具定义，支持注册、查询、列出与统一执行。
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register 注册工具；名称重复或定义不完整时返错。
func (r *Registry) Register(t Tool) error {
	if !namePattern.MatchString(t.Name) {
		return fmt.Errorf("工具名称 %q 不合法", t.Name)
	}
	if t.Description == "" {
		return fmt.Errorf("工具 %q 缺少描述", t.Name)
	}
	if t.Run == nil {
		return fmt.Errorf("工具 %q 缺少执行入口", t.Name)
	}
	if len(t.Parameters) == 0 {
		t.Parameters = json.RawMessage(`{"type":"object","properties":{}}`)
	} else if !json.Valid(t.Parameters) {
		return fmt.Errorf("工具 %q 的参数 Schema 不是有效 JSON", t.Name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name]; exists {
		return fmt.Errorf("工具 %q 已注册", t.Name)
	}
	r.tools[t.Name] = t
	return nil
}

// Get 按名称查询工具。
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Names 按名称排序列出已注册工具名。
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// List 按名称排序列出全部工具，保证提示词顺序稳定。
func (r *Registry) List() []Tool {
	names := r.Names()

	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(names))
	for _, name := range names {
		out = append(out, r.tools[name])
	}
	return out
}

// Definitions 返回提供给模型的工具定义。
func (r *Registry) Definitions() []llm.Tool {
	list := r.List()
	out := make([]llm.Tool, 0, len(list))
	for _, t := range list {
		out = append(out, llm.Tool{
			Type: "function",
			Function: llm.FunctionDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return out
}

// Execute 是工具的统一执行入口：检查存在性、解析参数，再调用工具。
func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) (string, error) {
	t, ok := r.Get(name)
	if !ok {
		return "", ErrUnknownTool
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	var object map[string]any
	if err := json.Unmarshal(args, &object); err != nil || object == nil {
		return "", ErrInvalidArgs
	}
	return t.Run(ctx, args)
}
