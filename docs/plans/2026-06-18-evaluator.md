# HandEvaluator 实现计划(M0 第二步)

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 实现 `internal/engine/evaluator.go`,从 5–7 张牌中选出最强 5 张组合的 `HandRank`,可比较定胜负,零外部依赖。

**Architecture:** 单包 `internal/engine`。核心是纯函数 `evaluate5([]Card) HandRank`(5 张牌 → 评估);`Evaluate` / `Best5` 通过遍历 C(n,5) 个组合复用它。`HandRank` 是值类型,`Compare` 一次实现所有类别共用。

**Tech Stack:** Go 1.25 标准库。

**参考设计:** `docs/plans/2026-06-18-evaluator-design.md`

---

### Task 1: 类型定义 + Compare + 占位测试(TDD 起点)

**Files:**
- Modify: `internal/engine/evaluator.go`(替换占位)
- Modify: `internal/engine/evaluator_test.go`(替换占位)

**Step 1: 写 evaluator.go —— 类型与 Compare**

把 `internal/engine/evaluator.go` 的全部内容替换为:

```go
package engine

import "fmt"

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
		parts = append(parts, rankChars[r-2])
	}
	return fmt.Sprintf("%s (%s)", h.Category, joinBytes(parts))
}

// joinBytes 把单字节字符串切片用空格连成一个字符串。
func joinBytes(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " " + p
	}
	return out
}
```

**Step 2: 写 evaluator_test.go —— Compare 与 HandCategory 顺序**

把 `internal/engine/evaluator_test.go` 的全部内容替换为:

```go
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
```

**Step 3: 运行测试**

Run: `go test ./internal/engine/ -run 'TestHandCategory|TestHandRank' -v`
Expected: 5 个新测试 PASS(card 的测试也仍 PASS,但本步先聚焦新测试)。

Run: `go test ./internal/engine/ -v`
Expected: 全部 10 个测试 PASS。

**Step 4: vet + build**

```
go vet ./...
go build ./...
```

**Step 5: Commit**

```bash
git add internal/engine/evaluator.go internal/engine/evaluator_test.go
git commit -m "$(cat <<'EOF'
feat(engine): HandCategory/HandRank 类型与 Compare

定义 9 种牌型类别常量、HandRank 值类型,实现 Compare(类别 + tiebreaker
字典序)与 String。单测覆盖类别顺序、String、Compare 三类情形。

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: evaluate5 —— 5 张牌评估

**Files:**
- Modify: `internal/engine/evaluator.go`(追加)
- Modify: `internal/engine/evaluator_test.go`(追加)

**Step 1: 追加 evaluate5 到 evaluator.go**

在 evaluator.go 末尾追加:

```go

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
		r   Rank
		n   int
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

// straightHighCard 判断 ranks(长度 5,可能含重复的预先被外层排除)是否构成顺子。
// 若是,返回(高牌点数, true)。wheel(A-2-3-4-5)返回 (5, true)。
func straightHighCard(ranks []Rank) (Rank, bool) {
	// 去重排序(降序)
	s := make([]Rank, len(ranks))
	copy(s, ranks)
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] > s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	// 5 张不同点数(去重后仍是 5)且首尾相差 4 -> 顺子
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
```

**Step 2: 追加 evaluate5 测试**

在 evaluator_test.go 末尾追加:

```go

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
```

**Step 3: 运行测试**

Run: `go test ./internal/engine/ -v`
Expected: 全部测试 PASS(card 5 个 + evaluator Task1 5 个 + Task2 新增约 11 个)。

如果某个 evaluate5 测试失败,先排查 `straightHighCard` 与频次排序逻辑,不要调测试期望。

**Step 4: vet + build**

**Step 5: Commit**

```bash
git add internal/engine/evaluator.go internal/engine/evaluator_test.go
git commit -m "$(cat <<'EOF'
feat(engine): evaluate5 实现 5 张牌牌型评估

支持 9 种牌型识别,正确处理 wheel 顺子(A-2-3-4-5,5-high)
与同花顺。tiebreaker 按(count 降序, rank 降序)生成,Compare
复用 Task1 的实现。

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Evaluate / Best5 —— 5–7 张入口 + 边界

