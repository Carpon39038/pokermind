// Package match 编排一局 Heads-up 多手对局,落库 + 更新 ELO。
package match

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"pokermind/internal/elo"
	"pokermind/internal/engine"
	"pokermind/internal/store"
)

// PlayerSpec 描述一个参赛模型的身份(用于落库标识)。
type PlayerSpec struct {
	Provider string
	Model    string
	Label    string
}

// Result 是一局的产出。
type Result struct {
	HandsPlayed int
	Winner      int // 0=p1, 1=p2, -1=平局
	FinalStacks []int
	GameID      int64
	EloChange   []float64 // 索引对应 seat (N=2 时长度 2)
}

// Play 跑一局。p1 是 seat0(makeP1() 每次 PlayHand 调用前构造一个新 Player
// 实例,这样 LLMPlayer 等带状态的实现不会跨手污染)。
// rec 可以为 nil(纯内存跑,不落库),便于无 DB 测试。
func Play(p1, p2 PlayerSpec, makeP1, makeP2 func() engine.Player, hands int, cfg engine.Config, rec *store.Store, rngSeed int64) (*Result, error) {
	if hands <= 0 {
		return nil, fmt.Errorf("match: hands must be > 0")
	}
	if cfg.BigBlind <= 0 || cfg.SmallBlind <= 0 {
		return nil, fmt.Errorf("match: invalid blinds")
	}

	var p1ID, p2ID int64
	if rec != nil {
		var err error
		p1ID, err = rec.RegisterPlayer(p1.Provider, p1.Model, p1.Label)
		if err != nil {
			return nil, fmt.Errorf("register p1: %w", err)
		}
		p2ID, err = rec.RegisterPlayer(p2.Provider, p2.Model, p2.Label)
		if err != nil {
			return nil, fmt.Errorf("register p2: %w", err)
		}
	}

	playerIDs := []int64{p1ID, p2ID}
	stacks := []int{cfg.StartingStack, cfg.StartingStack}
	rng := rand.New(rand.NewSource(rngSeed))
	startedAt := time.Now()

	gameRecord := store.GameRecord{
		NumSeats:   2,
		Seats:      []store.GameSeat{}, // 填在下面
		StartedAt:  startedAt,
		ConfigJSON: configJSON(cfg, hands),
	}

	handsPlayed := 0
	for h := 1; h <= hands; h++ {
		// 破产检查:任一方筹码不够 BB 就提前结束
		if stacks[0] < cfg.BigBlind || stacks[1] < cfg.BigBlind {
			break
		}

		button := (h - 1) % 2 // h=1: button=0(p1 当 SB);h=2: button=1(p2 当 SB)
		seats := []engine.PlayerSeat{
			{ID: 0, Stack: stacks[0], Player: makeP1()},
			{ID: 1, Stack: stacks[1], Player: makeP2()},
		}
		events, result := engine.PlayHand(seats, button, cfg, rng, h)

		// 更新累积 stack(engine 的 FinalStacks 已经把 pot 结算到赢家)
		stacks = result.FinalStacks

		// 翻译成 HandRecord
		hr := translateHand(h, button, events, result, playerIDs)
		gameRecord.Hands = append(gameRecord.Hands, hr)
		handsPlayed++
	}

	// 定胜负:筹码多的赢
	winner := -1
	if stacks[0] > stacks[1] {
		winner = 0
	} else if stacks[1] > stacks[0] {
		winner = 1
	}

	gameRecord.HandsPlayed = handsPlayed
	gameRecord.Seats = []store.GameSeat{
		{PlayerID: p1ID, FinalChips: stacks[0], IsWinner: winner == 0},
		{PlayerID: p2ID, FinalChips: stacks[1], IsWinner: winner == 1},
	}
	gameRecord.IsDraw = (winner == -1)
	gameRecord.FinishedAt = time.Now()

	out := &Result{
		HandsPlayed: handsPlayed,
		Winner:      winner,
		FinalStacks: stacks,
		EloChange:   make([]float64, 2),
	}

	// 落库 + 更新 ELO
	if rec != nil {
		gameID, err := rec.RecordGame(gameRecord)
		if err != nil {
			return nil, fmt.Errorf("record game: %w", err)
		}
		out.GameID = gameID

		elo1, _ := rec.GetElo(p1ID)
		elo2, _ := rec.GetElo(p2ID)
		var score elo.Score
		switch winner {
		case 0:
			score = elo.Win
		case 1:
			score = elo.Loss
		default:
			score = elo.Draw
		}
		new1, new2 := elo.Update(float64(elo1), float64(elo2), score, 0)
		_ = rec.SetElo(p1ID, int(new1))
		_ = rec.SetElo(p2ID, int(new2))
		out.EloChange[0] = new1 - float64(elo1)
		out.EloChange[1] = new2 - float64(elo2)
	}

	return out, nil
}

