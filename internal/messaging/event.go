package messaging

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Event 是 GoOnCall 领域事件。
type Event struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// NewEvent 构造事件并序列化 payload。
func NewEvent(eventType string, payload any) (Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	return Event{
		ID:        uuid.NewString(),
		Type:      eventType,
		Timestamp: time.Now(),
		Payload:   data,
	}, nil
}

// DecodePayload 反序列化 payload。
func (e Event) DecodePayload(v any) error {
	return json.Unmarshal(e.Payload, v)
}
