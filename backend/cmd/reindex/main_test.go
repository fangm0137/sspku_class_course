package main

import "testing"

func TestParseIDs(t *testing.T) {
	got, err := parseIDs([]string{"3", "7,8", " 12 "})
	if err != nil {
		t.Fatal(err)
	}
	want := []uint64{3, 7, 8, 12}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if empty, err := parseIDs(nil); err != nil || len(empty) != 0 {
		t.Fatal("no arguments must mean every material")
	}
	for _, bad := range [][]string{{"0"}, {"-1"}, {"a"}, {"1.5"}} {
		if _, err := parseIDs(bad); err == nil {
			t.Fatalf("expected rejection for %v", bad)
		}
	}
}
