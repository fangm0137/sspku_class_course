// Package rag 提供正文切片、课程模型网关调用与向量检索。它只用标准库实现，
// 以便在离线或未配置网关的环境里仍能构建、启动并完成关键字检索。
package rag

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// DefaultChunkSize 与 DefaultChunkOverlap 是 auto 策略的目标大小与重叠长度（字节）。
	DefaultChunkSize    = 800
	DefaultChunkOverlap = 80
	// MinCustomSize 与 MaxCustomSize 限定 custom 策略的切片大小。
	MinCustomSize = 100
	MaxCustomSize = 2000

	// ModeAuto 未指定策略时使用：最大约 800 字节、重叠 80 字节，优先在空行/换行/句末断开。
	ModeAuto = "auto"
	// ModeCustom 使用调用方给定的大小与重叠（100–2000 字节、重叠不超过一半）。
	ModeCustom = "custom"
	// ModeHierarchy 按 Markdown 标题（#/##/###）分章，标题保留在章内。
	ModeHierarchy = "hierarchy"
)

// ErrInvalidSplit 表示切分参数或正文不满足约定；调用方应让请求整体失败，而不是写入半成品。
var ErrInvalidSplit = errors.New("invalid split parameters")

// Chunk 是一个切片。Index 在材料内唯一；Start/End 是相对**预处理后正文**的字节偏移，
// 因此用它们截取预处理文本可以逐字复现 Text。未启用预处理时预处理文本即 body_text。
type Chunk struct {
	Index int
	Start int
	End   int
	Text  string
}

// SplitOptions 是一次建索引使用的切分参数。Mode 为空等同 auto。
type SplitOptions struct {
	Mode    string
	Size    int
	Overlap int
	// RemoveURLs/RemoveEmails/CollapseWhitespace 只作用于待切分与待嵌入文本，
	// 不改写 knowledge_entries.body_text；启用后偏移相对预处理后的文本。
	RemoveURLs         bool
	RemoveEmails       bool
	CollapseWhitespace bool
}

// EffectiveMode 返回规范化后的策略名。
func (o SplitOptions) EffectiveMode() string {
	mode := strings.TrimSpace(o.Mode)
	if mode == "" {
		return ModeAuto
	}
	return mode
}

// Validate 检查策略名与 custom 参数。auto 与 hierarchy 的窗口大小由规约固定，忽略调用方数值。
func (o SplitOptions) Validate() error {
	switch o.EffectiveMode() {
	case ModeAuto, ModeHierarchy:
		return nil
	case ModeCustom:
		if o.Size < MinCustomSize || o.Size > MaxCustomSize {
			return ErrInvalidSplit
		}
		if o.Overlap < 0 || o.Overlap*2 > o.Size {
			return ErrInvalidSplit
		}
		return nil
	default:
		return ErrInvalidSplit
	}
}

var (
	urlPattern   = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"'）】、，。；：！？]+`)
	emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	// 只折叠横向空白（含全角空格与不换行空格），保留段落换行；再把 3 个以上连续换行压成 2 个。
	horizontalBlank = regexp.MustCompile(`[^\S\n]+`)
	manyNewlines    = regexp.MustCompile(`\n{3,}`)
)

// Prepare 返回实际用于切分与嵌入的文本。不启用任何预处理时与输入完全相同，
// 因此默认路径下切片偏移同时也能在 body_text 上逐字复现。
func Prepare(text string, options SplitOptions) string {
	prepared := text
	if options.RemoveURLs {
		prepared = urlPattern.ReplaceAllString(prepared, " ")
	}
	if options.RemoveEmails {
		prepared = emailPattern.ReplaceAllString(prepared, " ")
	}
	if options.CollapseWhitespace {
		prepared = horizontalBlank.ReplaceAllString(prepared, " ")
		prepared = manyNewlines.ReplaceAllString(prepared, "\n\n")
	}
	return prepared
}

