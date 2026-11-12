// Package demo 提供最小示例工具：一个只读工具和一个需要审批的写工具。
//
// 所有路径都被限制在指定目录内，不会操作真实业务资源。
package demo

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gooncall-agent/internal/tools"
)

// maxReadBytes 限制示例文件读取大小，避免把超大内容塞进模型上下文。
const maxReadBytes = 1 << 20

// ReadFileTool 返回读取示例目录内文本文件的工具。
func ReadFileTool(dir string) tools.Tool {
	return tools.Tool{
		Name:        "demo_read_file",
		Description: "读取示例目录内的文本文件，路径为相对示例目录的相对路径。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "相对于示例目录的文件路径"}
			},
			"required": ["path"]
		}`),
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(raw, &args); err != nil || args.Path == "" {
				return "", fmt.Errorf("path 不能为空")
			}
			path, err := resolve(dir, args.Path)
			if err != nil {
				return "", err
			}

			info, err := os.Stat(path)
			if err != nil {
				return "", fmt.Errorf("读取文件: %w", err)
			}
			if info.IsDir() {
				return "", fmt.Errorf("%s 是目录", args.Path)
			}
			if info.Size() > maxReadBytes {
				return "", fmt.Errorf("文件超过 %d 字节上限", maxReadBytes)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("读取文件: %w", err)
			}
			return string(data), nil
		},
	}
}

// WriteFileTool 返回写入示例目录内文件的工具，需要服务端审批。
func WriteFileTool(dir string) tools.Tool {
	return tools.Tool{
		Name:             "demo_write_file",
		Description:      "在示例目录内写入文本文件。该操作需要人工审批，路径为相对示例目录的相对路径。",
		RequiresApproval: true,
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": {"type": "string", "description": "相对于示例目录的文件路径"},
				"content": {"type": "string", "description": "写入的文本内容"}
			},
			"required": ["path", "content"]
		}`),
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(raw, &args); err != nil || args.Path == "" {
				return "", fmt.Errorf("path 不能为空")
			}
			path, err := resolve(dir, args.Path)
			if err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return "", fmt.Errorf("创建目录: %w", err)
			}

			if err := os.WriteFile(path, []byte(args.Content), 0o600); err != nil {
				// 写入可能已经部分生效：能读回预期内容则视为成功，否则标记结果未知。
				if data, readErr := os.ReadFile(path); readErr == nil && string(data) == args.Content {
					return fmt.Sprintf("已写入 %s（%d 字节）", args.Path, len(args.Content)), nil
				}
				return "", tools.Unknown(fmt.Errorf("写入文件: %w", err))
			}
			return fmt.Sprintf("已写入 %s（%d 字节）", args.Path, len(args.Content)), nil
		},
	}
}

// resolve 只接受示例目录内的相对路径。
func resolve(dir, name string) (string, error) {
	if !filepath.IsLocal(name) {
		return "", fmt.Errorf("路径必须位于示例目录内: %s", name)
	}
	return filepath.Join(dir, name), nil
}
