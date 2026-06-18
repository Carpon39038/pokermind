package engine

import "testing"

func TestHandCategoryOrder(t *testing.T) {
	// HighCard < Pair < ... < StraightFlush
	order := []HandCategory{
		HighCard, Pair, TwoPair, ThreeOfAKind, Straight,
		Flush, FullHouse, FourOfAKind, StraightFlush,
	}
	for i := 1; i < len(order); i++ {
		if order[i] <= order[i-1] {
			t.Fatalf("%v not stronger than %v", order[i], order[i-1])
		}
	}
}

func TestHandCategoryString(t *testing.T) {
	cases := []struct {
		c    HandCategory
		want string
	}{
		{HighCard, "HighCard"},
		{Pair, "Pair"},
		{StraightFlush, "StraightFlush"},
	}
	for _, tc := range cases {
		if got := tc.c.String(); got != tc.want {
			t.Errorf("%v.String() = %q, want %q", tc.c, got, tc.want)
		}
	}
}

func TestHandRankCompareDifferentCategory(t *testing.T) {
	lo := HandRank{Category: Pair, Ranks: []Rank{14, 13, 12, 11}}
	hi := HandRank{Category: TwoPair, Ranks: []Rank{2, 2, 3}}
	if lo.Compare(hi) >= 0 {
		t.Fatalf("Pair should lose to TwoPair")
	}
	if hi.Compare(lo) <= 0 {
		t.Fatalf("TwoPair should beat Pair")
	}
}

func TestHandRankCompareSameCategoryTiebreaker(t *testing.T) {
	// 同为 Pair,一方 K 大一方 Q 大,K 赢
	a := HandRank{Category: Pair, Ranks: []Rank{13, 12, 9, 7}}
	b := HandRank{Category: Pair, Ranks: []Rank{12, 13, 11, 10}}
	if a.Compare(b) <= 0 {
		t.Fatalf("Pair of K should beat Pair of Q")
	}
}

func TestHandRankCompareEqual(t *testing.T) {
	a := HandRank{Category: Flush, Ranks: []Rank{13, 12, 11, 9, 7}}
	b := HandRank{Category: Flush, Ranks: []Rank{13, 12, 11, 9, 7}}
	if a.Compare(b) != 0 {
		t.Fatalf("identical hands should tie")
	}
}
