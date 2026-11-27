package main

import (
	"fmt"
	"math"
	"testing"
)

// shareSum adds up the shares of rows.
func shareSum(rows []ShareRow) float64 {
	var total float64
	for _, r := range rows {
		total += r.Share
	}
	return total
}

// TestFoldTopSharesCapsK8s checks that foldTopShares keeps the topN largest
// labels, folds the rest and any stored "other" into one trailing "other" row,
// and preserves the total share.
func TestFoldTopSharesCapsK8s(t *testing.T) {
	t.Parallel()
	var rows []ShareRow
	for i := range 40 {
		rows = append(rows, ShareRow{Label: fmt.Sprintf("1.%d", i), Share: float64(40-i) / 1000})
	}
	rows = append(rows, ShareRow{Label: "other", Share: 0.01})
	before := shareSum(rows)

	got := foldTopShares(rows, topVersions)
	if len(got) != topVersions+1 {
		t.Fatalf("len = %d, want %d (top %d plus other)", len(got), topVersions+1, topVersions)
	}
	for i := range topVersions {
		want := fmt.Sprintf("1.%d", i)
		if got[i].Label != want {
			t.Fatalf("row %d label = %q, want %q", i, got[i].Label, want)
		}
	}
	last := got[len(got)-1]
	if last.Label != "other" {
		t.Fatalf("last label = %q, want other", last.Label)
	}
	// Ranks 10..39 (30 values) plus the stored "other" row.
	var wantOther float64
	for i := topVersions; i < 40; i++ {
		wantOther += float64(40-i) / 1000
	}
	wantOther += 0.01
	if math.Abs(last.Share-wantOther) > 1e-12 {
		t.Fatalf("other share = %v, want %v", last.Share, wantOther)
	}
	if math.Abs(shareSum(got)-before) > 1e-12 {
		t.Fatalf("total share = %v, want %v", shareSum(got), before)
	}
}

// TestFoldTopSharesUnderCap checks that a dimension within the cap comes back
// unchanged, with a stored "other" row kept as a single row.
func TestFoldTopSharesUnderCap(t *testing.T) {
	t.Parallel()
	rows := []ShareRow{
		{Label: "1.31", Share: 0.5},
		{Label: "other", Share: 0.1},
		{Label: "1.30", Share: 0.4},
	}
	got := foldTopShares(rows, topVersions)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3: %+v", len(got), got)
	}
	others := 0
	for _, r := range got {
		if r.Label == "other" {
			others++
		}
	}
	if others != 1 {
		t.Fatalf("other rows = %d, want 1: %+v", others, got)
	}
}

// TestFoldTopSharesNoOther checks that an empty input yields an empty, non-nil
// result and that no "other" row is invented when nothing folds into it.
func TestFoldTopSharesNoOther(t *testing.T) {
	t.Parallel()
	if got := foldTopShares(nil, topVersions); got == nil || len(got) != 0 {
		t.Fatalf("foldTopShares(nil) = %#v, want empty non-nil", got)
	}
	got := foldTopShares([]ShareRow{{Label: "1.31", Share: 1}}, 1)
	if len(got) != 1 || got[0].Label != "1.31" {
		t.Fatalf("got %+v, want only 1.31", got)
	}
}

// One install-day can report many versions, so folded shares overlap; the
// "other" row never claims more than every install-day.
func TestFoldTopSharesOtherCappedAtOne(t *testing.T) {
	t.Parallel()
	var rows []ShareRow
	for i := range 12 {
		rows = append(rows, ShareRow{Label: fmt.Sprintf("v1.%02d", i), Share: 1})
	}
	out := foldTopShares(rows, 10)
	last := out[len(out)-1]
	if last.Label != versionOtherStored || last.Share != 1 {
		t.Fatalf("last row = %+v, want other capped at 1", last)
	}
}
