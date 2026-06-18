package engine

import (
	"fmt"
	"strings"
)

// HandCategory 德州扑克的 9 种牌型,值越大越强。
type HandCategory int8

const (
	HighCard HandCategory = iota
	Pair
	TwoPair
	ThreeOfAKind
	Straight
	Flush
	FullHouse
	FourOfAKind
	StraightFlush
)

// String 返回牌型类别名,如 "Pair"、"Flush"、"StraightFlush"。
func (c HandCategory) String() string {
	names := [...]string{
		"HighCard", "Pair", "TwoPair", "ThreeOfAKind", "Straight",
		"Flush", "FullHouse", "FourOfAKind", "StraightFlush",
	}
	if c < 0 || int(c) >= len(names) {
		return fmt.Sprintf("HandCategory(%d)", int(c))
	}
	return names[c]
}

// HandRank 是一手 5 张牌的评估结果,可直接比较定胜负。
type HandRank struct {
	Category HandCategory
	// Ranks 是从高到低的关键牌点数,用于同类别时比大小。长度依类别固定。
	Ranks []Rank
}

// Compare 返回 >0 (h 更大) / 0 (平) / <0 (h 更小)。
func (h HandRank) Compare(o HandRank) int {
	if h.Category != o.Category {
		return int(h.Category) - int(o.Category)
	}
	n := len(h.Ranks)
	if len(o.Ranks) < n {
		n = len(o.Ranks)
	}
	for i := 0; i < n; i++ {
		if h.Ranks[i] != o.Ranks[i] {
			return int(h.Ranks[i]) - int(o.Ranks[i])
		}
	}
	return len(h.Ranks) - len(o.Ranks)
}

// String 返回可读形式,如 "Flush" / "Pair (K kicker Q 9)"。
func (h HandRank) String() string {
	if len(h.Ranks) == 0 {
		return h.Category.String()
	}
	parts := make([]string, 0, len(h.Ranks))
	for _, r := range h.Ranks {
		parts = append(parts, string(rankChars[r-2]))
	}
	return fmt.Sprintf("%s (%s)", h.Category, strings.Join(parts, " "))
}
