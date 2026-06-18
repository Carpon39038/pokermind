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

func c(r Rank, s Suit) Card { return Card{Rank: r, Suit: s} }

func TestEvaluate5HighCard(t *testing.T) {
	h := evaluate5([]Card{c(2, 0), c(5, 1), c(7, 0), c(9, 2), c(13, 3)})
	if h.Category != HighCard {
		t.Fatalf("got %v, want HighCard", h.Category)
	}
}

func TestEvaluate5Pair(t *testing.T) {
	h := evaluate5([]Card{c(13, 0), c(13, 1), c(5, 0), c(9, 2), c(2, 3)})
	if h.Category != Pair || h.Ranks[0] != 13 {
		t.Fatalf("got %v %v, want Pair(K ...)", h.Category, h.Ranks)
	}
}

func TestEvaluate5TwoPair(t *testing.T) {
	h := evaluate5([]Card{c(13, 0), c(13, 1), c(8, 0), c(8, 2), c(2, 3)})
	if h.Category != TwoPair {
		t.Fatalf("got %v, want TwoPair", h.Category)
	}
	if h.Ranks[0] != 13 || h.Ranks[1] != 8 {
		t.Fatalf("two pair ranks = %v, want [13 8 ...]", h.Ranks)
	}
}

func TestEvaluate5Trips(t *testing.T) {
	h := evaluate5([]Card{c(7, 0), c(7, 1), c(7, 2), c(9, 0), c(2, 3)})
	if h.Category != ThreeOfAKind || h.Ranks[0] != 7 {
		t.Fatalf("got %v %v, want Trips(7 ...)", h.Category, h.Ranks)
	}
}

func TestEvaluate5Straight(t *testing.T) {
	h := evaluate5([]Card{c(14, 0), c(13, 1), c(12, 0), c(11, 2), c(10, 3)})
	if h.Category != Straight || h.Ranks[0] != 14 {
		t.Fatalf("got %v %v, want Straight(14)", h.Category, h.Ranks)
	}
}

func TestEvaluate5WheelStraight(t *testing.T) {
	h := evaluate5([]Card{c(14, 0), c(2, 1), c(3, 0), c(4, 2), c(5, 3)})
	if h.Category != Straight {
		t.Fatalf("got %v, want Straight", h.Category)
	}
	if h.Ranks[0] != 5 {
		t.Fatalf("wheel high = %d, want 5", h.Ranks[0])
	}
	// wheel (5-high) 应小于 6-high 顺子
	six := HandRank{Category: Straight, Ranks: []Rank{6}}
	if h.Compare(six) >= 0 {
		t.Fatalf("wheel straight should lose to 6-high straight")
	}
}

func TestEvaluate5Flush(t *testing.T) {
	h := evaluate5([]Card{c(14, 1), c(13, 1), c(10, 1), c(6, 1), c(2, 1)})
	if h.Category != Flush {
		t.Fatalf("got %v, want Flush", h.Category)
	}
}

func TestEvaluate5FullHouse(t *testing.T) {
	h := evaluate5([]Card{c(9, 0), c(9, 1), c(9, 2), c(4, 0), c(4, 2)})
	if h.Category != FullHouse {
		t.Fatalf("got %v, want FullHouse", h.Category)
	}
	if h.Ranks[0] != 9 || h.Ranks[1] != 4 {
		t.Fatalf("full house ranks = %v, want [9 4]", h.Ranks)
	}
}

func TestEvaluate5Quads(t *testing.T) {
	h := evaluate5([]Card{c(3, 0), c(3, 1), c(3, 2), c(3, 3), c(14, 2)})
	if h.Category != FourOfAKind || h.Ranks[0] != 3 || h.Ranks[1] != 14 {
		t.Fatalf("got %v %v, want Quads(3 kicker 14)", h.Category, h.Ranks)
	}
}

func TestEvaluate5StraightFlush(t *testing.T) {
	h := evaluate5([]Card{c(14, 0), c(13, 0), c(12, 0), c(11, 0), c(10, 0)})
	if h.Category != StraightFlush {
		t.Fatalf("got %v, want StraightFlush (royal)", h.Category)
	}
}

func TestEvaluate5WheelStraightFlush(t *testing.T) {
	h := evaluate5([]Card{c(14, 1), c(2, 1), c(3, 1), c(4, 1), c(5, 1)})
	if h.Category != StraightFlush {
		t.Fatalf("got %v, want StraightFlush (wheel)", h.Category)
	}
}
