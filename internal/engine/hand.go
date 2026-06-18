package engine

import "math/rand"

// Street 表示一手牌的阶段。
type Street int8

const (
	Preflop Street = iota
	Flop
	Turn
	River
	Showdown
)

// String 返回阶段名(preflop/flop/turn/river/showdown)。
func (s Street) String() string {
	names := [...]string{"preflop", "flop", "turn", "river", "showdown"}
	if s < 0 || int(s) >= len(names) {
		return "Street(?)"
	}
	return names[s]
}

// ActionType 是玩家的动作类型。
type ActionType int8

const (
	Fold ActionType = iota
	Call
	Raise
)

// String 返回动作名(fold/call/raise)。
func (a ActionType) String() string {
	names := [...]string{"fold", "call", "raise"}
	if a < 0 || int(a) >= len(names) {
		return "ActionType(?)"
	}
	return names[a]
}

// Action 是玩家决策。Amount 仅在 Type=Raise 时有效,含义为「加注到多少」(raise-to)。
type Action struct {
	Type   ActionType
	Amount int
}

// Config 是一手牌的固定配置。
type Config struct {
	SmallBlind    int
	BigBlind      int
	StartingStack int
}

// PlayerSeat 是一个座位。Decide 是外部注入的决策回调。
type PlayerSeat struct {
	ID     int
	Stack  int
	Decide func(obs Observation) Action
}

// Observation 是引擎给玩家的可见信息(不含对手底牌)。
type Observation struct {
	HandID      int
	Street      Street
	HoleCards   []Card
	Community   []Card
	Pot         int
	ToCall      int  // 跟注需补多少;0 表示可 check
	MinRaise    int  // 最小加注到的额度(raise-to 下限)
	MyStack     int  // 当前剩余筹码(不含本街已投入)
	MyBet       int  // 本街已投入
	OpponentBet int  // 对手本街已投入
	IsButton    bool // 是否为按钮(SB)位
}

// EventType 是事件类型。
type EventType int8

const (
	BlindPosted EventType = iota
	DealtHole
	ActionTaken
	StreetAdvanced
	PotAwarded
	HandFinished
)

// Event 是引擎产出的状态变化记录,供回放/落库/Web 使用。
type Event struct {
	Type    EventType
	Street  Street
	Seat    int  // 相关玩家索引(-1 表示不适用)
	Action  *Action
	Cards   []Card // DealtHole / StreetAdvanced 时有效
	Amount  int    // BlindPosted / PotAwarded 时有效
	Winners []int  // HandFinished / PotAwarded 时有效
	Folded  bool   // HandFinished 时有效
	Message string // 可选的人类可读说明
}

// HandResult 是一手牌的最终结算。
type HandResult struct {
	Winners  []int         // 赢家 seat 索引(平局时多个)
	PotWon   int           // 赢家总入账
	Folded   bool          // 是否因弃牌结束
	Showdown *ShowdownInfo // 摊牌时非 nil
}

// ShowdownInfo 是摊牌细节。
type ShowdownInfo struct {
	Best5 [][]Card   // 每个 seat 的最佳 5 张
	Ranks []HandRank // 每个 seat 的 HandRank
}

// handState 是一手牌的内部可变状态。
type handState struct {
	cfg       Config
	seats     [2]PlayerSeat
	stacks    [2]int // 实时筹码(扣除已投入)
	bets      [2]int // 本街已投入
	hole      [2][]Card
	community []Card
	pot       int
	street    Street
	button    int // 按钮位 = SB 位
	sb, bb    int // SB / BB 的 seat 索引
	folded    [2]bool
	allIn     [2]bool
	handID    int
	deck      *Deck
}

// setupHand 初始化一手牌:扣盲注、发底牌,返回内部状态与初始事件。
// button=0 表示 seat0 是按钮(SB),button=1 表示 seat1 是按钮。
func setupHand(seats [2]PlayerSeat, button int, cfg Config, rng *rand.Rand, handID int) (*handState, []Event) {
	if button != 0 && button != 1 {
		panic("setupHand: button must be 0 or 1")
	}
	if cfg.SmallBlind <= 0 || cfg.BigBlind <= cfg.SmallBlind {
		panic("setupHand: invalid blinds")
	}
	if cfg.StartingStack < cfg.BigBlind {
		panic("setupHand: starting stack smaller than big blind")
	}

	sb := button
	bb := 1 - button

	st := &handState{
		cfg:    cfg,
		button: button,
		sb:     sb,
		bb:     bb,
		handID: handID,
		street: Preflop,
		deck:   NewDeck(WithRand(rng)),
	}
	st.seats[0] = seats[0]
	st.seats[1] = seats[1]
	st.stacks[0] = seats[0].Stack
	st.stacks[1] = seats[1].Stack

	var events []Event

	sbAmt := postBlind(st, sb, cfg.SmallBlind)
	events = append(events, Event{Type: BlindPosted, Seat: sb, Amount: sbAmt, Message: "small blind"})
	bbAmt := postBlind(st, bb, cfg.BigBlind)
	events = append(events, Event{Type: BlindPosted, Seat: bb, Amount: bbAmt, Message: "big blind"})

	st.hole[0] = drawN(st.deck, 2)
	st.hole[1] = drawN(st.deck, 2)
	events = append(events, Event{Type: DealtHole, Seat: 0, Cards: st.hole[0]})
	events = append(events, Event{Type: DealtHole, Seat: 1, Cards: st.hole[1]})

	return st, events
}

// postBlind 从玩家 stack 投入盲注(最多投入 stack),返回实际投入额。
// 若投入等于剩余 stack,标记 all-in。
func postBlind(st *handState, seat, amount int) int {
	if amount >= st.stacks[seat] {
		amount = st.stacks[seat]
		st.allIn[seat] = true
	}
	st.stacks[seat] -= amount
	st.bets[seat] += amount
	st.pot += amount
	return amount
}

// drawN 从牌堆抽 n 张。
func drawN(d *Deck, n int) []Card {
	out := make([]Card, 0, n)
	for i := 0; i < n; i++ {
		c, ok := d.Draw()
		if !ok {
			panic("drawN: deck exhausted")
		}
		out = append(out, c)
	}
	return out
}
