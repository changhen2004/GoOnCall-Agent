package messaging

import (
	"testing"
)

func TestNewEvent(t *testing.T) {
	ev, err := NewEvent(IncidentCreated, map[string]string{"incident_id": "inc_1"})
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if ev.Type != IncidentCreated {
		t.Fatalf("type = %s, want %s", ev.Type, IncidentCreated)
	}
	if ev.ID == "" {
		t.Fatal("event id should not be empty")
	}
	if ev.Timestamp.IsZero() {
		t.Fatal("timestamp should be set")
	}

	var payload map[string]string
	if err := ev.DecodePayload(&payload); err != nil {
		t.Fatalf("DecodePayload() error = %v", err)
	}
	if payload["incident_id"] != "inc_1" {
		t.Fatalf("payload = %v", payload)
	}
}

func TestRoutingKeys(t *testing.T) {
	cases := map[string]string{
		IncidentCreated: "incident.created",
		AgentRequested:  "agent.requested",
		ActionRequested: "action.requested",
		ActionCompleted: "action.completed",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("routing key %s != %s", got, want)
		}
	}
}

func TestExchangeConstant(t *testing.T) {
	if ExchangeAgent != "gooncall.agent" {
		t.Fatalf("ExchangeAgent = %s, want gooncall.agent", ExchangeAgent)
	}
}