// translateHand 把一手的 events + result 翻译成 store.HandRecord。
// playerIDs: seat 索引到 player ID 的映射(长度 2-6)。
func translateHand(handIndex, button int, events []engine.Event, result engine.HandResult, playerIDs []int64) store.HandRecord {
	hr := store.HandRecord{
		HandIndex:   handIndex,
		ButtonSeat:  button,
		Folded:      result.Folded,
		Pot:         result.PotWon,
		PlayerHoles: make([]string, len(playerIDs)), // 默认空字符串
	}

	// 摊牌时填赢家 ID;fold 时赢家也填(fold 后唯一的赢家)
	// engine 的 Winners 是 seat 索引。单赢家或平局。
	if len(result.Winners) == 1 {
		seat := result.Winners[0]
		if seat >= 0 && seat < len(playerIDs) {
			hr.WinnerPID = playerIDs[seat]
		}
	} else if len(result.Winners) > 1 {
		hr.IsDraw = true
	}

	// 收集底牌 / 公共牌 / 动作
	var community []engine.Card
	seq := 0
	for _, ev := range events {
		switch ev.Type {
		case engine.DealtHole:
			if ev.Seat >= 0 && ev.Seat < len(playerIDs) {
				hr.PlayerHoles[ev.Seat] = cardsToStr(ev.Cards)
			}
		case engine.StreetAdvanced:
			community = append(community, ev.Cards...)
		case engine.ActionTaken:
			if ev.Action == nil {
				continue
			}
			ar := store.ActionRecord{
				Seq:        seq,
				Street:     ev.Street.String(),
				Seat:       ev.Seat,
				PlayerID:   seatToPlayerID(ev.Seat, playerIDs),
				ActionType: ev.Action.Type.String(),
				Amount:     ev.Action.Amount,
				// PotBefore / ToCall 暂留 0 —— engine.Event 未暴露,待后续在事件里补
			}
			if ev.Action.SelfReport != nil {
				ar.HasSelfReport = true
				ar.Reasoning = ev.Action.SelfReport.Reasoning
				ar.HandStrength = ev.Action.SelfReport.HandStrength
				ar.EstEquity = ev.Action.SelfReport.EstimatedEquity
				ar.IsBluffing = ev.Action.SelfReport.IsBluffing
			}
			hr.Actions = append(hr.Actions, ar)
			seq++
		}
	}

	hr.Community = cardsToStr(community)
	return hr
}

func seatToPlayerID(seat int, playerIDs []int64) int64 {
	if seat >= 0 && seat < len(playerIDs) {
		return playerIDs[seat]
	}
	return 0 // 不应到达
}

func cardsToStr(cs []engine.Card) string {
	if len(cs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		parts = append(parts, c.String())
	}
	return strings.Join(parts, " ")
}

func configJSON(cfg engine.Config, hands int) string {
	b, _ := json.Marshal(map[string]any{
		"sb":             cfg.SmallBlind,
		"bb":             cfg.BigBlind,
		"starting_stack": cfg.StartingStack,
		"hands":          hands,
	})
	return string(b)
}

// ResultN 是 N 人内存对局(PlayN)的产出。
// 若 rec != nil 落库,则包含 GameID/EloChange/PlayerIDs。
type ResultN struct {
	HandsPlayed int
	WinnerSeat  int      // 最终筹码最高的 seat(平局给顺位最先);-1 表示全部破产
	FinalStacks []int    // 每 seat 结算后筹码
	GameID      int64    // 仅当 rec != nil 时有值
	EloChange   []float64 // 仅当 rec != nil 时有值,索引对应 seat
	PlayerIDs   []int64   // 仅当 rec != nil 时有值,索引对应 seat
}

