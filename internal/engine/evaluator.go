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

// evaluate5 评估正好 5 张牌。cards 长度必须为 5,否则 panic。
func evaluate5(cards []Card) HandRank {
	if len(cards) != 5 {
		panic("evaluate5: need exactly 5 cards")
	}

	// 统计点数频次与花色频次
	rankCount := map[Rank]int{}
	suitCount := map[Suit]int{}
	ranks := make([]Rank, 5)
	for i, c := range cards {
		rankCount[c.Rank]++
		suitCount[c.Suit]++
		ranks[i] = c.Rank
	}
	isFlush := len(suitCount) == 1

	// 顺子判定(处理 wheel:A-2-3-4-5)
	straightHigh, ok := straightHighCard(ranks)
	isStraight := ok

	// 把 rank 频次按 (count 降序, rank 降序) 排序,作为 tiebreaker
	type rc struct {
		r Rank
		n int
	}
	counts := make([]rc, 0, len(rankCount))
	for r, n := range rankCount {
		counts = append(counts, rc{r, n})
	}
	// 简单插入排序(规模 <=5)
	for i := 1; i < len(counts); i++ {
		for j := i; j > 0; j-- {
			if counts[j].n > counts[j-1].n ||
				(counts[j].n == counts[j-1].n && counts[j].r > counts[j-1].r) {
				counts[j], counts[j-1] = counts[j-1], counts[j]
			} else {
				break
			}
		}
	}
	tb := make([]Rank, len(counts))
	for i, c := range counts {
		tb[i] = c.r
	}

	switch {
	case isStraight && isFlush:
		// 同花顺,wheel 用 5 作为高牌
		return HandRank{Category: StraightFlush, Ranks: []Rank{straightHigh}}
	case counts[0].n == 4:
		return HandRank{Category: FourOfAKind, Ranks: tb[:2]}
	case counts[0].n == 3 && counts[1].n == 2:
		return HandRank{Category: FullHouse, Ranks: tb[:2]}
	case isFlush:
		return HandRank{Category: Flush, Ranks: sortedDesc(ranks)}
	case isStraight:
		return HandRank{Category: Straight, Ranks: []Rank{straightHigh}}
	case counts[0].n == 3:
		return HandRank{Category: ThreeOfAKind, Ranks: tb[:3]}
	case counts[0].n == 2 && counts[1].n == 2:
		return HandRank{Category: TwoPair, Ranks: tb[:3]}
	case counts[0].n == 2:
		return HandRank{Category: Pair, Ranks: tb[:4]}
	default:
		return HandRank{Category: HighCard, Ranks: sortedDesc(ranks)}
	}
}

// straightHighCard 判断 ranks(长度 5)是否构成顺子。
// 若是,返回(高牌点数, true)。wheel(A-2-3-4-5)返回 (5, true)。
func straightHighCard(ranks []Rank) (Rank, bool) {
	// 降序排序后的副本
	s := make([]Rank, len(ranks))
	copy(s, ranks)
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] > s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	// 必须是 5 张互不相同的点数
	distinct := true
	for i := 1; i < len(s); i++ {
		if s[i] == s[i-1] {
			distinct = false
			break
		}
	}
	if !distinct {
		return 0, false
	}
	// 5 张不同点数且首尾相差 4 -> 顺子
	if len(s) == 5 && s[0]-s[4] == 4 {
		return s[0], true
	}
	// wheel: A(14) 5 4 3 2
	if len(s) == 5 && s[0] == 14 && s[1] == 5 && s[2] == 4 && s[3] == 3 && s[4] == 2 {
		return 5, true
	}
	return 0, false
}

// sortedDesc 返回 ranks 的降序副本。
func sortedDesc(ranks []Rank) []Rank {
	out := make([]Rank, len(ranks))
	copy(out, ranks)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] > out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// bestCombo 遍历 cards 的所有 C(n,5) 个 5 张组合,返回最强组合的 HandRank 与对应 5 张牌。
// cards 长度必须在 [5,7]。
func bestCombo(cards []Card) (HandRank, []Card) {
	if len(cards) < 5 || len(cards) > 7 {
		panic(fmt.Sprintf("bestCombo: cards length %d out of [5,7]", len(cards)))
	}
	if len(cards) == 5 {
		return evaluate5(cards), cards
	}
	var bestRank HandRank
	var bestCards []Card
	first := true
	// 用索引数组生成 5-组合(字典序)
	idx := []int{0, 1, 2, 3, 4}
	n := len(cards)
	for {
		combo := []Card{cards[idx[0]], cards[idx[1]], cards[idx[2]], cards[idx[3]], cards[idx[4]]}
		r := evaluate5(combo)
		if first || r.Compare(bestRank) > 0 {
			bestRank = r
			bestCards = combo
			first = false
		}
		// 推进索引到下一个 5-组合
		k := 4
		for k >= 0 && idx[k] == n-5+k {
			k--
		}
		if k < 0 {
			break
		}
		idx[k]++
		for j := k + 1; j < 5; j++ {
			idx[j] = idx[j-1] + 1
		}
	}
	return bestRank, bestCards
}

// Evaluate 从 5–7 张牌中选出最强 5 张组合并返回其 HandRank。
func Evaluate(cards []Card) HandRank {
	r, _ := bestCombo(cards)
	return r
}

// Best5 返回构成最强牌型的 5 张牌(从输入中选出)。多组并列时任取一组。
func Best5(cards []Card) []Card {
	_, c := bestCombo(cards)
	return c
}
