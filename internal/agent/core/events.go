package core

// EventType 是展示事件类型。事件只用于实时展示，不承担持久化职责；
// 客户端断连后丢失的增量不补播，以查询接口保存的记录为准。
type EventType string

const (
	EventTextDelta        EventType = "text_delta"
	EventToolStart        EventType = "tool_start"
	EventToolEnd          EventType = "tool_end"
	EventApprovalRequired EventType = "approval_required"
)

// Event 是核心向外输出的展示事件。
type Event struct {
	Type       EventType
	Delta      string
	ToolCallID string
	ToolName   string
	Arguments  string
	ApprovalID string
	Status     string
	Error      string
}