// PlayN 跑一局 N 人(2-6)内存对局。
//
//   specs       N 个 PlayerSpec
//   makePlayers N 个 Player 工厂,每次 PlayHand 调用前重新构造(避免跨手状态)
//   hands       计划手数;任一 seat 筹码 < BB 时提前结束
//   rec         可选存储层;为 nil 时不落库,非 nil 时落库并更新 ELO
//
// 返回每 seat 最终筹码。若 rec != nil,则包含 GameID/EloChange/PlayerIDs。
func PlayN(specs []PlayerSpec, makePlayers []func() engine.Player, hands int, cfg engine.Config, rngSeed int64, rec *store.Store) (*ResultN, error) {
	n := len(specs)
	if n < 2 || n > 6 {
		return nil, fmt.Errorf("PlayN: need 2-6 specs, got %d", n)
	}
	if len(makePlayers) != n {
		return nil, fmt.Errorf("PlayN: makePlayers length %d != specs length %d", len(makePlayers), n)
	}
	if hands <= 0 {
		return nil, fmt.Errorf("PlayN: hands must be > 0")
	}
	if cfg.BigBlind <= 0 || cfg.SmallBlind <= 0 {
		return nil, fmt.Errorf("PlayN: invalid blinds")
	}

	// 注册玩家(若需要落库)
	var playerIDs []int64
	if rec != nil {
		playerIDs = make([]int64, n)
		for i, spec := range specs {
			id, err := rec.RegisterPlayer(spec.Provider, spec.Model, spec.Label)
			if err != nil {
				return nil, fmt.Errorf("register player %d: %w", i, err)
			}
			playerIDs[i] = id
		}
	} else {
		// 占位,避免 translateHand 索引越界
		playerIDs = make([]int64, n)
	}

	stacks := make([]int, n)
	for i := range stacks {
		stacks[i] = cfg.StartingStack
	}
	rng := rand.New(rand.NewSource(rngSeed))
	startedAt := time.Now()

	gameRecord := store.GameRecord{
		NumSeats:   n,
		Seats:      make([]store.GameSeat, n),
		StartedAt:  startedAt,
		ConfigJSON: configJSON(cfg, hands),
	}

	handsPlayed := 0
	for h := 1; h <= hands; h++ {
		// 破产检查:任一 seat 筹码 < BB 提前结束
		bust := false
		for _, s := range stacks {
			if s < cfg.BigBlind {
				bust = true
				break
			}
		}
		if bust {
			break
		}

		button := (h - 1) % n
		seats := make([]engine.PlayerSeat, n)
		for i := 0; i < n; i++ {
			seats[i] = engine.PlayerSeat{
				ID:     i,
				Stack:  stacks[i],
				Player: makePlayers[i](),
			}
		}
		events, result := engine.PlayHand(seats, button, cfg, rng, h)
		stacks = result.FinalStacks

		// 翻译成 HandRecord(若需要落库)
		if rec != nil {
			hr := translateHand(h, button, events, result, playerIDs)
			gameRecord.Hands = append(gameRecord.Hands, hr)
		}
		handsPlayed++
	}

	// 定最终赢家:筹码最高(并列给顺位最先)
	winnerSeat := -1
	best := -1
	for i, s := range stacks {
		if s > best {
			best = s
			winnerSeat = i
		}
	}

	out := &ResultN{
		HandsPlayed: handsPlayed,
		WinnerSeat:  winnerSeat,
		FinalStacks: stacks,
	}

	// 落库 + 更新 ELO
	if rec != nil {
		// 填充 gameRecord.Seats
		isDraw := (winnerSeat == -1)
		for i, finalChips := range stacks {
			gameRecord.Seats[i] = store.GameSeat{
				PlayerID:   playerIDs[i],
				FinalChips: finalChips,
				IsWinner:   !isDraw && i == winnerSeat,
			}
		}
		gameRecord.HandsPlayed = handsPlayed
		gameRecord.IsDraw = isDraw
		gameRecord.FinishedAt = time.Now()

		gameID, err := rec.RecordGame(gameRecord)
		if err != nil {
			return nil, fmt.Errorf("record game: %w", err)
		}
		out.GameID = gameID
		out.PlayerIDs = playerIDs
		out.EloChange = make([]float64, n)

		// 计算多人 ELO
		if !isDraw && winnerSeat >= 0 {
			// 获取所有玩家当前 ELO
			elos := make([]float64, n)
			for i, pid := range playerIDs {
				elo, _ := rec.GetElo(pid)
				elos[i] = float64(elo)
			}

			// 用 elo.UpdateMulti:赢家 vs 所有输家
			winnerRating := elos[winnerSeat]
			var loserRatings []float64
			for i, elo := range elos {
				if i != winnerSeat {
					loserRatings = append(loserRatings, elo)
				}
			}
			newWinner, newLosers := elo.UpdateMulti(winnerRating, loserRatings, 0)

			// 写回数据库
			loserIdx := 0
			for i, pid := range playerIDs {
				if i == winnerSeat {
					_ = rec.SetElo(pid, int(newWinner))
					out.EloChange[i] = newWinner - elos[i]
				} else {
					_ = rec.SetElo(pid, int(newLosers[loserIdx]))
					out.EloChange[i] = newLosers[loserIdx] - elos[i]
					loserIdx++
				}
			}
		}
		// 平局时不更新 ELO(out.EloChange 保持全 0)
	}

	return out, nil
}

