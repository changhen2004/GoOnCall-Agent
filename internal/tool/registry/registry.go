// Package registry 管理工具注册与风险等级。
package registry

import (
	"context"
	"fmt"
	"sync"

	"github.com/cloudwego/eino/components/tool"
)

// RiskLevel 表示工具操作的风险等级（设计文档 8.4）。
type RiskLevel string

const (
	RiskLow      RiskLevel = "LOW"
	RiskMedium   RiskLevel = "MEDIUM"
	RiskHigh     RiskLevel = "HIGH"
	RiskCritical RiskLevel = "CRITICAL"
)

// RegisteredTool 是已注册工具及其元数据。
type RegisteredTool struct {
	Name      string
	Tool      tool.InvokableTool
	RiskLevel RiskLevel
}

// Registry 是线程安全的工具注册表。
type Registry struct {
	mu    sync.RWMutex
	tools map[string]RegisteredTool
	order []string
}

// New 创建一个空 Registry。
func New() *Registry {
	return &Registry{tools: make(map[string]RegisteredTool)}
}

// Register 注册一个工具，名字从工具 Info() 推导。
func (r *Registry) Register(t tool.InvokableTool, risk RiskLevel) error {
	info, err := t.Info(context.Background())
	if err != nil {
		return fmt.Errorf("get tool info: %w", err)
	}
	if info.Name == "" {
		return fmt.Errorf("tool name must not be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[info.Name]; ok {
		return fmt.Errorf("tool %q already registered", info.Name)
	}
	r.tools[info.Name] = RegisteredTool{Name: info.Name, Tool: t, RiskLevel: risk}
	r.order = append(r.order, info.Name)
	return nil
}

// Get 按名字查询工具。
func (r *Registry) Get(name string) (RegisteredTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// All 按注册顺序返回全部工具。
func (r *Registry) All() []RegisteredTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RegisteredTool, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.tools[name])
	}
	return out
}

// Names 返回全部工具名。
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.order))
	out = append(out, r.order...)
	return out
}

// EinoTools 返回注册工具对应的 Eino BaseTool，供 react.WithTools 使用。
func (r *Registry) EinoTools() []tool.BaseTool {
	all := r.All()
	out := make([]tool.BaseTool, 0, len(all))
	for _, t := range all {
		out = append(out, t.Tool)
	}
	return out
}
