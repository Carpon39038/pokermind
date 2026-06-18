package engine

import "math/rand"

// Player 是引擎认识的玩家。实现者可以是 RuleBot、LLMPlayer 等。
// 引擎只调用 Decide,不关心玩家是谁、怎么想。
type Player interface {
	Decide(obs Observation) Action
}

// playerFunc 把闭包适配为 Player。
type playerFunc func(Observation) Action

func (f playerFunc) Decide(obs Observation) Action { return f(obs) }

// PlayerFromFunc 用闭包构造一个 Player。
func PlayerFromFunc(f func(Observation) Action) Player { return playerFunc(f) }

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
// SelfReport 由 LLMPlayer 填写(内心戏),RuleBot 等规则玩家留 nil。
type Action struct {
	Type       ActionType
	Amount     int
	SelfReport *SelfReport
}

// SelfReport 是模型的内心戏(本项目的灵魂)。
// PLAN §4:模型自评的手牌强度、自报的胜率、诈唬意图、推理过程。
type SelfReport struct {
	HandStrength    float64 // 0-1
	EstimatedEquity float64 // 0-1
	IsBluffing      bool
	Reasoning       string
}

// Config 是一手牌的固定配置。
type Config struct {
	SmallBlind    int
	BigBlind      int
	StartingStack int
}

// PlayerSeat 是一个座位。Player 是外部注入的决策接口。
type PlayerSeat struct {
	ID     int
	Stack  int
	Player Player
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
	OpponentBet int  // 对手本街已投入(Heads-up 语义;多人时仅参考)
	IsButton    bool // 是否为按钮位
	NumPlayers  int  // 桌上总人数(含已弃牌)
	Position    string // 座位位置标签:"BTN"/"SB"/"BB"/"UTG"/"UTG+1"/...
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
	Winners     []int         // 赢家 seat 索引(平局时多个)
	PotWon      int           // 赢家总入账(平局时为人均,余数归首位已在 FinalStacks 体现)
	Folded      bool          // 是否因弃牌结束
	Showdown    *ShowdownInfo // 摊牌时非 nil
	FinalStacks []int         // 结算后各 seat 筹码(便于校验守恒),长度 = seat 数
}

// ShowdownInfo 是摊牌细节。
type ShowdownInfo struct {
	Best5 [][]Card   // 每个 seat 的最佳 5 张
	Ranks []HandRank // 每个 seat 的 HandRank
}

// handState 是一手牌的内部可变状态。
type handState struct {
	cfg       Config
	seats     []PlayerSeat
	stacks    []int   // 实时筹码(扣除已投入)
	bets      []int   // 本街已投入
	hole      [][]Card
	community []Card
	pot       int
	street    Street
	button    int   // 按钮位 seat 索引
	sb        int   // SB 的 seat 索引
	bb        int   // BB 的 seat 索引
	folded    []bool
	allIn     []bool
	handID    int
	deck      *Deck
}

