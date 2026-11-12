// Package tools 定义工具契约、注册表与统一执行入口。
package tools

import (
	"context"
	"encoding/json"
	"errors"
)

// Tool 是一个可被模型调用的工具。审批属性是静态定义，不受模型参数影响。
//
// Parameters 只用于向模型描述输入；执行前仍由 Run 做实际解析和业务校验。
// RequiresApproval 由工具定义固定，模型不能通过参数改变它。
type Tool struct {
	Name             string
	Description      string
	Parameters       json.RawMessage
	RequiresApproval bool
	Run              func(ctx context.Context, args json.RawMessage) (string, error)
}

var (
	// ErrUnknownTool 表示模型调用了未注册的工具。
	ErrUnknownTool = errors.New("工具不存在")
	// ErrInvalidArgs 表示工具参数不是合法 JSON 对象。
	ErrInvalidArgs = errors.New("工具参数无效")
	// ErrResultUnknown 表示外部操作已经开始，但无法确认是否成功。
	ErrResultUnknown = errors.New("外部操作结果未知")
)

// UnknownError 标记“结果未知”的工具错误。
//
// 上层收到该错误必须停止运行并记录结果未知，不能伪装成普通失败让模型重试。
type UnknownError struct {
	Cause error
}

func (e *UnknownError) Error() string {
	if e.Cause == nil {
		return ErrResultUnknown.Error()
	}
	return ErrResultUnknown.Error() + ": " + e.Cause.Error()
}

func (e *UnknownError) Is(target error) bool { return target == ErrResultUnknown }

// Unknown 把错误包装为结果未知。
func Unknown(cause error) error { return &UnknownError{Cause: cause} }

// IsUnknown 判断错误是否为结果未知。
func IsUnknown(err error) bool { return errors.Is(err, ErrResultUnknown) }
