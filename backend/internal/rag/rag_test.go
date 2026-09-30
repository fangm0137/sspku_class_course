package rag

import (
	"strings"
	"testing"
)

func TestSplitRejectsUnusableInput(t *testing.T) {
	for name, input := range map[string]struct {
		text    string
		mode    string
		size    int
		overlap int
	}{
		"blank":            {"   \n\t ", "auto", 0, 0},
		"empty":            {"", "auto", 0, 0},
		"unknown-mode":     {"正文内容", "hierarchy", 0, 0},
		"custom-too-small": {"正文内容", "custom", 99, 0},
		"custom-too-wide":  {"正文内容", "custom", 2001, 0},
		"custom-overlap":   {"正文内容", "custom", 200, 150},
		"overlap-equals":   {"正文内容", "auto", 200, 200},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Split(input.text, input.mode, input.size, input.overlap); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestSplitAutoDefaults(t *testing.T) {
	text := strings.Repeat("学习要点。", 400)
	chunks, err := Split(text, "auto", 0, 0)
	if err != nil {
		t.Fatal(err)
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
}

func TestSplitPrefersNaturalBoundaries(t *testing.T) {
	body := strings.Repeat("第一段内容。", 60) + "\n\n" + strings.Repeat("第二段内容。", 60)
	chunks, err := Split(body, "custom", 400, 40)
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
	chunks, err := Split(strings.Repeat("汉", 500), "custom", 101, 10)
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
