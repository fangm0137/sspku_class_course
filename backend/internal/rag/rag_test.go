package rag

import (
	"strings"
	"testing"
)

func TestSplitRejectsUnusableInput(t *testing.T) {
	for name, input := range map[string]struct {
		text    string
		options SplitOptions
	}{
		"blank":                  {"   \n\t ", SplitOptions{}},
		"empty":                  {"", SplitOptions{}},
		"unknown-mode":           {"正文内容", SplitOptions{Mode: "semantic"}},
		"custom-too-small":       {"正文内容", SplitOptions{Mode: ModeCustom, Size: 99}},
		"custom-too-wide":        {"正文内容", SplitOptions{Mode: ModeCustom, Size: 2001}},
		"custom-overlap":         {"正文内容", SplitOptions{Mode: ModeCustom, Size: 200, Overlap: 150}},
		"custom-negative":        {"正文内容", SplitOptions{Mode: ModeCustom, Size: 200, Overlap: -1}},
		"blank-after-collapse":   {"   \u3000  ", SplitOptions{Mode: ModeAuto, CollapseWhitespace: true}},
		"hierarchy-with-only-sp": {"\n\n  \n", SplitOptions{Mode: ModeHierarchy}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := SplitWith(input.text, input.options); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestSplitAutoUsesWindowAndOverlap(t *testing.T) {
	text := strings.Repeat("学习要点。", 400)
	prepared, chunks, err := SplitWith(text, SplitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if prepared != text {
		t.Fatal("auto without preprocessing must not rewrite the source")
	}
	if len(chunks) < 2 {
		t.Fatalf("long text produced %d chunk(s)", len(chunks))
	}
	previousStart := -1
	for index, chunk := range chunks {
		if chunk.Index != index {
			t.Fatalf("chunk index %d out of order", chunk.Index)
		}
		if chunk.Start <= previousStart || chunk.End <= chunk.Start {
			t.Fatalf("chunk %d does not advance: %+v", index, chunk)
		}
		if text[chunk.Start:chunk.End] != chunk.Text {
			t.Fatalf("chunk %d offsets do not map back to the source", index)
		}
		if strings.TrimSpace(chunk.Text) == "" {
			t.Fatalf("chunk %d is blank", index)
		}
		if len(chunk.Text) > DefaultChunkSize {
			t.Fatalf("chunk %d exceeds the target size", index)
		}
		previousStart = chunk.Start
	}
	if chunks[len(chunks)-1].End != len(text) {
		t.Fatal("last chunk does not reach the end of the text")
	}
	// auto 固定使用 800/80：相邻切片必须真的重叠，否则长材料的上下文会被切断。
	// 重叠量会前后对齐到字符边界，因此允许在 80 附近浮动不足一个字的字节数。
	second := chunks[1]
	if second.Start >= chunks[0].End {
		t.Fatalf("auto chunks do not overlap: %d >= %d", second.Start, chunks[0].End)
	}
	if overlap := chunks[0].End - second.Start; overlap < DefaultChunkOverlap-4 || overlap > DefaultChunkOverlap+4 {
		t.Fatalf("auto overlap is %d, want about %d", overlap, DefaultChunkOverlap)
	}
}

func TestSplitPrefersNaturalBoundaries(t *testing.T) {
	body := strings.Repeat("第一段内容。", 60) + "\n\n" + strings.Repeat("第二段内容。", 60)
	_, chunks, err := SplitWith(body, SplitOptions{Mode: ModeCustom, Size: 400, Overlap: 40})
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range chunks {
		if chunk.End < len(body) && !strings.HasSuffix(chunk.Text, "。") && !strings.HasSuffix(chunk.Text, "\n\n") && !strings.HasSuffix(chunk.Text, "\n") {
			t.Fatalf("chunk %d does not end on a boundary: %q", chunk.Index, chunk.Text[len(chunk.Text)-6:])
		}
	}
}

func TestSplitKeepsRuneBoundaries(t *testing.T) {
	_, chunks, err := SplitWith(strings.Repeat("汉", 500), SplitOptions{Mode: ModeCustom, Size: 101, Overlap: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range chunks {
		for _, r := range chunk.Text {
			if r == '\uFFFD' {
				t.Fatal("chunk contains a broken rune")
			}
		}
	}
}

func TestSplitHierarchyKeepsHeadingsInTheirSection(t *testing.T) {
	body := "# 第一章\n\n" + strings.Repeat("甲。", 20) + "\n\n" +
		"## 第一节\n\n" + strings.Repeat("乙。", 20) + "\n\n" +
		"### 小结\n\n" + strings.Repeat("丙。", 20)
	prepared, chunks, err := SplitWith(body, SplitOptions{Mode: ModeHierarchy})
	if err != nil {
		t.Fatal(err)
	}
	if prepared != body {
		t.Fatal("hierarchy must not rewrite the source")
	}
	if len(chunks) != 3 {
		t.Fatalf("got %d sections, want 3", len(chunks))
	}
	for _, heading := range []string{"# 第一章", "## 第一节", "### 小结"} {
		found := false
		for _, chunk := range chunks {
			if strings.HasPrefix(chunk.Text, heading) {
				found = true
			}
		}
		if !found {
			t.Fatalf("heading %q is not kept inside a section chunk", heading)
		}
	}
	for _, chunk := range chunks {
		if body[chunk.Start:chunk.End] != chunk.Text {
			t.Fatalf("chunk %d offsets do not map back to the source", chunk.Index)
		}
	}
}

func TestSplitHierarchySplitsOversizedSection(t *testing.T) {
	body := "# 长章\n\n" + strings.Repeat("长内容。", 400)
	_, chunks, err := SplitWith(body, SplitOptions{Mode: ModeHierarchy})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("oversized section produced %d chunk(s)", len(chunks))
	}
	if !strings.HasPrefix(chunks[0].Text, "# 长章") {
		t.Fatal("first chunk must keep the heading")
	}
	for _, chunk := range chunks {
		if len(chunk.Text) > DefaultChunkSize {
			t.Fatalf("chunk %d exceeds the target size", chunk.Index)
		}
	}
}

func TestSplitPreprocessLeavesSourceUntouched(t *testing.T) {
	source := "联系 a@b.com 或访问 https://example.com/x 获取资料" + strings.Repeat("　 ", 5) + "完。"
	options := SplitOptions{Mode: ModeAuto, RemoveURLs: true, RemoveEmails: true, CollapseWhitespace: true}
	prepared, chunks, err := SplitWith(source, options)
	if err != nil {
		t.Fatal(err)
	}
	if prepared == source {
		t.Fatal("preprocessing did not change the text used for chunking")
	}
	if strings.Contains(prepared, "a@b.com") || strings.Contains(prepared, "https://example.com") {
		t.Fatalf("preprocessing left contact details behind: %q", prepared)
	}
	for _, chunk := range chunks {
		// 偏移基准是预处理后的文本。
		if prepared[chunk.Start:chunk.End] != chunk.Text {
			t.Fatalf("chunk %d offsets do not map back to the prepared text", chunk.Index)
		}
	}
}

func TestFuseRanksByReciprocalRank(t *testing.T) {
	got := Fuse([][]uint64{{1, 2, 3}, {3, 4}}, 3)
	want := []uint64{3, 1, 2}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if single := Fuse([][]uint64{{9, 8, 7}}, 2); len(single) != 2 || single[0] != 9 || single[1] != 8 {
		t.Fatalf("single path order not preserved: %v", single)
	}
	if empty := Fuse(nil, 4); len(empty) != 0 {
		t.Fatalf("empty input produced %v", empty)
	}
}

func TestFuseScoredReportsRankScores(t *testing.T) {
	fused := FuseScored([][]Candidate{
		{{ID: 1, Score: 9.5}, {ID: 2, Score: 4.2}},
		{{ID: 2, Score: 0.91}, {ID: 3, Score: 0.4}},
	}, 5)
	if len(fused) != 3 || fused[0].ID != 2 {
		t.Fatalf("two-path hit must rank first: %+v", fused)
	}
	// 名次分 = 1/(k+rank+1)：id2 命中两路（rank 1 与 0），必然高于只命中一路的 id1。
	if fused[0].Score <= fused[1].Score {
		t.Fatalf("rank score does not reflect double hits: %+v", fused)
	}
	if fused[0].Score != 1/float64(FusionK+2)+1/float64(FusionK+1) {
		t.Fatalf("unexpected rank score %v", fused[0].Score)
	}
}

func TestNormalizeAndValidateMode(t *testing.T) {
	if NormalizeMode("") != ModeHybrid {
		t.Fatal("empty mode must default to hybrid")
	}
	if NormalizeMode(" VECTOR ") != ModeVector {
		t.Fatal("mode must be normalized")
	}
	for _, mode := range []string{ModeKeyword, ModeVector, ModeHybrid} {
		if !ValidMode(mode) {
			t.Fatalf("%s must be valid", mode)
		}
	}
	for _, mode := range []string{"", "semantic", "KEYWORD "} {
		if ValidMode(mode) {
			t.Fatalf("%q must be rejected before normalization", mode)
		}
	}
}
