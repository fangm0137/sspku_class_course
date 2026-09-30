// Package rag 提供正文切片、课程模型网关调用与向量检索。它只用标准库实现，
// 以便在离线或未配置网关的环境里仍能构建与启动。
package rag

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	// DefaultChunkSize 与 DefaultChunkOverlap 是 auto 模式的默认目标大小与重叠长度（字节）。
	DefaultChunkSize    = 800
	DefaultChunkOverlap = 80
	// MinCustomSize 与 MaxCustomSize 限定 custom 模式的切片大小。
	MinCustomSize = 100
	MaxCustomSize = 2000
)

// ErrInvalidSplit 表示切分参数或正文不满足约定；调用方应让上传整体失败，而不是写入半成品。
var ErrInvalidSplit = errors.New("invalid split parameters")

// Chunk 是一个切片。Index 在材料内唯一，Start/End 是相对预处理后正文的字节偏移，
// 因此用它们截取正文可以逐字复现 Text。
type Chunk struct {
	Index int
	Start int
	End   int
	Text  string
}

// Split 按 mode 切分正文。auto 使用 800 字节与 80 字节重叠的默认值，
// custom 使用显式 size/overlap（100–2000 字节、重叠不超过一半）。
func Split(text, mode string, size, overlap int) ([]Chunk, error) {
	if mode == "" {
		mode = "auto"
	}
	switch mode {
	case "auto":
		if size <= 0 {
			size = DefaultChunkSize
		}
		if overlap < 0 {
			overlap = DefaultChunkOverlap
		}
	case "custom":
		if size < MinCustomSize || size > MaxCustomSize || overlap < 0 || overlap*2 > size {
			return nil, ErrInvalidSplit
		}
	default:
		return nil, ErrInvalidSplit
	}
	if overlap >= size || size <= 0 {
		return nil, ErrInvalidSplit
	}
	if strings.TrimSpace(text) == "" {
		return nil, ErrInvalidSplit
	}
	chunks := make([]Chunk, 0, len(text)/size+1)
	for position := 0; position < len(text); {
		end := position + size
		if end >= len(text) {
			end = len(text)
		} else {
			end = breakPoint(text, position, end, size)
			// 回落点仍是任意字节位置，向前对齐到 rune 起点，避免切出半个汉字。
			for end > position && !utf8.RuneStart(text[end]) {
				end--
			}
		}
		if end <= position {
			return nil, ErrInvalidSplit
		}
		chunks = append(chunks, Chunk{Index: len(chunks), Start: position, End: end, Text: text[position:end]})
		if end == len(text) {
			break
		}
		next := end - overlap
		if next <= position {
			next = position + 1
		}
		for next < end && !utf8.RuneStart(text[next]) {
			next++
		}
		if next >= end {
			next = end
		}
		if next <= position {
			return nil, ErrInvalidSplit
		}
		position = next
	}
	if len(chunks) == 0 {
		return nil, ErrInvalidSplit
	}
	return chunks, nil
}

// breakPoint 在 [start+size/4, limit] 内挑一个更自然的切点：
// 优先空行，其次换行，再次句末标点；都没有时用 limit。
// 最靠前的切点限制在四分之一处，避免产生极短的切片。
func breakPoint(text string, start, limit, size int) int {
	window := text[start:limit]
	minimum := size / 4
	if at := strings.LastIndex(window, "\n\n"); at >= minimum {
		return start + at + 2
	}
	if at := strings.LastIndexByte(window, '\n'); at >= minimum {
		return start + at + 1
	}
	for index := len(window); index > minimum; {
		r, width := utf8.DecodeLastRuneInString(window[:index])
		index -= width
		if isSentenceEnd(r) {
			return start + index + width
		}
	}
	return limit
}

func isSentenceEnd(r rune) bool {
	switch r {
	case '。', '！', '？', '；', '：', '!', '?', ';', ':', '.':
		return true
	}
	return false
}
