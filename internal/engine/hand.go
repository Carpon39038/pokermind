package engine

import (
	"math/rand"

	"pokermind/internal/sidepot"
)

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
	cfg        Config
	seats      []PlayerSeat
	stacks     []int // 实时筹码(扣除已投入)
	origStacks []int // 起始筹码副本;本手总投入 = origStacks[i] - stacks[i](sidepot 用)
	bets       []int // 本街已投入
	hole       [][]Card
	community  []Card
	pot        int
	street     Street
	button     int // 按钮位 seat 索引
	sb         int // SB 的 seat 索引
	bb         int // BB 的 seat 索引
	folded     []bool
	allIn      []bool
	handID     int
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
		stacks:     make([]int, n),
		origStacks: make([]int, n),
		bets:       make([]int, n),
		hole:   make([][]Card, n),
		folded: make([]bool, n),
		allIn:  make([]bool, n),
	}
	st.deck.Shuffle() // 构造的 deck 是顺序的,必须洗牌否则发牌固定
	for i, s := range seats {
		st.stacks[i] = s.Stack
		st.origStacks[i] = s.Stack
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

// firstActor 返回本街的第一个行动者(跳过已弃牌/all-in)。
//   N=2(Heads-up):preflop SB(=button)先,postflop BB 先(原 Heads-up 规则)
//   N>=3:preflop bb+1 先,postflop button+1 先(顺时针)
// 若所有候选都已 fold/all-in,返回 -1(本街无行动)。
func (st *handState) firstActor() int {
	n := len(st.seats)
	var start int
	if n == 2 {
		if st.street == Preflop {
			start = st.sb
		} else {
			start = st.bb
		}
	} else if st.street == Preflop {
		start = (st.bb + 1) % n
	} else {
		start = (st.button + 1) % n
	}
	// 顺时针找第一个能行动的(未弃牌、未 all-in)
	for i := 0; i < n; i++ {
		seat := (start + i) % n
		if !st.folded[seat] && !st.allIn[seat] {
			return seat
		}
	}
	return -1
}

// nextActiveActor 从 from 的下一个 seat 起,顺时针找下一个能行动的玩家。
// 没有则返回 -1。
func (st *handState) nextActiveActor(from int) int {
	n := len(st.seats)
	for i := 1; i <= n; i++ {
		seat := (from + i) % n
		if !st.folded[seat] && !st.allIn[seat] {
			return seat
		}
	}
	return -1
}

// contendingCount 返回未弃牌的玩家数。
func (st *handState) contendingCount() int {
	c := 0
	for _, f := range st.folded {
		if !f {
			c++
		}
	}
	return c
}

// betsAllEqual 返回所有未弃牌玩家的本街下注是否相等
// (all-in 玩家视为"已满足",因为不能再补)。
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

// runStreet 跑完一个 street 的下注轮,N=2..6 通用。
//
// 算法:
//  1. 从 firstActor 起,顺时针轮流询问
//  2. acted[] 标记本轮每个未弃牌未 all-in 玩家是否行动过
//  3. 加注会重置其他玩家的 acted(他们要再决定 call/raise/fold)
//  4. 终止条件(任一):
//     - 只剩 1 个未弃牌玩家
//     - 所有未弃牌未 all-in 玩家都行动过 且 betsAllEqual()
func (st *handState) runStreet(events []Event) []Event {
	n := len(st.seats)

	// 终止:只剩 1 个未弃牌
	if st.contendingCount() <= 1 {
		return events
	}

	// 终止:所有未弃牌都已 all-in(无需行动)
	if !st.hasActivePlayer() {
		return events
	}

	acted := make([]bool, n)
	actor := st.firstActor()
	if actor < 0 {
		return events
	}

	// 终止检查:所有能行动的都 acted 且下注相等
	streetDone := func() bool {
		if st.contendingCount() <= 1 {
			return true
		}
		// 至少有一个能行动的玩家(否则上面已 return)
		for i := 0; i < n; i++ {
			if st.folded[i] || st.allIn[i] {
				continue
			}
			if !acted[i] {
				return false
			}
		}
		return st.betsAllEqual()
	}

	for {
		if streetDone() {
			break
		}
		// 跳过不能行动的(理论上 nextActiveActor 已经保证,但 acted 循环里
		// 某玩家可能正好 all-in 后被轮到 —— 直接当已行动)
		if st.folded[actor] || st.allIn[actor] {
			acted[actor] = true
			actor = st.nextActiveActor(actor)
			if actor < 0 {
				break
			}
			continue
		}

		obs := st.buildObservation(actor)
		action := st.seats[actor].Player.Decide(obs)
		_, ev := st.applyAction(actor, action)
		events = append(events, ev)
		acted[actor] = true

		if action.Type == Fold {
			// 弃牌后若只剩 1 个未弃牌,PlayHand 会处理;本街立刻结束
			break
		}
		if action.Type == Raise {
			// 加注重置其他人的 acted(他们要再决定);自己保持 acted=true
			for i := 0; i < n; i++ {
				if i != actor && !st.folded[i] && !st.allIn[i] {
					acted[i] = false
				}
			}
		}

		if streetDone() {
			break
		}
		actor = st.nextActiveActor(actor)
		if actor < 0 {
			break
		}
	}
	return events
}

// hasActivePlayer 返回是否还有未弃牌且未 all-in 的玩家(可继续行动)。
func (st *handState) hasActivePlayer() bool {
	for i := 0; i < len(st.seats); i++ {
		if !st.folded[i] && !st.allIn[i] {
			return true
		}
	}
	return false
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

// contributions 返回每个 seat 本手总投入(用于 sidepot 分层)。
// 投入 = 起始 stack - 当前 stack(钱从 stack 流到 pot)。
func (st *handState) contributions() []int {
	c := make([]int, len(st.seats))
	for i := range st.seats {
		c[i] = st.origStacks[i] - st.stacks[i]
		if c[i] < 0 {
			c[i] = 0
		}
	}
	return c
}

// settleShowdown 用 Evaluate/Best5 给每个未弃牌玩家评级,
// 然后用 sidepot.Compute + Determine 按层级分发奖金。
// 返回:
//   winners  去重后的总赢家 seat 列表(用于事件流显示)
//   info     ShowdownInfo(所有未弃牌玩家的 Best5/Ranks)
//   payouts  每 seat 赢得的总额(用于 awardFromPayouts)
func (st *handState) settleShowdown() (winners []int, info ShowdownInfo, payouts []int) {
	n := len(st.seats)
	info.Best5 = make([][]Card, n)
	info.Ranks = make([]HandRank, n)

	// 先把所有未弃牌玩家的牌力算出(含已弃牌的填零值,只为索引对齐)
	contendingFlags := make([]bool, n)
	for i := 0; i < n; i++ {
		if st.folded[i] {
			continue
		}
		all := append(append([]Card{}, st.hole[i]...), st.community...)
		info.Ranks[i] = Evaluate(all)
		info.Best5[i] = Best5(all)
		contendingFlags[i] = true
	}

	pots := sidepot.Compute(st.contributions(), contendingFlags)

	// 每层 pot 的赢家 = 该层 eligible 里 Rank 最强的(并列都算)
	winnersByPot := make([][]int, len(pots))
	winnerSet := map[int]struct{}{}
	for idx, pot := range pots {
		if len(pot.Eligible) == 0 {
			winnersByPot[idx] = nil
			continue
		}
		// 找 eligible 里最强的 Rank
		var bestRank HandRank
		first := true
		for _, s := range pot.Eligible {
			if first || info.Ranks[s].Compare(bestRank) > 0 {
				bestRank = info.Ranks[s]
				first = false
			}
		}
		// 收集所有达到该 Rank 的(并列)
		var layerWinners []int
		for _, s := range pot.Eligible {
			if info.Ranks[s].Compare(bestRank) == 0 {
				layerWinners = append(layerWinners, s)
				winnerSet[s] = struct{}{}
			}
		}
		winnersByPot[idx] = layerWinners
	}

	payouts = sidepot.Distribute(pots, winnersByPot)

	for s := range winnerSet {
		winners = append(winners, s)
	}
	// 排序保证事件流稳定(避免 map 迭代顺序)
	for i := 1; i < len(winners); i++ {
		for j := i; j > 0 && winners[j] < winners[j-1]; j-- {
			winners[j], winners[j-1] = winners[j-1], winners[j]
		}
	}
	return winners, info, payouts
}

// awardFromPayouts 按 payouts[i] 给每 seat 加奖金,清零 pot。
func (st *handState) awardFromPayouts(payouts []int) {
	for i, p := range payouts {
		if i < len(st.stacks) {
			st.stacks[i] += p
		}
	}
	st.pot = 0
}

// awardFoldPot 在只剩 1 个未弃牌玩家时按 sidepot 分发(弃牌者的贡献保留
// 但不争)。唯一未弃牌者通过"无人争"规则拿到每层 pot。
func (st *handState) awardFoldPot(winner int) []int {
	n := len(st.seats)
	contending := make([]bool, n)
	contending[winner] = true
	pots := sidepot.Compute(st.contributions(), contending)
	// 每层 winners=nil → Distribute 把金额给 eligible 顺位最先(就是 winner)
	winnersByPot := make([][]int, len(pots))
	return sidepot.Distribute(pots, winnersByPot)
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

// soleContender 返回唯一的未弃牌玩家 seat;若不止 1 个未弃牌返回 -1。
func (st *handState) soleContender() int {
	winner := -1
	for i, f := range st.folded {
		if !f {
			if winner != -1 {
				return -1
			}
			winner = i
		}
	}
	return winner
}

// PlayHand 完整跑完一手牌,返回事件流与结算结果。支持 N=2..6。
//
//   盲注、发牌、下注轮(runStreet)、弃牌结算、摊牌结算(含 sidepot)均完整。
//
// button 是按钮位 seat 索引。
func PlayHand(seats []PlayerSeat, button int, cfg Config, rng *rand.Rand, handID int) ([]Event, HandResult) {
	st, events := setupHand(seats, button, cfg, rng, handID)

	finishByFold := func(winner int) ([]Event, HandResult) {
		// 把本街 bets 并入 pot(弃牌者贡献保留但不争,由 sidepot 处理)
		st.absorbBets()
		payouts := st.awardFoldPot(winner)
		st.awardFromPayouts(payouts)
		// 事件 Amount = winner 实拿
		got := 0
		if winner < len(payouts) {
			got = payouts[winner]
		}
		events = append(events, Event{Type: PotAwarded, Winners: []int{winner}, Amount: got})
		events = append(events, Event{Type: HandFinished, Folded: true, Winners: []int{winner}})
		return events, HandResult{Winners: []int{winner}, PotWon: got, Folded: true, FinalStacks: append([]int(nil), st.stacks...)}
	}

	for {
		// 只剩 1 个未弃牌玩家(setupHand 后理论上不会,但防御)
		if w := st.soleContender(); w >= 0 {
			return finishByFold(w)
		}

		events = st.runStreet(events)

		// runStreet 后只剩 1 个未弃牌 → fold 结算
		if w := st.soleContender(); w >= 0 {
			return finishByFold(w)
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
			winners, info, payouts := st.settleShowdown()
			st.awardFromPayouts(payouts)
			// 事件 Amount = 总奖池(所有 payouts 之和)
			totalAward := 0
			for _, p := range payouts {
				totalAward += p
			}
			events = append(events, Event{Type: PotAwarded, Winners: winners, Amount: totalAward})
			events = append(events, Event{Type: HandFinished, Winners: winners})
			// PotWon:报告赢家中最多的(主要赢家)
			potWon := 0
			for _, w := range winners {
				if w < len(payouts) && payouts[w] > potWon {
					potWon = payouts[w]
				}
			}
			return events, HandResult{Winners: winners, PotWon: potWon, Folded: false, Showdown: &info, FinalStacks: append([]int(nil), st.stacks...)}
		}
	}
}
