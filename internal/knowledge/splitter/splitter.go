// Package splitter 将文档切分为检索片段。
package splitter

import "strings"

// Splitter 按段落边界切分文本。
type Splitter struct {
	chunkSize int
}

// New 创建切分器，chunkSize 为片段目标大小（字符数）。
func New(chunkSize int) *Splitter {
	if chunkSize <= 0 {
		chunkSize = 1000
	}
	return &Splitter{chunkSize: chunkSize}
}

// Split 将文本切分为不超过 chunkSize 的片段，优先在段落边界切分。
func (s *Splitter) Split(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if len(text) <= s.chunkSize {
		return []string{text}
	}

	paras := splitParagraphs(text)
	chunks := make([]string, 0)
	var buf strings.Builder

	flush := func() {
		if buf.Len() > 0 {
			chunks = append(chunks, strings.TrimSpace(buf.String()))
			buf.Reset()
		}
	}

	for _, p := range paras {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if len(p) > s.chunkSize {
			flush()
			chunks = append(chunks, hardSplit(p, s.chunkSize)...)
			continue
		}
		if buf.Len() > 0 && buf.Len()+2+len(p) > s.chunkSize {
			flush()
		}
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
		}
		buf.WriteString(p)
	}
	flush()
	return chunks
}

func splitParagraphs(text string) []string {
	return strings.Split(text, "\n\n")
}

func hardSplit(s string, size int) []string {
	out := make([]string, 0)
	for i := 0; i < len(s); i += size {
		end := i + size
		if end > len(s) {
			end = len(s)
		}
		out = append(out, strings.TrimSpace(s[i:end]))
	}
	return out
}
