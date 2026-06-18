# Player 接口 + RuleBot 设计与实现计划(M0 第四步)

> 日期:2026-06-18
> 范围:把当前的 `PlayerSeat{Decide 闭包}` 升级为正式的 `Player` 接口;实现极简 `RuleBot` 作为基线对手。
> 不含:LLMPlayer、provider adapter、CLI、Match 编排。

---

## 1. 目标

让引擎的"决策注入点"正式定型。`PlayHand` 改为接受 `[2]Player`(加上 Stack 仍然在 PlayerSeat 上,因为 Stack 是引擎状态不是玩家身份)。RuleBot 用一个极简策略当基线:有牌力就跟,没牌就弃。

## 2. 设计

### 2.1 Player 接口

```go
// Player 是引擎认识的玩家。实现者可以是 RuleBot、LLMPlayer 等。
// 引擎只调用 Decide,不关心玩家是谁、怎么想。
type Player interface {
    Decide(obs Observation) Action
}
```

- 只一个方法。`Name`/`ID` 这些由调用方(Match / CLI)单独管理,不污染接口。
- 现有 `PlayerSeat.Decide` 是个闭包,天然满足 `Player` 接口(闭包类型 `func(Observation) Action` 不直接实现 interface,需要一个薄 adapter)。

### 2.2 PlayerSeat 调整

```go
type PlayerSeat struct {
    ID     int
    Stack  int
    Player Player  // 替换原来的 Decide 字段
}
```

`PlayHand` / `setupHand` / `buildObservation` 调用从 `st.seats[i].Decide(obs)` 改为 `st.seats[i].Player.Decide(obs)`。

> **breaking change:** 现有 `hand_test.go` 用 `Decide: alwaysCall()` 构造 seat,需要全部改为 `Player: playerFunc(alwaysCall())`。这是合理的迁移成本。

### 2.3 playerFunc adapter

为了让闭包能当 Player 用(测试代码方便),提供一个内置 adapter:

```go
// playerFunc 把一个闭包包成 Player。
type playerFunc func(Observation) Action

func (f playerFunc) Decide(obs Observation) Action { return f(obs) }

// PlayerFromFunc 用闭包构造 Player。
func PlayerFromFunc(f func(Observation) Action) Player { return playerFunc(f) }
```

### 2.4 RuleBot 策略

极简:

```go
// RuleBot 是极简规则基线:牌力够强就 call/check,否则 fold。
// 不主动 raise(避免规则复杂度)。"够强"阈值 = 至少一对。
type RuleBot struct{}

func (RuleBot) Decide(obs Observation) Action {
    // 评估当前所有可见牌(底牌 + 公共牌)的最强 5 张
    all := append(append([]Card{}, obs.HoleCards...), obs.Community...)
    if len(all) >= 5 {
        rank := Evaluate(all)
        // 至少一对(Pair 或更强)就跟/过;否则弃
        if rank.Category >= Pair {
            return Action{Type: Call}
        }
    } else {
        // preflop 公共牌还没翻,简单按底牌是否成对决定
        if obs.HoleCards[0].Rank == obs.HoleCards[1].Rank {
            return Action{Type: Call} // 口袋对子
        }
    }
    // 没 Pair 且要补钱 → fold;ToCall=0 → check(免费看牌没坏处)
    if obs.ToCall == 0 {
        return Action{Type: Call} // check
    }
    return Action{Type: Fold}
}
```

**策略要点:**
- 任何 street 用 Evaluate 看 hole+community 是否 >= Pair → call。
- preflop 公共牌未翻时,只看底牌是否口袋对 → call;否则按 ToCall 决定(check 免费 / fold 收费)。
- 永不主动 raise。这是"极简基线",不是"会打牌的 bot"。

## 3. 单测覆盖

1. **PlayerFromFunc 包装的闭包能当 Player 用**:`var p Player = PlayerFromFunc(...)`;调用 Decide 行为正确。
2. **RuleBot preflop 口袋对 → Call**:底牌 {K,K},公共牌空,断言 Call。
3. **RuleBot preflop 散牌 + ToCall>0 → Fold**:底牌 {K,2},ToCall=10,断言 Fold。
4. **RuleBot preflop 散牌 + ToCall=0 → Call(check)**:底牌 {K,2},ToCall=0,断言 Call(免费看牌)。
5. **RuleBot flop 成对 → Call**:底牌 {A,5},公共牌 {5,2,9},成一对 → Call。
6. **RuleBot flop 无对 + ToCall>0 → Fold**:底牌 {A,K},公共牌 {2,5,9},无对 → Fold。
7. **RuleBot 端到端跑通 PlayHand**:RuleBot vs 总是 all-in 的对手(用 PlayerFromFunc),固定 seed,断言不 panic、能拿到 HandResult。
8. **迁移现有测试**:所有原 `Decide: alwaysCall()` 改为 `Player: PlayerFromFunc(alwaysCall())`,47 个测试仍全绿。

## 4. 任务分解

### Task 1: Player 接口 + playerFunc adapter + PlayerSeat 改造

- 在 hand.go 加 `Player` 接口、`playerFunc`、`PlayerFromFunc`。
- `PlayerSeat.Decide` 字段改为 `Player Player`。
- 全文搜索 `Decide(`,改成 `Player.Decide(`(setupHand / runStreet 调用点)。
- 改造 hand_test.go:所有 `Decide: alwaysXxx()` 改成 `Player: PlayerFromFunc(alwaysXxx())`。
- 跑全部测试,确保 47 个仍全绿。
- Commit。

### Task 2: RuleBot 实现 + 测试

- 新建 `internal/engine/rulebot.go`,实现 `RuleBot{}` 与 `Decide`。
- 新建 `internal/engine/rulebot_test.go`,7 个测试覆盖上面 §3 的 2–7。
- 跑测试,全绿。
- Commit。

### Task 3: 收尾验收

- `go test ./...` / `go vet ./...` / `go build ./...` 全绿。
- 测试总数约 47 + 6 = 53。

## 5. 不在本步范围

- LLMPlayer、provider adapter。
- CLI 命令。
- Match 多手编排、ELO。
- RuleBot 升级(诈唬、加注、preflop 牌力表)—— 留到需要更强基线时。
