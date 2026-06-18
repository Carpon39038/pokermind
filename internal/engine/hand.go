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

// currentMaxBet 返回本街两人中的最高下注。
func (st *handState) currentMaxBet() int {
	if st.bets[0] > st.bets[1] {
		return st.bets[0]
	}
	return st.bets[1]
}

// toCallFor 返回某 seat 跟注需补多少(= 本街最高下注 - 自己本街已投入,下限 0)。
func (st *handState) toCallFor(seat int) int {
	diff := st.currentMaxBet() - st.bets[seat]
	if diff < 0 {
		return 0
	}
	return diff
}

// minRaiseTo 返回最小 raise-to 额度。简化规则:= 当前最高下注 + BB。
func (st *handState) minRaiseTo() int {
	return st.currentMaxBet() + st.cfg.BigBlind
}

// buildObservation 构造给某 seat 的可见信息。
func (st *handState) buildObservation(seat int) Observation {
	opp := 1 - seat
	return Observation{
		HandID:      st.handID,
		Street:      st.street,
		HoleCards:   st.hole[seat],
		Community:   st.community,
		Pot:         st.pot + st.bets[0] + st.bets[1],
		ToCall:      st.toCallFor(seat),
		MinRaise:    st.minRaiseTo(),
		MyStack:     st.stacks[seat],
		MyBet:       st.bets[seat],
		OpponentBet: st.bets[opp],
		IsButton:    seat == st.button,
	}
}

// firstActor 返回本街的第一个行动者。Preflop: SB(按钮)先;Postflop: BB 先。
func (st *handState) firstActor() int {
	if st.street == Preflop {
		return st.sb
	}
	return st.bb
}

// betsEqual 返回两人本街下注是否相等。
func (st *handState) betsEqual() bool { return st.bets[0] == st.bets[1] }

// runStreet 跑完一个 street 的下注轮,把 ActionTaken 事件追加到 events 并返回。
func (st *handState) runStreet(events []Event) []Event {
	// 双方 all-in,跳过(由调用方处理剩余街)
	if st.allIn[0] && st.allIn[1] {
		return events
	}

	actor := st.firstActor()
	acted := [2]bool{false, false}

	for {
		// 一方已弃牌,立刻结束
		if st.folded[0] || st.folded[1] {
			break
		}
		// 双方都 all-in,结束
		if st.allIn[0] && st.allIn[1] {
			break
		}
		// 行动者已 all-in,跳过并把 acted 置位
		if st.allIn[actor] {
			acted[actor] = true
			// 若两人都行动过且下注相等,结束
			if acted[0] && acted[1] && st.betsEqual() {
				break
			}
			actor = 1 - actor
			continue
		}

		obs := st.buildObservation(actor)
		action := st.seats[actor].Decide(obs)
		_, ev := st.applyAction(actor, action)
		events = append(events, ev)
		acted[actor] = true

		if action.Type == Fold {
			break
		}
		// 终止:两人都行动过且下注相等
		if acted[0] && acted[1] && st.betsEqual() {
			break
		}
		// 双方都 all-in,结束
		if st.allIn[0] && st.allIn[1] {
			break
		}
		actor = 1 - actor
	}
	return events
}

// applyAction 应用一个动作,返回是否触发 all-in 与事件。
// 非法动作(raise-to 过低/超筹、未知 Type)panic。
func (st *handState) applyAction(seat int, a Action) (allIn bool, ev Event) {
	ev = Event{Type: ActionTaken, Street: st.street, Seat: seat, Action: &a}
	switch a.Type {
	case Fold:
		st.folded[seat] = true
	case Call:
		need := st.toCallFor(seat)
		if need > st.stacks[seat] {
			need = st.stacks[seat]
		}
		st.stacks[seat] -= need
		st.bets[seat] += need
		if st.stacks[seat] == 0 {
			st.allIn[seat] = true
			allIn = true
		}
	case Raise:
		if a.Amount <= st.bets[seat] {
			panic("applyAction: raise-to must be greater than current bet")
		}
		delta := a.Amount - st.bets[seat]
		if delta > st.stacks[seat] {
			panic("applyAction: raise-to exceeds stack")
		}
		// 允许 all-in 不足 minRaise,否则必须 >= minRaiseTo
		isAllIn := (a.Amount == st.bets[seat]+st.stacks[seat])
		if a.Amount < st.minRaiseTo() && !isAllIn {
			panic("applyAction: raise-to below minimum")
		}
		st.stacks[seat] -= delta
		st.bets[seat] = a.Amount
		if st.stacks[seat] == 0 {
			st.allIn[seat] = true
			allIn = true
		}
	default:
		panic("applyAction: unknown action type")
	}
	return allIn, ev
}

