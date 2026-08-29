package registry

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
)

type toolInput struct {
	Name string `json:"name" jsonschema:"required,description=名字"`
}

func makeFakeTool(name string) tool.InvokableTool {
	fn := func(ctx context.Context, in toolInput) (string, error) {
		return "ok:" + in.Name, nil
	}
	t, err := toolutils.InferTool(name, "fake tool "+name, fn)
	if err != nil {
		panic(err)
	}
	return t
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := New()
	if err := reg.Register(makeFakeTool("fake.a"), RiskLow); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got, ok := reg.Get("fake.a")
	if !ok {
		t.Fatal("Get(fake.a) not found")
	}
	if got.RiskLevel != RiskLow {
		t.Fatalf("risk = %s, want LOW", got.RiskLevel)
	}
	if got.Name != "fake.a" {
		t.Fatalf("name = %s, want fake.a", got.Name)
	}
}

func TestRegistry_DuplicateRegister(t *testing.T) {
	reg := New()
	if err := reg.Register(makeFakeTool("fake.a"), RiskLow); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := reg.Register(makeFakeTool("fake.a"), RiskMedium); err == nil {
		t.Fatal("Register(dup) expected error, got nil")
	}
}

func TestRegistry_AllAndNamesOrder(t *testing.T) {
	reg := New()
	_ = reg.Register(makeFakeTool("fake.a"), RiskLow)
	_ = reg.Register(makeFakeTool("fake.b"), RiskMedium)
	_ = reg.Register(makeFakeTool("fake.c"), RiskHigh)

	all := reg.All()
	if len(all) != 3 {
		t.Fatalf("All() len = %d, want 3", len(all))
	}
	names := reg.Names()
	want := []string{"fake.a", "fake.b", "fake.c"}
	for i, n := range want {
		if names[i] != n {
			t.Fatalf("Names()[%d] = %s, want %s", i, names[i], n)
		}
	}
	if len(reg.EinoTools()) != 3 {
		t.Fatalf("EinoTools() len = %d, want 3", len(reg.EinoTools()))
	}
}

func TestRegistry_GetMissing(t *testing.T) {
	reg := New()
	if _, ok := reg.Get("missing"); ok {
		t.Fatal("Get(missing) should return false")
	}
}