// LiveEvent 是 RunLive 在对局过程中向外推送的事件。
type LiveEvent struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// LiveEvent 类型常量。
const (
	EvMatchStarted  = "match_started"
	EvHandStarted   = "hand_started"
	EvAction        = "action"
	EvHandFinished  = "hand_finished"
	EvMatchFinished = "match_finished"
	EvError         = "error"
)

type liveSeatInfo struct {
	Seat     int    `json:"seat"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Label    string `json:"label"`
}

type matchStartedPayload struct {
	Seats         []liveSeatInfo `json:"seats"`
	Hands         int            `json:"hands"`
	SmallBlind    int            `json:"sb"`
	BigBlind      int            `json:"bb"`
	StartingStack int            `json:"starting_stack"`
}

type liveHole struct {
	Seat  int      `json:"seat"`
	Cards []string `json:"cards"`
}

type handStartedPayload struct {
	Hand   int        `json:"hand"`
	Button int        `json:"button"`
	Holes  []liveHole `json:"holes"`
}

type actionPayload struct {
	Hand       int     `json:"hand"`
	Seq        int     `json:"seq"`
	Street     string  `json:"street"`
	Seat       int     `json:"seat"`
	Provider   string  `json:"provider"`
	Model      string  `json:"model"`
	ActionType string  `json:"action_type"`
	Amount     int     `json:"amount"`
	HasReport  bool    `json:"has_report,omitempty"`
	Reasoning  string  `json:"reasoning,omitempty"`
	HandStr    float64 `json:"hand_strength,omitempty"`
	EstEquity  float64 `json:"estimated_equity,omitempty"`
	IsBluffing bool    `json:"is_bluffing,omitempty"`
}

type handFinishedPayload struct {
	Hand      int      `json:"hand"`
	Winners   []int    `json:"winners"`
	Community []string `json:"community"`
	Pot       int      `json:"pot"`
	Folded    bool     `json:"folded"`
}

type matchFinishedPayload struct {
	WinnerSeat  int       `json:"winner_seat"`
	FinalStacks []int     `json:"final_stacks"`
	EloChange   []float64 `json:"elo_change,omitempty"`
	GameID      int64     `json:"game_id,omitempty"`
}

// RunLive 与 PlayN 平级,差别:
//  1. 每个事件 hook 把 LiveEvent 发到 out(非阻塞,满则丢)。
//  2. ctx 用于取消(用户点「停止」),取消时立刻 return error,不落库。
//  3. 落库 + ELO 逻辑同 PlayN。
func RunLive(
	ctx context.Context,
	specs []PlayerSpec,
	makePlayers []func() engine.Player,
	hands int,
	cfg engine.Config,
	rngSeed int64,
	rec *store.Store,
	out chan<- LiveEvent,
) (*ResultN, error) {
	n := len(specs)
	if n < 2 || n > 6 {
		return nil, fmt.Errorf("RunLive: need 2-6 specs, got %d", n)
	}
	if len(makePlayers) != n {
		return nil, fmt.Errorf("RunLive: makePlayers length %d != specs length %d", len(makePlayers), n)
	}
	if hands <= 0 {
		return nil, fmt.Errorf("RunLive: hands must be > 0")
	}
	if cfg.BigBlind <= 0 || cfg.SmallBlind <= 0 {
		return nil, fmt.Errorf("RunLive: invalid blinds")
	}

	// 注册玩家
	var playerIDs []int64
	if rec != nil {
		playerIDs = make([]int64, n)
		for i, spec := range specs {
			id, err := rec.RegisterPlayer(spec.Provider, spec.Model, spec.Label)
			if err != nil {
				return nil, fmt.Errorf("register player %d: %w", i, err)
			}
			playerIDs[i] = id
		}
	} else {
		playerIDs = make([]int64, n)
	}

	// match_started
	msPayload := matchStartedPayload{
		Hands:         hands,
		SmallBlind:    cfg.SmallBlind,
		BigBlind:      cfg.BigBlind,
		StartingStack: cfg.StartingStack,
	}
	for i, spec := range specs {
		msPayload.Seats = append(msPayload.Seats, liveSeatInfo{
			Seat: i, Provider: spec.Provider, Model: spec.Model, Label: spec.Label,
		})
	}
	emitLive(out, LiveEvent{Type: EvMatchStarted, Payload: mustPayload(msPayload)})

	stacks := make([]int, n)
	for i := range stacks {
		stacks[i] = cfg.StartingStack
	}
	rng := rand.New(rand.NewSource(rngSeed))
	startedAt := time.Now()

	gameRecord := store.GameRecord{
		NumSeats:   n,
		Seats:      make([]store.GameSeat, n),
		StartedAt:  startedAt,
		ConfigJSON: configJSON(cfg, hands),
	}

	handsPlayed := 0
	for h := 1; h <= hands; h++ {
		if err := ctx.Err(); err != nil {
			emitLive(out, LiveEvent{Type: EvError, Payload: mustPayload(map[string]string{"error": err.Error()})})
			return nil, err
		}

		bust := false
		for _, s := range stacks {
			if s < cfg.BigBlind {
				bust = true
				break
			}
		}
		if bust {
			break
		}

		button := (h - 1) % n
		seats := make([]engine.PlayerSeat, n)
		for i := 0; i < n; i++ {
			seats[i] = engine.PlayerSeat{
				ID:     i,
				Stack:  stacks[i],
				Player: makePlayers[i](),
			}
		}
		events, result := engine.PlayHand(seats, button, cfg, rng, h)
		stacks = result.FinalStacks

		emitHandEvents(out, events, h, button, specs, result)

		if rec != nil {
			hr := translateHand(h, button, events, result, playerIDs)
			gameRecord.Hands = append(gameRecord.Hands, hr)
		}
		handsPlayed++
	}

	if err := ctx.Err(); err != nil {
		emitLive(out, LiveEvent{Type: EvError, Payload: mustPayload(map[string]string{"error": err.Error()})})
		return nil, err
	}

	winnerSeat := -1
	best := -1
	for i, s := range stacks {
		if s > best {
			best = s
			winnerSeat = i
		}
	}

	out2 := &ResultN{
		HandsPlayed: handsPlayed,
		WinnerSeat:  winnerSeat,
		FinalStacks: stacks,
	}

	if rec != nil {
		isDraw := (winnerSeat == -1)
		for i, finalChips := range stacks {
			gameRecord.Seats[i] = store.GameSeat{
				PlayerID:   playerIDs[i],
				FinalChips: finalChips,
				IsWinner:   !isDraw && i == winnerSeat,
			}
		}
		gameRecord.HandsPlayed = handsPlayed
		gameRecord.IsDraw = isDraw
		gameRecord.FinishedAt = time.Now()

		gameID, err := rec.RecordGame(gameRecord)
		if err != nil {
			return nil, fmt.Errorf("record game: %w", err)
		}
		out2.GameID = gameID
		out2.PlayerIDs = playerIDs
		out2.EloChange = make([]float64, n)

		if !isDraw && winnerSeat >= 0 {
			elos := make([]float64, n)
			for i, pid := range playerIDs {
				e, _ := rec.GetElo(pid)
				elos[i] = float64(e)
			}
			winnerRating := elos[winnerSeat]
			var loserRatings []float64
			for i, e := range elos {
				if i != winnerSeat {
					loserRatings = append(loserRatings, e)
				}
			}
			newWinner, newLosers := elo.UpdateMulti(winnerRating, loserRatings, 0)
			loserIdx := 0
			for i, pid := range playerIDs {
				if i == winnerSeat {
					_ = rec.SetElo(pid, int(newWinner))
					out2.EloChange[i] = newWinner - elos[i]
				} else {
					_ = rec.SetElo(pid, int(newLosers[loserIdx]))
					out2.EloChange[i] = newLosers[loserIdx] - elos[i]
					loserIdx++
				}
			}
		}
	}

	mfPayload := matchFinishedPayload{
		WinnerSeat:  winnerSeat,
		FinalStacks: stacks,
		EloChange:   out2.EloChange,
		GameID:      out2.GameID,
	}
	emitLive(out, LiveEvent{Type: EvMatchFinished, Payload: mustPayload(mfPayload)})

	return out2, nil
}

// emitHandEvents 从 engine events 抽出 hand_started / action / hand_finished 推送。
func emitHandEvents(out chan<- LiveEvent, events []engine.Event, hand int, button int, specs []PlayerSpec, result engine.HandResult) {
	hs := handStartedPayload{Hand: hand, Button: button}
	handStartedSent := false

	sendHandStarted := func() {
		if handStartedSent {
			return
		}
		handStartedSent = true
		emitLive(out, LiveEvent{Type: EvHandStarted, Payload: mustPayload(hs)})
	}

	// 先收集本手所有公共牌,便于 hand_finished 使用
	var communityCards []string
	for _, ev := range events {
		if ev.Type == engine.StreetAdvanced {
			for _, c := range ev.Cards {
				communityCards = append(communityCards, c.String())
			}
		}
	}

	seq := 0
	for _, ev := range events {
		switch ev.Type {
		case engine.DealtHole:
			if ev.Seat >= 0 && ev.Seat < len(specs) {
				cards := make([]string, 0, len(ev.Cards))
				for _, c := range ev.Cards {
					cards = append(cards, c.String())
				}
				hs.Holes = append(hs.Holes, liveHole{Seat: ev.Seat, Cards: cards})
			}
			sendHandStarted()
		case engine.ActionTaken:
			sendHandStarted()
			if ev.Action == nil {
				continue
			}
			ap := actionPayload{
				Hand:       hand,
				Seq:        seq,
				Street:     ev.Street.String(),
				Seat:       ev.Seat,
				ActionType: ev.Action.Type.String(),
				Amount:     ev.Action.Amount,
			}
			if ev.Seat >= 0 && ev.Seat < len(specs) {
				ap.Provider = specs[ev.Seat].Provider
				ap.Model = specs[ev.Seat].Model
			}
			if ev.Action.SelfReport != nil {
				ap.HasReport = true
				ap.Reasoning = ev.Action.SelfReport.Reasoning
				ap.HandStr = ev.Action.SelfReport.HandStrength
				ap.EstEquity = ev.Action.SelfReport.EstimatedEquity
				ap.IsBluffing = ev.Action.SelfReport.IsBluffing
			}
			seq++
			emitLive(out, LiveEvent{Type: EvAction, Payload: mustPayload(ap)})
		case engine.HandFinished:
			sendHandStarted()
			winners := append([]int(nil), ev.Winners...)
			hf := handFinishedPayload{
				Hand:      hand,
				Winners:   winners,
				Community: communityCards,
				Pot:       result.PotWon,
				Folded:    ev.Folded,
			}
			emitLive(out, LiveEvent{Type: EvHandFinished, Payload: mustPayload(hf)})
		}
	}
}

// emitLive 非阻塞地把 ev 发到 out;满则丢。
func emitLive(out chan<- LiveEvent, ev LiveEvent) {
	if out == nil {
		return
	}
	select {
	case out <- ev:
	default:
	}
}

// mustPayload 把 v marshal 成 RawMessage,失败时用 error payload。
func mustPayload(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(map[string]string{"_marshal_error": err.Error()})
	}
	return b
}
