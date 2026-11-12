package ops

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRabbitMQQueueStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/missing") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"name":"orders","vhost":"/","state":"running","consumers":2,
			"messages":10,"messages_ready":8,"messages_unacknowledged":2}`))
	}))
	defer server.Close()

	tool := NewRabbitMQTool(RabbitMQOptions{ManagementURL: server.URL, Timeout: time.Second})
	out, err := tool.Run(context.Background(), json.RawMessage(`{"queue":"orders"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatal(err)
	}
	if response["found"] != true || response["consumers"] != float64(2) {
		t.Fatalf("response = %v", response)
	}

	out, err = tool.Run(context.Background(), json.RawMessage(`{"queue":"missing"}`))
	if err != nil {
		t.Fatalf("missing queue: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatal(err)
	}
	if response["found"] != false {
		t.Fatalf("missing response = %v", response)
	}
}

func TestRabbitMQUnavailable(t *testing.T) {
	tool := NewRabbitMQTool(RabbitMQOptions{ManagementURL: "http://127.0.0.1:1", Timeout: 50 * time.Millisecond})
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"queue":"q"}`)); err == nil ||
		!strings.Contains(err.Error(), "不可达") {
		t.Fatalf("不可达错误 = %v", err)
	}
}
