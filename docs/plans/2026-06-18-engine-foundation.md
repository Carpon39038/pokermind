# Engine 地基实现计划（PLAN §9 第一步）

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 建立 `pokermind` Go 模块骨架,并实现零外部依赖的 `engine/card.go`(Card/Rank/Suit/Deck + 可注入 seed 的洗牌 + Draw),配齐可复现单测。

**Architecture:** 单一 `internal/engine` 包,纯标准库。`Deck` 用 functional options 注入 `*rand.Rand`,默认用 `math/rand` 全局源。Rank/Suit 用 `int8`,Card 是值类型可比较。

**Tech Stack:** Go 1.25,标准库 `math/rand`,标准 `testing`。

**参考设计:** `docs/plans/2026-06-18-engine-foundation-design.md`

---

### Task 1: 模块与目录骨架

**Files:**
- Create: `go.mod`
- Create: `cmd/pokermind/main.go`
- Create: `internal/engine/evaluator.go`(占位)
- Create: `internal/engine/evaluator_test.go`(占位)

**Step 1: 初始化模块**

Run:
```bash
cd /Users/carpon/projects/pokermind
go mod init pokermind
```
Expected: `go: creating new go.mod: module pokermind` 并生成 `go.mod`,内含 `module pokermind` 与 `go 1.25`。

**Step 2: 写占位 main**

Create `cmd/pokermind/main.go`:
```go
package main

import "fmt"

func main() {
	fmt.Println("pokermind dev")
}
```

**Step 3: 写占位 evaluator 文件**

Create `internal/engine/evaluator.go`:
```go
package engine

// HandEvaluator 比较 7 选 5 的最优牌型。
// 实现留给下一步,本文件仅为占位以稳定包结构。
```

Create `internal/engine/evaluator_test.go`:
```go
package engine

// evaluator 测试将在实现落地后补齐。
```

**Step 4: 验证构建与 vet**

Run:
```bash
go build ./...
go vet ./...
```
Expected: 两条都无输出、退出码 0。

**Step 5: Commit**

```bash
git add go.mod cmd/pokermind/main.go internal/engine/evaluator.go internal/engine/evaluator_test.go
git commit -m "$(cat <<'EOF'
chore: 初始化 pokermind 模块骨架

go mod init pokermind, 建立	cmd/pokermind 与 internal/engine 包
骨架, evaluator 仅占位待下一步实现。

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: 先写 card 单测(TDD,Deck 完整性 + String)

**Files:**
- Create: `internal/engine/card_test.go`

> 说明:本计划把 Card/Deck 测试分两批写,以保持每个 TDD 循环聚焦。这一批覆盖「不依赖洗牌随机性」的行为。

**Step 1: 写失败测试**

Create `internal/engine/card_test.go`:
```go
package engine

import "testing"

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
```

**Step 2: 运行测试,确认失败**

Run: `go test ./internal/engine/`
Expected: 编译失败,`undefined: NewDeck`、`undefined: Card` 等。

---

### Task 3: 实现 card.go 最小代码让 Task 2 通过

**Files:**
- Create: `internal/engine/card.go`

**Step 1: 写最小实现**

Create `internal/engine/card.go`:
```go
package engine

// Rank 表示点数:2..14,其中 14=A。
type Rank int8

// Suit 表示花色:0=黑桃 ♠, 1=红心 ♥, 2=方块 ♦, 3=梅花 ♣。
type Suit int8

// Card 是一张扑克牌,值类型,可比较、可作 map key。
type Card struct {
	Rank Rank
	Suit Suit
}

var rankChars = [...]byte{'2', '3', '4', '5', '6', '7', '8', '9', 'T', 'J', 'Q', 'K', 'A'}
var suitChars = [...]byte{'s', 'h', 'd', 'c'}

// String 返回两张牌的常见文本表示,如 "As" / "Th" / "2d" / "Kc"。
func (c Card) String() string {
	return string([]byte{rankChars[c.Rank-2], suitChars[c.Suit]})
}

// DeckOption 用于在 NewDeck 注入随机源等配置。
type DeckOption func(*Deck)

// WithRand 注入一个已 seed 化的 *rand.Rand,便于复现某一局。
func WithRand(r *Rand) DeckOption {
	return func(d *Deck) { d.rng = r }
}

// Deck 是一副牌。
type Deck struct {
	cards []Card
	rng   *Rand
}

