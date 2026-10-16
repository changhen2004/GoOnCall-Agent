// Package splitter 将文档切分为检索片段。
package splitter

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Splitter 按 Markdown 标题、段落、行、句子递归切分文本。
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

// Split 优先保留完整章节，超长部分逐级细分；大小以 Unicode 字符计。
func (s *Splitter) Split(text string) []string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return nil
	}
	return s.split(text, 1)
}

func (s *Splitter) split(text string, level int) []string {
	if utf8.RuneCountInString(text) <= s.chunkSize {
		return []string{text}
	}
	if level > 10 {
		return hardSplit(text, s.chunkSize)
	}

	var parts []string
	switch {
	case level <= 6:
		parts = splitSections(text, level)
	case level == 7:
		parts = strings.SplitAfter(text, "\n\n")
	case level == 8:
		parts = strings.SplitAfter(text, "\n")
	default:
		start := 0
		for i, r := range text {
			end := i + utf8.RuneLen(r)
			boundary := unicode.IsSpace(r)
			if level == 9 {
				boundary = strings.ContainsRune("。！？", r)
				if strings.ContainsRune(".!?", r) && (end == len(text) || unicode.IsSpace(firstRune(text[end:]))) {
					boundary = true
				}
			}
			if boundary {
				parts = append(parts, text[start:end])
				start = end
			}
		}
		parts = append(parts, text[start:])
	}

	var chunks []string
	var buf strings.Builder
	bufSize := 0

	flush := func() {
		if buf.Len() > 0 {
			chunks = append(chunks, strings.TrimSpace(buf.String()))
			buf.Reset()
			bufSize = 0
		}
	}

	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			continue
		}
		size := utf8.RuneCountInString(p)
		if size > s.chunkSize {
			flush()
			chunks = append(chunks, s.split(strings.TrimSpace(p), level+1)...)
			continue
		}
		if bufSize+size > s.chunkSize {
			flush()
		}
		buf.WriteString(p)
		bufSize += size
	}
	flush()
	return chunks
}

func firstRune(text string) rune {
	r, _ := utf8.DecodeRuneInString(text)
	return r
}

// splitSections 将标题附在下一节正文上，忽略围栏代码块中的伪标题。
// ponytail: 仅识别 ATX 标题和围栏代码；需要 Setext 等语法时再接 Markdown AST。
func splitSections(text string, level int) []string {
	var parts []string
	start, offset := 0, 0
	fence := ""
	heading := strings.Repeat("#", level)
	for _, line := range strings.SplitAfter(text, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if len(line)-len(trimmed) <= 3 {
			if fence != "" {
				if strings.HasPrefix(trimmed, fence) && strings.Trim(strings.TrimSpace(trimmed), fence[:1]) == "" {
					fence = ""
				}
			} else if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
				fence = trimmed[:3]
				for len(fence) < len(trimmed) && trimmed[len(fence)] == fence[0] {
					fence += fence[:1]
				}
			} else if strings.HasPrefix(trimmed, heading+" ") || strings.HasPrefix(trimmed, heading+"\t") || strings.TrimSpace(trimmed) == heading {
				if offset > start {
					parts = append(parts, text[start:offset])
				}
				start = offset
			}
		}
		offset += len(line)
	}
	return append(parts, text[start:])
}

func hardSplit(s string, size int) []string {
	out := make([]string, 0)
	runes := []rune(s)
	for i := 0; i < len(runes); i += size {
		end := i + size
		if end > len(runes) {
			end = len(runes)
		}
		if piece := strings.TrimSpace(string(runes[i:end])); piece != "" {
			out = append(out, piece)
		}
	}
	return out
}
