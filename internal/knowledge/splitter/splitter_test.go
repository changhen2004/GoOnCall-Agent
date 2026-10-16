package splitter

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestSplit_Empty(t *testing.T) {
	s := New(100)
	if got := s.Split("  "); got != nil {
		t.Fatalf("Split(empty) = %v, want nil", got)
	}
}

func TestSplit_RecursiveMarkdown(t *testing.T) {
	cases := []struct {
		name string
		text string
		size int
		want []string
	}{
		{"sections", "## A\n\n12345\n## B\n\n67890", 19, []string{"## A\n\n12345", "## B\n\n67890"}},
		{"nested headings", "# A\n## B\n\n12345\n## C\n\n67890", 19, []string{"# A\n## B\n\n12345", "## C\n\n67890"}},
		{"paragraphs", "第一段内容。\n\n第二段内容。", 8, []string{"第一段内容。", "第二段内容。"}},
		{"lines", "甲乙丙\n丁戊己", 5, []string{"甲乙丙", "丁戊己"}},
		{"sentences", "检查消费者。验证积压！", 7, []string{"检查消费者。", "验证积压！"}},
		{"words", "alpha beta gamma", 10, []string{"alpha", "beta gamma"}},
		{"unicode", "中文🙂中文🙂中文", 3, []string{"中文🙂", "中文🙂", "中文"}},
		{"crlf", "第一段\r\n\r\n第二段", 5, []string{"第一段", "第二段"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := New(tc.size).Split(tc.text)
			if len(got) != len(tc.want) {
				t.Fatalf("Split = %q, want %q", got, tc.want)
			}
			for i, chunk := range got {
				if chunk != tc.want[i] || !utf8.ValidString(chunk) || utf8.RuneCountInString(chunk) > tc.size {
					t.Fatalf("chunk[%d] = %q, want %q (limit %d characters)", i, chunk, tc.want[i], tc.size)
				}
			}
			withoutSpace := func(text string) string {
				return strings.Map(func(r rune) rune {
					if unicode.IsSpace(r) {
						return -1
					}
					return r
				}, text)
			}
			if withoutSpace(strings.Join(got, "")) != withoutSpace(tc.text) {
				t.Fatal("split lost content")
			}
		})
	}
}

func TestSplit_IgnoresHeadingsInsideFence(t *testing.T) {
	for _, fence := range []string{"```", "~~~~"} {
		text := "## A\n" + fence + "shell\n## comment\n" + fence + "\n## B\nbody"
		parts := splitSections(text, 2)
		if len(parts) != 2 || !strings.Contains(parts[0], "## comment") || !strings.HasPrefix(parts[1], "## B") {
			t.Fatalf("sections = %q", parts)
		}
		got := New(utf8.RuneCountInString(parts[0])).Split(text)
		if len(got) != 2 || !strings.Contains(got[0], fence+"shell\n## comment\n"+fence) {
			t.Fatalf("code block was split despite fitting: %q", got)
		}
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
