package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// TestRegistryRegisterAndExecute 覆盖注册、重复注册、未知工具与非法参数。
func TestRegistryRegisterAndExecute(t *testing.T) {
	registry := NewRegistry()
	tool := Tool{
		Name:        "echo",
		Description: "回显参数",
		Run: func(_ context.Context, args json.RawMessage) (string, error) {
			return string(args), nil
		},
	}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.Register(tool); err == nil {
		t.Fatal("重复注册应当失败")
	}

	out, err := registry.Execute(context.Background(), "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != `{"text":"hi"}` {
		t.Fatalf("out = %s", out)
	}

	if _, err := registry.Execute(context.Background(), "missing", nil); !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("未知工具错误 = %v", err)
	}
	if _, err := registry.Execute(context.Background(), "echo", json.RawMessage(`[]`)); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("非法参数错误 = %v", err)
	}
}