// NewDeck 返回一张未洗、按花色与点数顺序排列的 52 张完整牌。
// 默认随机源为包级全局源;测试请用 WithRand 注入固定 seed。
func NewDeck(opts ...DeckOption) *Deck {
	d := &Deck{cards: make([]Card, 0, 52)}
	for s := Suit(0); s < 4; s++ {
		for r := Rank(2); r <= 14; r++ {
			d.cards = append(d.cards, Card{Rank: r, Suit: s})
		}
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Remaining 返回牌堆中剩余牌数。
func (d *Deck) Remaining() int { return len(d.cards) }
```

> 注:`Rand` 与 `Draw`/`Shuffle` 在下一个 Task 加。本 Task 不引入 `math/rand`,避免有未用 import。

**Step 2: 运行测试,确认仍失败(因为还缺 Draw)**

Run: `go test ./internal/engine/`
Expected: 编译失败,`undefined: (d *Deck).Draw`、`undefined: Rand`。

—— 这是预期的。下一个 Task 补齐。

---

### Task 4: 补齐 Rand 类型、Draw 与 Shuffle,以及它们的测试

**Files:**
- Modify: `internal/engine/card.go`
- Modify: `internal/engine/card_test.go`(追加测试)
- Modify: `internal/engine/card_test.go`(把 `*Rand` 用法改对)

> 设计:`Rand` 是对 `math/rand.Rand` 的薄别名,只为让 `WithRand` 签名干净。若不想引别名,可直接用 `*math/rand.Rand`,但下面采用别名方案,测试与实现签名一致。

**Step 1: 修改 card.go 引入 rand**

在 `card.go` 顶部加 import,并定义 `Rand` 类型别名:
```go
import "math/rand"

// Rand 是 math/rand.Rand 的别名,用于随机源注入。
type Rand = rand.Rand
```

> 等价于直接用 `*rand.Rand`;别名只是让 WithRand 签名更短。`type X = Y` 是别名而非新类型,赋值兼容。

**Step 2: 实现 Draw 与 Shuffle**

在 `card.go` 追加:
```go
// Draw 取出牌堆顶一张。ok=false 表示已发完。
func (d *Deck) Draw() (Card, bool) {
	if len(d.cards) == 0 {
		return Card{}, false
	}
	c := d.cards[len(d.cards)-1]
	d.cards = d.cards[:len(d.cards)-1]
	return c, true
}

// rngOrDefault 返回注入的随机源;未注入则用全局源。
func (d *Deck) rngOrDefault() *Rand {
	if d.rng != nil {
		return d.rng
	}
	return globalRand
}

var globalRand = rand.New(rand.NewSource(1)) // 默认 seed=1,行为可预测

// Shuffle 用注入或默认随机源洗牌(Fisher-Yates)。
func (d *Deck) Shuffle() {
	r := d.rngOrDefault()
	for i := len(d.cards) - 1; i > 0; i-- {
		j := r.Intn(i + 1)
		d.cards[i], d.cards[j] = d.cards[j], d.cards[i]
	}
}
```

**Step 3: 追加测试到 card_test.go**

在 `card_test.go` 末尾追加:
```go
import "math/rand"

func TestShuffleDeterministicWithSameSeed(t *testing.T) {
	drawAll := func(d *Deck) []Card {
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
	drawAll := func(d *Deck) []Card {
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
	d1 := NewDeck(WithRand(rand.New(rand.NewSource(1))))
	d1.Shuffle()
	d2 := NewDeck(WithRand(rand.New(rand.NewSource(2))))
	d2.Shuffle()
	same := true
	for i := range d1.cards {
		_ = i
	}
	_ = d2
	// 简化:两组序列不应完全相同(52! 几乎不可能撞)
	s1, s2 := drawAll(d1), drawAll(d2)
	for i := range s1 {
		if s1[i] != s2[i] {
			same = false
			break
		}
	}
	if same {
		t.Fatalf("two different seeds produced identical sequence (suspicious)")
	}
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
```

> 上面的 `TestShuffleDifferentSeedsDiffer` 里有一段无用的 `for i := range d1.cards` 是为了演示「不要留死代码」——实际写测试时**删掉它**。计划里写出来是为了提醒:别抄进无用代码。最终实现请精简为:
> ```go
> func TestShuffleDifferentSeedsDiffer(t *testing.T) {
> 	drawAll := func(d *Deck) []Card {
> 		out := make([]Card, 0, 52)
> 		for {
> 			c, ok := d.Draw()
> 			if !ok { break }
> 			out = append(out, c)
> 		}
> 		return out
> 	}
> 	d1 := NewDeck(WithRand(rand.New(rand.NewSource(1)))); d1.Shuffle()
> 	d2 := NewDeck(WithRand(rand.New(rand.NewSource(2)))); d2.Shuffle()
> 	s1, s2 := drawAll(d1), drawAll(d2)
> 	for i := range s1 {
> 		if s1[i] != s2[i] { return }
> 	}
> 	t.Fatalf("two different seeds produced identical sequence (suspicious)")
> }
> ```

**Step 4: 运行全部测试**

Run: `go test ./internal/engine/ -v`
Expected: 5 个测试全部 PASS。

**Step 5: vet + build**

Run:
```bash
go vet ./...
go build ./...
```
Expected: 无输出、退出码 0。

**Step 6: Commit**

```bash
git add internal/engine/card.go internal/engine/card_test.go
git commit -m "$(cat <<'EOF'
feat(engine): 实现 Card/Rank/Suit/Deck 与可注入 seed 的洗牌

- Card 为 int8 值类型,String 返回 "As"/"Th" 等可读形式
- Deck 用 functional options,WithRand 注入 *rand.Rand 以复现牌局
- Draw 在牌堆空时返回 ok=false
- 单测覆盖:完整性、确定性洗牌、不同 seed 不同序列、空堆 Draw

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: 收尾验收

**Step 1: 全量测试 + vet + build**

Run:
```bash
go test ./...
go vet ./...
go build ./...
```
Expected: 三条均成功(test 打印 PASS,vet/build 无输出)。

**Step 2: 确认目录与 PLAN §9 一致**

Run: `ls -R cmd internal`
Expected: 仅见 `cmd/pokermind/main.go` 与 `internal/engine/{card.go,card_test.go,evaluator.go,evaluator_test.go}`,无多余空目录。

**Step 3: 无需额外提交**

若 Step 1 全绿,本步无代码改动,不产生提交。地基完成。
