# HandState 状态机设计(M0 第三步)

> 日期:2026-06-18
> 范围:单手 Heads-up 德州扑克状态机 —— 盲注、preflop/flop/turn/river 下注轮、摊牌结算。
> 不含:Player 接口(用闭包注入决策函数)、LLM、SQLite、Web、Match 编排、ELO、边池(Heads-up 无边池)。

---

## 1. 目标

给定两个玩家(用决策函数表示)和可注入随机源,完整跑完一手牌,产出事件流 + 结算结果。引擎本身不知道任何"模型"或"AI"的存在 —— 决策来自外部回调,这样引擎可单测,也能塞规则 bot 或 LLM。

## 2. 关键设计决定

### 2.1 动作语义(已与用户确认)

```go
type Action struct {
    Type   ActionType  // Fold / Call / Raise
    Amount int         // Type=Raise 时是「加到的总额」(raise-to),Type=Fold/Call 忽略
}
```

- `Call` 兼任 `Check`:当 `ToCall=0` 时,Call 即 check,不投钱。
- `Raise` 用**绝对总额**(raise-to):例如底池 100、对手下到 60,raise-to 150 表示「我下到 150,需补 90」。这与真实赌场一致,LLM 不易把"加注 50"(增量)和"加注到 50"(总额)搞混。
- 无 `Bet` / `Check` / `AllIn` 独立动作:`Bet` 用 `Raise` 表达(从 ToCall=0 加注);`AllIn` 是 `Call` 或 `Raise` 的副作用(下注超过自身筹码时截断),不需要单独类型。

### 2.2 Heads-up 盲注与按钮

Heads-up 中:**按钮位 = 小盲位**,且**preflop 由按钮(SB)先行动**,flop 之后由**大盲(BB)先行动**。这是 Heads-up 特殊规则(与多人桌不同),要测覆盖。

- 小盲 = 起始筹码的 1/2 的下限(标准:SB=5,BB=10,起步 1000);本步用 `Config{SmallBlind, BigBlind, StartingStack}` 参数化,不硬编码。
- 两人各先扣盲注入底池,然后发底牌(各 2 张),进入 preflop 下注轮。

### 2.3 下注轮终止条件

一个 street 在以下任一条件满足时结束:
1. 除最后下注者外,所有人都「匹配了当前最高下注」(matched),且**每个人都有过行动机会**(防止"某人下注后立刻结束")。
2. 仅剩 1 名未弃牌玩家(其他人都 fold)。
3. 所有人都 all-in(本步简化:若双方都 all-in,直接跑完剩余 street 到摊牌,不再询问决策)。

每轮结束时:把本轮所有 bet 收入 pot,community 牌按规则翻(flop 3 张,turn/river 各 1 张)。

### 2.4 街序

`Preflop → Flop → Turn → River → Showdown`

到摊牌时,用已实现的 `Evaluate` 比较两人最佳 5 张(底牌 + 公共牌),定赢家;平局平分;若中途有人 fold,另一人直接拿底池(不摊牌)。

### 2.5 事件流

每个状态变化产出一个 `Event`,追加到 `[]Event`。事件类型:`BlindPosted / DealtHole / ActionTaken / StreetAdvanced / PotAwarded / HandFinished`。事件流是引擎唯一的对外输出,Web 回放、SQLite 落库都从它派生。

## 3. API(草案)

