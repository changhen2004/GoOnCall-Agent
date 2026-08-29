package model

import "testing"

func TestFingerprint_UsesAlertNameWhenPresent(t *testing.T) {
	a := Fingerprint("gocommunity", "HighErrorRate", "error rate spiked")
	b := Fingerprint("gocommunity", "HighErrorRate", "different title")
	if a != b {
		t.Fatalf("fingerprints with same alert name should match: %q != %q", a, b)
	}
}

func TestFingerprint_FallsBackToTitle(t *testing.T) {
	a := Fingerprint("gocommunity", "", "rabbitmq backlog")
	b := Fingerprint("gocommunity", "", "rabbitmq backlog")
	if a != b {
		t.Fatalf("fingerprints with same title should match: %q != %q", a, b)
	}
}

func TestFingerprint_DistinguishesService(t *testing.T) {
	a := Fingerprint("svc-a", "alert", "title")
	b := Fingerprint("svc-b", "alert", "title")
	if a == b {
		t.Fatalf("fingerprints for different services should differ: %q", a)
	}
}

func TestFingerprint_DeterministicLength(t *testing.T) {
	fp := Fingerprint("gocommunity", "HighErrorRate", "error rate spiked")
	if len(fp) != 64 {
		t.Fatalf("expected sha256 hex length 64, got %d", len(fp))
	}
	if fp != Fingerprint("gocommunity", "HighErrorRate", "error rate spiked") {
		t.Fatal("fingerprint should be deterministic")
	}
}
