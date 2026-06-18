package engine

import (
	"math/rand"
	"testing"
)

func TestNewDeckHas52UniqueCards(t *testing.T) {
	d := NewDeck()
	if got := d.Remaining(); got != 52 {
		t.Fatalf("Remaining = %d, want 52", got)
	}
	seen := make(map[Card]bool, 52)
	for i := 0; i < 52; i++ {
		c, ok := d.Draw()
		if !ok {
			t.Fatalf("Draw #%d: ok=false, want true", i)
		}
		if seen[c] {
			t.Fatalf("duplicate card drawn: %v", c)
		}
		seen[c] = true
	}
	if len(seen) != 52 {
		t.Fatalf("unique drawn = %d, want 52", len(seen))
	}
}

func TestCardString(t *testing.T) {
	cases := []struct {
		c    Card
		want string
	}{
		{Card{Rank: 14, Suit: 0}, "As"},
		{Card{Rank: 10, Suit: 1}, "Th"},
		{Card{Rank: 2, Suit: 2}, "2d"},
		{Card{Rank: 13, Suit: 3}, "Kc"},
	}
	for _, tc := range cases {
		if got := tc.c.String(); got != tc.want {
			t.Errorf("%v.String() = %q, want %q", tc.c, got, tc.want)
		}
	}
}

// drawAll 把牌堆剩余牌全部抽出,顺序为 Draw 顺序。
func drawAll(d *Deck) []Card {
	out := make([]Card, 0, 52)
	for {
		c, ok := d.Draw()
		if !ok {
			break
		}
		out = append(out, c)
	}
	return out
}

func TestShuffleDeterministicWithSameSeed(t *testing.T) {
	d1 := NewDeck(WithRand(rand.New(rand.NewSource(42))))
	d1.Shuffle()
	seq1 := drawAll(d1)

	d2 := NewDeck(WithRand(rand.New(rand.NewSource(42))))
	d2.Shuffle()
	seq2 := drawAll(d2)

	if len(seq1) != 52 || len(seq2) != 52 {
		t.Fatalf("len = %d / %d, want 52", len(seq1), len(seq2))
	}
	for i := range seq1 {
		if seq1[i] != seq2[i] {
			t.Fatalf("seq differ at %d: %v vs %v", i, seq1[i], seq2[i])
		}
	}
}

func TestShuffleDifferentSeedsDiffer(t *testing.T) {
	d1 := NewDeck(WithRand(rand.New(rand.NewSource(1))))
	d1.Shuffle()
	d2 := NewDeck(WithRand(rand.New(rand.NewSource(2))))
	d2.Shuffle()
	s1, s2 := drawAll(d1), drawAll(d2)
	for i := range s1 {
		if s1[i] != s2[i] {
			return
		}
	}
	t.Fatalf("two different seeds produced identical sequence (suspicious)")
}

func TestDrawEmptyDeckReturnsFalse(t *testing.T) {
	d := NewDeck()
	for i := 0; i < 52; i++ {
		if _, ok := d.Draw(); !ok {
			t.Fatalf("Draw #%d: ok=false, want true", i)
		}
	}
	if _, ok := d.Draw(); ok {
		t.Fatalf("Draw #53: ok=true, want false")
	}
	if d.Remaining() != 0 {
		t.Fatalf("Remaining = %d, want 0", d.Remaining())
	}
}
