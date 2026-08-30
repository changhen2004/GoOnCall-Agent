// Package runtime 提供 Agent Runtime：将 Incident、LLM、工具串联为诊断执行闭环。
package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/flow/agent/react"

	"gooncall-agent/internal/agent/diagnosis"
	incidentmodel "gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
	"gooncall-agent/internal/tool/registry"
)

// DiagnosisResult 是诊断结果。
type DiagnosisResult struct {
	RunID      string `json:"run_id,omitempty"`
	Conclusion string `json:"conclusion"`
}

// Runtime 是 Agent 运行时，负责构建 ReAct Agent 并执行诊断。
type Runtime struct {
	chatModel    model.ToolCallingChatModel
	registry     *registry.Registry
	maxSteps     int
	runRepo      repository.RunRepository
	broker       *StreamBroker
	toolTimeout  time.Duration
	maxToolCalls int
}

// New 创建 Agent Runtime。
func New(chatModel model.ToolCallingChatModel, reg *registry.Registry, maxSteps int) *Runtime {
	if maxSteps <= 0 {
		maxSteps = 15
	}
	return &Runtime{
		chatModel: chatModel,
		registry:  reg,
		maxSteps:  maxSteps,
	}
}

// WithRunRecording 注入 Run 持久化与事件流，启用执行过程记录。
func (rt *Runtime) WithRunRecording(runRepo repository.RunRepository, broker *StreamBroker) *Runtime {
	rt.runRepo = runRepo
	rt.broker = broker
	return rt
}

// WithToolLimits 设置工具执行超时与最大调用次数。
func (rt *Runtime) WithToolLimits(timeout time.Duration, maxToolCalls int) *Runtime {
	if timeout > 0 {
		rt.toolTimeout = timeout
	}
	if maxToolCalls > 0 {
		rt.maxToolCalls = maxToolCalls
	}
	return rt
}

// Diagnose 对 Incident 执行诊断：Incident -> Agent -> Tool -> Result。
// 若启用 Run 记录，则创建 AgentRun 并记录步骤、工具调用与 SSE 事件。
func (rt *Runtime) Diagnose(ctx context.Context, inc *incidentmodel.Incident, systemPrompt string) (*DiagnosisResult, error) {
	var rec *Recorder
	if rt.runRepo != nil {
		rec = NewRecorder(rt.runRepo, rt.broker, inc, "diagnosis", rt.maxToolCalls)
		if err := rec.Start(ctx); err != nil {
			return nil, err
		}
	}

	agent := diagnosis.New(rt.chatModel, rt.maxSteps)
	reActAgent, err := agent.Build(ctx)
	if err != nil {
		if rec != nil {
			rec.Fail(ctx, err)
		}
		return nil, fmt.Errorf("build diagnosis agent: %w", err)
	}

	messages := diagnosis.BuildMessages(systemPrompt, inc)

	tools := rt.registry.EinoTools()
	if rec != nil {
		tools = wrapTools(rt.registry, rec, rt.toolTimeout)
		ctx = WithRunID(ctx, rec.RunID())
	}

	opts, err := react.WithTools(ctx, tools...)
	if err != nil {
		if rec != nil {
			rec.Fail(ctx, err)
		}
		return nil, fmt.Errorf("bind tools: %w", err)
	}

	result, err := reActAgent.Generate(ctx, messages, opts...)
	if err != nil {
		if rec != nil {
			rec.Fail(ctx, err)
		}
		return nil, fmt.Errorf("run diagnosis: %w", err)
	}

	if rec != nil {
		rec.Step(ctx, "diagnosis", "finalize", "", result.Content, 0)
		rec.Complete(ctx)
	}

	out := &DiagnosisResult{Conclusion: result.Content}
	if rec != nil {
		out.RunID = rec.RunID()
	}
	return out, nil
}