// setupHand 初始化一手牌:扣盲注、发底牌,返回内部状态与初始事件。
//   seats   长度 2-6
//   button  按钮位 seat 索引
//
// 盲注位:
//   N=2(Heads-up):sb = button,bb = 1-button(保留 Heads-up 特例)
//   N>=3:sb = (button+1)%N,bb = (button+2)%N(标准多人桌规则)
func setupHand(seats []PlayerSeat, button int, cfg Config, rng *rand.Rand, handID int) (*handState, []Event) {
	n := len(seats)
	if n < 2 || n > 6 {
		panic("setupHand: seats length must be in [2,6]")
	}
	if button < 0 || button >= n {
		panic("setupHand: button out of range")
	}
	if cfg.SmallBlind <= 0 || cfg.BigBlind <= cfg.SmallBlind {
		panic("setupHand: invalid blinds")
	}
	if cfg.StartingStack < cfg.BigBlind {
		panic("setupHand: starting stack smaller than big blind")
	}

	var sb, bb int
	if n == 2 {
		sb = button
		bb = 1 - button
	} else {
		sb = (button + 1) % n
		bb = (button + 2) % n
	}

	st := &handState{
		cfg:    cfg,
		button: button,
		sb:     sb,
		bb:     bb,
		handID: handID,
		street: Preflop,
		deck:   NewDeck(WithRand(rng)),
		seats:  append([]PlayerSeat(nil), seats...),
		stacks: make([]int, n),
		bets:   make([]int, n),
		hole:   make([][]Card, n),
		folded: make([]bool, n),
		allIn:  make([]bool, n),
	}
	st.deck.Shuffle() // 构造的 deck 是顺序的,必须洗牌否则发牌固定
	for i, s := range seats {
		st.stacks[i] = s.Stack
	}

	var events []Event

	sbAmt := postBlind(st, sb, cfg.SmallBlind)
	events = append(events, Event{Type: BlindPosted, Seat: sb, Amount: sbAmt, Message: "small blind"})
	bbAmt := postBlind(st, bb, cfg.BigBlind)
	events = append(events, Event{Type: BlindPosted, Seat: bb, Amount: bbAmt, Message: "big blind"})

	for i := 0; i < n; i++ {
		st.hole[i] = drawN(st.deck, 2)
		events = append(events, Event{Type: DealtHole, Seat: i, Cards: st.hole[i]})
	}

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

// currentMaxBet 返回本街所有玩家中的最高下注。
func (st *handState) currentMaxBet() int {
	max := 0
	for _, b := range st.bets {
		if b > max {
			max = b
		}
	}
	return max
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

// positionLabel 返回某 seat 的位置标签(用于 Observation.Position)。
func (st *handState) positionLabel(seat int) string {
	switch seat {
	case st.button:
		return "BTN"
	case st.sb:
		return "SB"
	case st.bb:
		return "BB"
	default:
		// N>3 时其他位按距 BB 的偏移命名
		return "UTG"
	}
}

// buildObservation 构造给某 seat 的可见信息。
// OpponentBet 在 Heads-up 语义下指对手;多人时仅填第一个未弃牌非自己 seat,
// 多人语义在后续 task 完善(LLM prompt 仅作参考用)。
func (st *handState) buildObservation(seat int) Observation {
	oppBet := 0
	for i, b := range st.bets {
		if i != seat && b > oppBet {
			oppBet = b
		}
	}
	potTotal := st.pot
	for _, b := range st.bets {
		potTotal += b
	}
	return Observation{
		HandID:      st.handID,
		Street:      st.street,
		HoleCards:   st.hole[seat],
		Community:   st.community,
		Pot:         potTotal,
		ToCall:      st.toCallFor(seat),
		MinRaise:    st.minRaiseTo(),
		MyStack:     st.stacks[seat],
		MyBet:       st.bets[seat],
		OpponentBet: oppBet,
		IsButton:    seat == st.button,
		NumPlayers:  len(st.seats),
		Position:    st.positionLabel(seat),
	}
}

// firstActor 返回本街的第一个行动者。
//   N=2(Heads-up):preflop SB(=button)先,postflop BB 先(原 Heads-up 规则)
//   N>=3:preflop bb+1 先,postflop button+1 先(顺时针,跳过弃牌)
//   后续 task 完善多人的"跳过弃牌"细节;此处只给起点。
func (st *handState) firstActor() int {
	n := len(st.seats)
	if n == 2 {
		if st.street == Preflop {
			return st.sb
		}
		return st.bb
	}
	if st.street == Preflop {
		return (st.bb + 1) % n
	}
	return (st.button + 1) % n
}

// betsAllEqual 返回所有未弃牌玩家的本街下注是否相等。
func (st *handState) betsAllEqual() bool {
	ref := -1
	for i, b := range st.bets {
		if st.folded[i] || st.allIn[i] {
			continue
		}
		if ref == -1 {
			ref = b
		} else if b != ref {
			return false
		}
	}
	return true
}

// runStreet 跑完一个 street 的下注轮。当前实现保留 Heads-up 双人循环语义
// (N=2 正确);N>3 的多人行动顺序、acted 清空等逻辑留待后续 task。
func (st *handState) runStreet(events []Event) []Event {
	n := len(st.seats)
	if n != 2 {
		// 多人版 runStreet 在后续 task 实现;此处不可达,但保留 panic 防御
		panic("runStreet: multi-player not yet implemented (use N=2)")
	}

	// 双方 all-in,跳过(由调用方处理剩余街)
	if st.allIn[0] && st.allIn[1] {
		return events
	}

	actor := st.firstActor()
	acted := [2]bool{false, false}

	for {
		if st.folded[0] || st.folded[1] {
			break
		}
		if st.allIn[0] && st.allIn[1] {
			break
		}
		if st.allIn[actor] {
			acted[actor] = true
			if acted[0] && acted[1] && st.betsAllEqual() {
				break
			}
			actor = 1 - actor
			continue
		}

		obs := st.buildObservation(actor)
		action := st.seats[actor].Player.Decide(obs)
		_, ev := st.applyAction(actor, action)
		events = append(events, ev)
		acted[actor] = true

		if action.Type == Fold {
			break
		}
		if acted[0] && acted[1] && st.betsAllEqual() {
			break
		}
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
	for i := range st.bets {
		st.pot += st.bets[i]
		st.bets[i] = 0
	}

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
// 仅在 Heads-up(N=2)下使用;多人版在后续 task 改造(需走 sidepot)。
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

// potTotal 返回当前 pot + 所有未结算的本街 bets。
func (st *handState) potTotal() int {
	t := st.pot
	for _, b := range st.bets {
		t += b
	}
	return t
}

// clearBets 把本街 bets 全部并入 pot 并清零(用于 fold/结算路径)。
func (st *handState) absorbBets() {
	for i := range st.bets {
		st.pot += st.bets[i]
		st.bets[i] = 0
	}
}

// anyAllIn 返回是否有任一玩家 all-in。
func (st *handState) anyAllIn() bool {
	for _, a := range st.allIn {
		if a {
			return true
		}
	}
	return false
}

// PlayHand 完整跑完一手牌,返回事件流与结算结果。
//
// 当前实现支持 Heads-up(N=2,完整正确);N>=3 的多人逻辑(runStreet 行动顺序、
// sidepot 结算)在后续 task 落地。N!=2 时 panic。
//
// button 是按钮位 seat 索引。
func PlayHand(seats []PlayerSeat, button int, cfg Config, rng *rand.Rand, handID int) ([]Event, HandResult) {
	if len(seats) != 2 {
		panic("PlayHand: multi-player not yet implemented (use N=2)")
	}
	st, events := setupHand(seats, button, cfg, rng, handID)

	finishByFold := func(winner int) ([]Event, HandResult) {
		pot := st.potTotal()
		events = append(events, Event{Type: PotAwarded, Winners: []int{winner}, Amount: pot})
		st.absorbBets()
		st.awardPot([]int{winner})
		events = append(events, Event{Type: HandFinished, Folded: true, Winners: []int{winner}})
		return events, HandResult{Winners: []int{winner}, PotWon: pot, Folded: true, FinalStacks: append([]int(nil), st.stacks...)}
	}

	for {
		// 一方已弃牌(理论上 setupHand 后不会立即 fold,但 runStreet 后可能)
		if st.folded[0] || st.folded[1] {
			winner := 0
			if st.folded[0] {
				winner = 1
			}
			return finishByFold(winner)
		}

		events = st.runStreet(events)

		// runStreet 后再检查 fold
		if st.folded[0] || st.folded[1] {
			winner := 0
			if st.folded[0] {
				winner = 1
			}
			return finishByFold(winner)
		}

		// 若任一方 all-in,跳过决策,翻完剩余 street
		if st.anyAllIn() {
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
			potWon := pot
			if len(winners) > 1 {
				potWon = pot / len(winners)
			}
			return events, HandResult{Winners: winners, PotWon: potWon, Folded: false, Showdown: &info, FinalStacks: append([]int(nil), st.stacks...)}
		}
	}
}