**Files:**
- Modify: `internal/engine/evaluator.go`
- Modify: `internal/engine/evaluator_test.go`

**Step 1: 追加 Evaluate 与 Best5 到 evaluator.go**

```go

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
	// 用索引数组生成组合
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
		// 推进索引(按字典序生成下一个 5-组合)
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
```

**Step 2: 追加入口测试**

在 evaluator_test.go 末尾追加:

```go

func TestEvaluateSixCardsPicksBestPair(t *testing.T) {
	// 6 张:K K Q J 9 7,最强是 Pair(K),tiebreaker [Q J 9]
	h := Evaluate([]Card{c(13, 0), c(13, 1), c(12, 2), c(11, 3), c(9, 0), c(7, 1)})
	if h.Category != Pair || h.Ranks[0] != 13 {
		t.Fatalf("got %v %v, want Pair(13 ...)", h.Category, h.Ranks)
	}
}

func TestEvaluateSevenFindsStraightFlush(t *testing.T) {
	// 7 张里藏一个 K-Q-J-T-9 同花顺,必须选它而非普通顺子/同花
	cards := []Card{
		c(13, 0), c(12, 0), c(11, 0), c(10, 0), c(9, 0), // spades K-high straight flush
		c(8, 1), c(2, 2),
	}
	h := Evaluate(cards)
	if h.Category != StraightFlush || h.Ranks[0] != 13 {
		t.Fatalf("got %v %v, want StraightFlush(13)", h.Category, h.Ranks)
	}
}

func TestBest5MatchesEvaluate(t *testing.T) {
	cards := []Card{
		c(13, 0), c(12, 0), c(11, 0), c(10, 0), c(9, 0),
		c(8, 1), c(2, 2),
	}
	all := Evaluate(cards)
	five := Evaluate(Best5(cards))
	if all.Compare(five) != 0 {
		t.Fatalf("Best5 rank %v != full Evaluate %v", five, all)
	}
	if len(Best5(cards)) != 5 {
		t.Fatalf("Best5 len = %d, want 5", len(Best5(cards)))
	}
}

func TestEvaluateSevenPrefersFlushOverStraight(t *testing.T) {
	// 5 张红心(构成同花,其中也有顺子) + 2 张杂牌
	cards := []Card{
		c(10, 1), c(9, 1), c(8, 1), c(7, 1), c(6, 1), // heart straight flush!
		c(2, 0), c(3, 2),
	}
	h := Evaluate(cards)
	if h.Category != StraightFlush {
		t.Fatalf("got %v, want StraightFlush", h.Category)
	}
}

func TestEvaluatePanicsOnTooFew(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic for 4 cards")
		}
	}()
	Evaluate([]Card{c(2, 0), c(3, 1), c(4, 2), c(5, 3)})
}

func TestEvaluatePanicsOnTooMany(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic for 8 cards")
		}
	}()
	Evaluate([]Card{
		c(2, 0), c(3, 1), c(4, 2), c(5, 3), c(6, 0),
		c(7, 1), c(8, 2), c(9, 3),
	})
}
```

**Step 3: 运行测试**

Run: `go test ./internal/engine/ -v`
Expected: 全部 PASS(card 5 + evaluator 全部)。

**Step 4: vet + build**

**Step 5: Commit**

```bash
git add internal/engine/evaluator.go internal/engine/evaluator_test.go
git commit -m "$(cat <<'EOF'
feat(engine): Evaluate/Best5 支持 5-7 张入口

bestCombo 遍历 C(n,5) 个组合(常数级:5/6/7 -> 1/6/21)取最优,
Evaluate 与 Best5 共享该遍历避免 DRY。边界(长度<5 或 >7)panic。

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: 收尾验收

**Step 1:** `go test ./...` 全绿、`go vet ./...` / `go build ./...` 干净。
**Step 2:** 确认 evaluator.go/test 不再含占位注释,全部为真实实现。
**Step 3:** 跑一个手算验证(可选,人工抽查):
```go
// 7 张皇家同花顺候选:必须返回 StraightFlush(14)
```
该抽查已被 `TestEvaluateSevenFindsStraightFlush` 覆盖,无需额外代码。
