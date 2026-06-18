# Engine 地基设计（PLAN §9 第一步）

> 日期：2026-06-18
> 范围：仅 PLAN.md §9 立即可执行的第一步 —— `go mod init` + 目录骨架 + `engine/card.go` + 配套单测。
> 不包含：evaluator 实现、Player 接口、LLMPlayer、后续所有里程碑内容。

---

## 1. 目标

打下整个项目最确定、最可单测、零外部依赖的地基：一副标准 52 张扑克牌 + 可注入随机源的洗牌 + 抽牌。

## 2. 目录与模块骨架

只创建本步用得到的文件，不空建后续里程碑的目录：

```
pokermind/
├── go.mod                         # module pokermind, go 1.25
├── cmd/pokermind/main.go          # 占位 main()，打印 version；让 go build ./... 能过
└── internal/engine/
    ├── card.go                    # Card / Rank / Suit / Deck
    ├── card_test.go
    ├── evaluator.go               # 仅建文件 + 接口签名占位，实现放下一步
    └── evaluator_test.go          # 占位
```

- 不建 `players/ match/ store/ server/ web/`，避免空目录噪声。
- `cmd/pokermind/main.go` 只放最小 `main()`，真正 CLI 在 M0 后续接。
- `go.mod` 用 `go 1.25`。

## 3. `engine/card.go` 数据模型与 API

```go
package engine

type Rank int8   // 2..14（14 = A）
type Suit int8   // 0=♠ 1=♥ 2=♦ 3=♣

type Card struct {
    Rank Rank
    Suit Suit
}

type Deck struct { /* 内部 []Card + rng */ }

func NewDeck(opts ...DeckOption) *Deck
func WithRand(r *rand.Rand) DeckOption
func (d *Deck) Shuffle()
func (d *Deck) Draw() (Card, bool)   // bool=false 表示牌发完了
func (d *Deck) Remaining() int
```

设计决定：
- Rank/Suit 用 `int8` 而非字符串：省内存、天然可比较可排序、可作 map key。
- `Deck` 用 functional options 暴露随机源；`WithRand` 接受一个用 seed 构造的 `*rand.Rand`，便于固定 seed 复现 bug（PLAN §3 设计原则）。
- 默认随机源用 `math/rand` 的全局源（不锁定全局状态，测试都显式注入 seed）。
- 提供 `Card.String()`，格式 `"As"` / `"Th"`（rank 字符 + suit 字符），测试断言可读。
- 不引入外部依赖。

## 4. 单测覆盖（`card_test.go`）

固定 seed，保证可复现：

1. **Deck 完整性**：新 Deck 恰好 52 张，无重复，4 花 × 13 点。
2. **洗牌确定性**：同 seed 两次 Shuffle → 同序列；不同 seed → 不同序列（固定断言）。
3. **Draw 行为**：连抽 52 次全部成功，第 53 次 `ok=false`；抽出的 52 张作为集合恰好等于一副完整牌（与顺序无关）。
4. **String 格式**：`Card{Rank:14, Suit:0}` → `"As"`；`Card{Rank:10, Suit:1}` → `"Th"`。
5. **Card 可比较 / 可作 map key**：靠 Go struct 语义，测试只断言行为。

`evaluator.go` 本步只建文件 + 接口签名占位，不写实现，不写 evaluator 测试。

## 5. 验收标准

- `go build ./...` 通过。
- `go test ./...` 通过，且测试都基于固定 seed 可复现。
- `go vet ./...` 干净。

## 6. 不在本步范围

- HandEvaluator 牌型比较实现（PLAN §2 已决定自写，放在紧接的下一步）。
- `Player` 接口、`Observation` / `Action` / `SelfReport` 数据结构（PLAN §4，后续 M0 任务）。
- 任何 LLM、SQLite、Web 相关内容。