// SplitWith 先按选项预处理正文，再按策略切分，返回预处理文本与切片。
// 切片的 Start/End 以返回的预处理文本为基准，可用它逐字复现 Text。
func SplitWith(text string, options SplitOptions) (string, []Chunk, error) {
	if err := options.Validate(); err != nil {
		return "", nil, err
	}
	prepared := Prepare(text, options)
	if strings.TrimSpace(prepared) == "" {
		return "", nil, ErrInvalidSplit
	}
	var chunks []Chunk
	switch options.EffectiveMode() {
	case ModeHierarchy:
		chunks = hierarchyChunks(prepared, DefaultChunkSize, DefaultChunkOverlap)
	default:
		size, overlap := DefaultChunkSize, DefaultChunkOverlap
		if options.EffectiveMode() == ModeCustom {
			size, overlap = options.Size, options.Overlap
		}
		chunks = windowChunks(prepared, 0, len(prepared), size, overlap)
	}
	if len(chunks) == 0 {
		return "", nil, ErrInvalidSplit
	}
	for index := range chunks {
		chunks[index].Index = index
	}
	return prepared, chunks, nil
}

// Split 是 SplitWith 的简化入口，保留给只关心策略与窗口大小的调用方。
func Split(text, mode string, size, overlap int) ([]Chunk, error) {
	_, chunks, err := SplitWith(text, SplitOptions{Mode: mode, Size: size, Overlap: overlap})
	return chunks, err
}

// hierarchyChunks 按 Markdown 标题分章；单章超过窗口时再按 auto 窗口细分。
func hierarchyChunks(text string, size, overlap int) []Chunk {
	chunks := make([]Chunk, 0, 8)
	for _, section := range headingSections(text) {
		if strings.TrimSpace(text[section[0]:section[1]]) == "" {
			continue
		}
		chunks = append(chunks, windowChunks(text, section[0], section[1], size, overlap)...)
	}
	return chunks
}

// headingSections 把正文切成若干区间：每个 #/##/### 标题开启一个新区间，
// 标题行本身属于它开启的区间；首个标题之前的内容（若有）单独成段。
func headingSections(text string) [][2]int {
	sections := make([][2]int, 0, 8)
	start := 0
	for index := 0; index < len(text); {
		lineEnd := strings.IndexByte(text[index:], '\n')
		next := len(text)
		if lineEnd >= 0 {
			next = index + lineEnd + 1
		}
		if isHeading(text[index:min(next, len(text))]) {
			if index > start && strings.TrimSpace(text[start:index]) != "" {
				sections = append(sections, [2]int{start, index})
			}
			start = index
		}
		if next <= index {
			break
		}
		index = next
	}
	if start < len(text) {
		sections = append(sections, [2]int{start, len(text)})
	}
	return sections
}

// isHeading 判断一行是否为 1–3 级 Markdown 标题（`#` 后必须是空格或行尾）。
func isHeading(line string) bool {
	line = strings.TrimRight(line, "\n")
	line = strings.TrimLeft(line, " \t")
	hashes := 0
	for hashes < len(line) && line[hashes] == '#' {
		hashes++
	}
	if hashes == 0 || hashes > 3 {
		return false
	}
	return hashes == len(line) || line[hashes] == ' ' || line[hashes] == '\t'
}

// windowChunks 在 [from,to) 内按 size/overlap 切分，返回绝对偏移的切片。
func windowChunks(text string, from, to, size, overlap int) []Chunk {
	if size <= 0 {
		size = DefaultChunkSize
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= size {
		overlap = size / 2
	}
	chunks := make([]Chunk, 0, (to-from)/size+1)
	position := from
	for position < to {
		end := position + size
		if end >= to {
			end = to
		} else {
			end = breakPoint(text, position, end, size)
			// 回落点可能落在多字节字符中间，向前对齐到 rune 起点，避免切出半个汉字。
			for end > position && !utf8.RuneStart(text[end]) {
				end--
			}
		}
		if end > to {
			end = to
		}
		if end <= position {
			break
		}
		if strings.TrimSpace(text[position:end]) != "" {
			chunks = append(chunks, Chunk{Start: position, End: end, Text: text[position:end]})
		}
		if end >= to {
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
			break
		}
		position = next
	}
	return chunks
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