```go
package engine

type ActionType int8
const (
    Fold ActionType = iota
    Call
    Raise
)

type Action struct {
    Type   ActionType
    Amount int  // Raise 时为「加到多少」(raise-to)
}

// PlayerSeat 是一个座位,Decide 是外部注入的决策回调。
// 引擎不关心它是 RuleBot 还是 LLMPlayer。
type PlayerSeat struct {
    ID     int
    Stack  int
    Decide func(obs Observation) Action
}

type Observation struct {
    HandID     int
    Street     Street
    HoleCards  []Card
    Community  []Card
    Pot        int
    ToCall     int  // 跟注需要补多少(call-to 的差额);0 表示可 check
    MinRaise   int  // 最小加注到的额度(= 当前最高下注 + 最小加注增量)
    MyStack    int
    MyBet      int  // 本街已投入
    OpponentBet int
    IsButton   bool
}

type Street int8
const (
    Preflop Street = iota
    Flop
    Turn
    River
    Showdown
)

type Config struct {
    SmallBlind   int
    BigBlind     int
    StartingStack int
}

// PlayHand 跑完一手 Heads-up 牌。
// button=0 表示 seat0 是按钮(SB),button=1 表示 seat1 是按钮。
// 用注入的 rng 发牌以保证可复现。
// 返回完整事件流与最终结算结果。
func PlayHand(seats [2]PlayerSeat, button int, cfg Config, rng *rand.Rand, handID int) (events []Event, result HandResult)
```

> 注:`PlayerSeat` 这里是「引擎视角的玩家」,**不是 PLAN §4 的 `Player` 接口**。后者会在下一步引入(LLMPlayer 实现它),那时 `PlayHand` 会改成接受 `[2]Player` 接口而不是 `Decide` 回调。本步先用闭包,目的是让引擎可单测且不被接口设计拖累。

## 4. HandResult

```go
type HandResult struct {
    Winners []int  // 赢家 seat 索引(可能多个=平局)
    PotWon  int    // 赢家拿走的总筹码
    Folded  bool   // 是否因对手弃牌结束(非摊牌)
    // 摊牌时(非 fold)填充:
    Showdown *ShowdownInfo
}

type ShowdownInfo struct {
    Best5    [][]Card  // 每个 seat 的最佳 5 张
    Ranks    []HandRank // 每个 seat 的 HandRank
}
```

## 5. 单测覆盖(关键场景)

固定 seed,确定性断言:

1. **盲注正确扣费**:SB 扣 SB,BB 扣 BB,两人 stack 减少对应额度,pot = SB+BB。
2. **preflop 行动顺序**:按钮(SB)先行动;BB 最后行动且有「option to act」(即使 SB 只是 call,BB 仍可 raise)。
3. **postflop 行动顺序**:BB 先行动。
4. **Call = Check 当 ToCall=0**:BB 在 preflop 面对 SB 的 call,可以再 check(进入 flop)或 raise。
5. **Fold 结算**:一方 fold,另一方直接拿 pot,`Folded=true`,无 ShowdownInfo。
6. **Raise-to 语义**:raise-to X 的玩家本街总投入变成 X(不是 X+已投)。
7. **MinRaise 校验**:raise-to 低于 MinRaise 时引擎如何处理(策略:若发生,视为非法动作 → 当作 Fold?本设计选:**引擎信任 Decide 返回合法动作,违规直接 panic**(程序员错误),理由:LLM 不合规由 Player adapter 层重试/兜底,引擎不替它擦屁股)。
8. **All-in 截断**:玩家筹码不足以 call 时,Call 把剩余全部投入,后续 street 跳过决策直接发完。
9. **摊牌结算**:用固定 seed 发出已知牌,断言赢家正确(可用 HandRank 手算)。
10. **平局**:构造一个已知平局局面(共享公共牌、底牌等价),断言两人都进 Winners、PotWon 平分。
11. **筹码不为负**:任何动作后两人 stack 都 ≥ 0。
12. **street 顺序**:事件流里 StreetAdvanced 按顺序出现 Preflop→Flop→Turn→River→Showdown。

## 6. 验收

- `go test ./...` 全绿。
- `go vet ./...` / `go build ./...` 干净。
- 新建 `internal/engine/hand.go` + `hand_test.go`。
- 引擎与外部决策完全解耦:测试用闭包 `func(obs) Action`,不引入任何 mock 框架。

## 7. 不在本步范围

- Player 接口、RuleBot、LLMPlayer(下一步)。
- Match 多手编排、ELO。
- 边池(Heads-up 无)。
- 复式发牌(PLAN §1.2 已明确本期不做)。
- 内心戏采集(那是 Action 层的事,引擎只产 Event)。
