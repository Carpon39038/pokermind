# HandEvaluator 设计(M0 第二步)

> 日期:2026-06-18
> 范围:实现 `internal/engine/evaluator.go` —— 给定 5–7 张牌,算出最优 5 张组合的牌型,可比较定胜负。零外部依赖,自写。
> 不包含:HandState 状态机、Player 接口、任何 LLM/存储/Web 内容。

---

## 1. 目标

让引擎能回答「这一手谁的牌更大」。两个子问题:

1. 给 5–7 张牌,找出其中最强的 5 张组合,并给出其牌型与 tiebreaker。
2. 比较两个牌型结果,定胜负(赢/输/平)。

## 2. API

```go
package engine

// HandCategory 德州扑克的 9 种牌型,值越大越强。
type HandCategory int8

const (
    HighCard HandCategory = iota  // 0
    Pair                          // 1
    TwoPair                       // 2
    ThreeOfAKind                  // 3
    Straight                      // 4
    Flush                         // 5
    FullHouse                     // 6
    FourOfAKind                   // 7
    StraightFlush                 // 8 包含 Royal Flush(它是 A 高的同花顺)
)

// HandRank 是一手 5 张牌的评估结果,可直接比较定胜负。
type HandRank struct {
    Category HandCategory
    // Ranks 是从高到低的「关键牌点数」,用于同类别时比大小。
    // 长度依类别固定:
    //   FourOfAKind: [quad, kicker]            长度 2
    //   FullHouse:    [trip, pair]             长度 2
    //   Flush/Straight/HighCard: 5 张从高到低   长度 5(Straight 特殊见下)
    //   ThreeOfAKind: [trip, k1, k2]           长度 3
    //   TwoPair:      [hi_pair, lo_pair, k]    长度 3
    //   Pair:         [pair, k1, k2, k3]       长度 4
    //   StraightFlush: 同 Straight 的规则       长度 5
    Ranks []Rank
}

// Compare 返回 >0 (h 更大) / 0 (平) / <0 (h 更小)。
func (h HandRank) Compare(o HandRank) int

// String 返回可读形式,如 "Flush (K high)" / "TwoPair (K 8, K kicker Q)"。
// 供回放/日志/UI 使用。
func (h HandRank) String() string

// Evaluate 从 5–7 张牌中选出最强 5 张组合并返回其 HandRank。
// cards 长度必须在 [5,7]。长度 5/6/7 均遍历 C(n,5) 个组合取最大。
func Evaluate(cards []Card) HandRank

// Best5 返回构成最强牌型的 5 张牌(从输入的 7 张中)。
// 供回放/UI 展示「赢在哪 5 张」。若多组并列,任取一组。
func Best5(cards []Card) []Card
```

## 3. 设计要点

- **HandRank 可直接比较**:先比 Category,Category 相同则按 Ranks 字典序逐位比(都从高到低)。`Compare` 一次实现,所有类别共用。
- **A 既可当 14 也可当 1**(顺子 A-2-3-4-5,俗称 wheel):在 `straightRanks` 里特殊处理 —— wheel 的 Ranks 存 `[5,4,3,2,1]`(注意 1 而非 14),保证它比 6-high 顺子还小。
- **输入 5/6/7 张都支持**:`Evaluate` 遍历所有 C(n,5) 个 5 张组合,每个跑 5-张评估器,取 `Compare` 最大的。组合数:5→1, 6→6, 7→21,常数级,无需优化。
- **零外部依赖**:`chehsunliu/poker` 等库允许,但本步选自写(已与用户确认),练习价值最大,且代码量小。
- **5 张评估器**(`evaluate5`):核心纯函数,做四件事 —— 数 rank 频次、数 suit 频次、判定顺子、组合出 HandRank。
- **`Best5`**:复用 `Evaluate` 的遍历逻辑,但记录「最强组合对应的 5 张原牌」。可选地,把遍历逻辑抽成一个内部 `bestCombo(cards)` 同时返回 rank 与 5 张牌,让 `Evaluate` 和 `Best5` 都调它,避免 DRY。

## 4. 单测覆盖

固定输入,断言确定结果:

1. **HandCategory 顺序**:9 个常量按 HighCard < ... < StraightFlush。
2. **Compare 各类别**:类别不同 → 类别大的赢;类别同 → Ranks 字典序;完全相同 → 平。
3. **evaluate5 各类别正确归类**:
   - HighCard:无对无顺无同花
   - Pair / TwoPair / Trips / FullHouse / Quads:各种频次组合
   - Straight:普通顺(A-K-Q-J-T 高顺)与 wheel(A-2-3-4-5 低顺),wheel < 6-high
   - Flush:5 张同花非顺
   - StraightFlush:含 Royal(A 高同花顺)与 wheel 同花顺
4. **Evaluate 多组合**:
   - 6 张输入:其中一对 + 3 散牌,返回 Pair 且 Ranks 正确。
   - 7 张输入:给出能组成同花顺的局面,`Evaluate` 必须选出同花顺(而非普通顺子或同花)。
5. **Best5**:给定 7 张(含一个明确的同花),`Best5` 返回的 5 张必须是同花的且其 `Evaluate` 与对全 7 张的 `Evaluate` 相同。
6. **String 可读性**:至少断言 `"Flush"` / `"Straight"` / `"TwoPair"` 等关键词出现在 String 输出里。
7. **边界**:`Evaluate` 输入长度 <5 或 >7 时 panic 或返回错误(选 panic + 文档,因为这是程序员错误而非运行时错误)。

## 5. 验收

- `go test ./internal/engine/ -v` 全绿。
- `go vet ./...` / `go build ./...` 干净。
- 删除当前 `evaluator.go` / `evaluator_test.go` 中的占位注释,换成真实实现。

## 6. 不在本步范围

- 多人边池(side pot)结算逻辑(留 6-max,M3)。
- 牌型概率/蒙特卡洛胜率(PLAN §1.2 已明确本期不做)。
- 任何与 Player / Match / Store 相关的内容。
