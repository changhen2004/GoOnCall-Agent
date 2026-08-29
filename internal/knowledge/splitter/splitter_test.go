package splitter

import (
	"strings"
	"testing"
)

func TestSplit_Empty(t *testing.T) {
	s := New(100)
	if got := s.Split("  "); got != nil {
		t.Fatalf("Split(empty) = %v, want nil", got)
	}
}

func TestSplit_ShortTextSingleChunk(t *testing.T) {
	s := New(1000)
	got := s.Split("一段短文本")
	if len(got) != 1 || got[0] != "一段短文本" {
		t.Fatalf("Split = %v", got)
	}
}

func TestSplit_MergesParagraphsUpToLimit(t *testing.T) {
	s := New(100)
	text := `段落A内容

段落B内容

段落C内容`
	got := s.Split(text)
	if len(got) == 0 {
		t.Fatal("Split returned empty")
	}
	for _, c := range got {
		if len(c) > 100 {
			t.Fatalf("chunk len %d > 100: %q", len(c), c)
		}
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{"段落A内容", "段落B内容", "段落C内容"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("chunks missing %q: %v", want, got)
		}
	}
}

func TestSplit_LongParagraphHardSplit(t *testing.T) {
	s := New(10)
	long := strings.Repeat("a", 25)
	got := s.Split(long)
	if len(got) != 3 {
		t.Fatalf("Split(long) len = %d, want 3", len(got))
	}
	for _, c := range got {
		if len(c) > 10 {
			t.Fatalf("chunk len %d > 10", len(c))
		}
	}
}