// advanceStreet 把本街 bets 累加进 pot,翻公共牌,进入下一 street。
// Preflop→Flop(3 张),Flop→Turn(1 张),Turn→River(1 张),River→Showdown(不翻牌)。
func (st *handState) advanceStreet(events []Event) []Event {
	// 把本街 bets 清零并入 pot
	st.pot += st.bets[0] + st.bets[1]
	st.bets[0] = 0
	st.bets[1] = 0

	switch st.street {
	case Preflop:
		flop := drawN(st.deck, 3)
		st.community = append(st.community, flop...)
		st.street = Flop
		events = append(events, Event{Type: StreetAdvanced, Street: Flop, Cards: flop})
	case Flop:
		turn := drawN(st.deck, 1)
		st.community = append(st.community, turn...)
		st.street = Turn
		events = append(events, Event{Type: StreetAdvanced, Street: Turn, Cards: turn})
	case Turn:
		river := drawN(st.deck, 1)
		st.community = append(st.community, river...)
		st.street = River
		events = append(events, Event{Type: StreetAdvanced, Street: River, Cards: river})
	case River:
		st.street = Showdown
		events = append(events, Event{Type: StreetAdvanced, Street: Showdown})
	}
	return events
}

// settleShowdown 用 Evaluate/Best5 定赢家,返回(赢家列表, ShowdownInfo)。
func (st *handState) settleShowdown() ([]int, ShowdownInfo) {
	all0 := append(append([]Card{}, st.hole[0]...), st.community...)
	all1 := append(append([]Card{}, st.hole[1]...), st.community...)
	r0 := Evaluate(all0)
	r1 := Evaluate(all1)
	info := ShowdownInfo{
		Best5: [][]Card{Best5(all0), Best5(all1)},
		Ranks: []HandRank{r0, r1},
	}
	switch {
	case r0.Compare(r1) > 0:
		return []int{0}, info
	case r0.Compare(r1) < 0:
		return []int{1}, info
	default:
		return []int{0, 1}, info
	}
}

// awardPot 把 pot 分给赢家(平局平分,余数给首位),清零 pot。
func (st *handState) awardPot(winners []int) {
	n := len(winners)
	if n == 0 {
		return
	}
	share := st.pot / n
	rem := st.pot - share*n
	for i, w := range winners {
		add := share
		if i == 0 {
			add += rem
		}
		st.stacks[w] += add
	}
	st.pot = 0
}

// PlayHand 完整跑完一手 Heads-up 牌,返回事件流与结算结果。
// button=0 表示 seat0 是按钮(SB)。
func PlayHand(seats [2]PlayerSeat, button int, cfg Config, rng *rand.Rand, handID int) ([]Event, HandResult) {
	st, events := setupHand(seats, button, cfg, rng, handID)

	for {
		// 一方已弃牌(理论上 setupHand 后不会立即 fold,但 runStreet 后可能)
		if st.folded[0] || st.folded[1] {
			winner := 0
			if st.folded[0] {
				winner = 1
			}
			pot := st.pot + st.bets[0] + st.bets[1]
			events = append(events, Event{Type: PotAwarded, Winners: []int{winner}, Amount: pot})
			st.pot = pot
			st.bets[0] = 0
			st.bets[1] = 0
			st.awardPot([]int{winner})
			events = append(events, Event{Type: HandFinished, Folded: true, Winners: []int{winner}})
			return events, HandResult{Winners: []int{winner}, PotWon: pot, Folded: true}
		}

		events = st.runStreet(events)

		// runStreet 后再检查 fold
		if st.folded[0] || st.folded[1] {
			winner := 0
			if st.folded[0] {
				winner = 1
			}
			pot := st.pot + st.bets[0] + st.bets[1]
			events = append(events, Event{Type: PotAwarded, Winners: []int{winner}, Amount: pot})
			st.pot = pot
			st.bets[0] = 0
			st.bets[1] = 0
			st.awardPot([]int{winner})
			events = append(events, Event{Type: HandFinished, Folded: true, Winners: []int{winner}})
			return events, HandResult{Winners: []int{winner}, PotWon: pot, Folded: true}
		}

		// 若任一方 all-in,跳过决策,翻完剩余 street
		if st.allIn[0] || st.allIn[1] {
			for st.street != Showdown {
				events = st.advanceStreet(events)
			}
		} else {
			events = st.advanceStreet(events)
		}

		if st.street == Showdown {
			winners, info := st.settleShowdown()
			pot := st.pot
			events = append(events, Event{Type: PotAwarded, Winners: winners, Amount: pot})
			st.awardPot(winners)
			events = append(events, Event{Type: HandFinished, Winners: winners})
			// PotWon:赢家拿走的总额(平局时报告平均,余数忽略)
			potWon := pot
			if len(winners) > 1 {
				potWon = pot / len(winners)
			}
			return events, HandResult{Winners: winners, PotWon: potWon, Folded: false, Showdown: &info}
		}
	}
}
