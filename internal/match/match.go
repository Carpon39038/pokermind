// Package match 编排一局 Heads-up 多手对局,落库 + 更新 ELO。
package match

import (
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
	FinalStacks [2]int
	GameID      int64
	EloChange   [2]float64 // [p1 delta, p2 delta]
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

	stacks := [2]int{cfg.StartingStack, cfg.StartingStack}
	rng := rand.New(rand.NewSource(rngSeed))
	startedAt := time.Now()

	gameRecord := store.GameRecord{
		P1ID: p1ID, P2ID: p2ID,
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
		seats := [2]engine.PlayerSeat{
			{ID: 0, Stack: stacks[0], Player: makeP1()},
			{ID: 1, Stack: stacks[1], Player: makeP2()},
		}
		events, result := engine.PlayHand(seats, button, cfg, rng, h)

		// 更新累积 stack(engine 的 FinalStacks 已经把 pot 结算到赢家)
		stacks = result.FinalStacks

		// 翻译成 HandRecord
		hr := translateHand(h, button, events, result, p1ID, p2ID)
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
	gameRecord.P1FinalChips = stacks[0]
	gameRecord.P2FinalChips = stacks[1]
	gameRecord.FinishedAt = time.Now()
	if winner == 0 {
		gameRecord.WinnerPID = p1ID
	} else if winner == 1 {
		gameRecord.WinnerPID = p2ID
	} else {
		gameRecord.IsDraw = true
	}

	out := &Result{
		HandsPlayed: handsPlayed,
		Winner:      winner,
		FinalStacks: stacks,
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
		out.EloChange = [2]float64{new1 - float64(elo1), new2 - float64(elo2)}
	}

	return out, nil
}

// translateHand 把一手的 events + result 翻译成 store.HandRecord。
func translateHand(handIndex, button int, events []engine.Event, result engine.HandResult, p1ID, p2ID int64) store.HandRecord {
	hr := store.HandRecord{
		HandIndex:  handIndex,
		ButtonSeat: button,
		Folded:     result.Folded,
		Pot:        result.PotWon,
	}

	// 摊牌时填赢家 ID;fold 时赢家也填(fold 后唯一的赢家)
	// engine 的 Winners 是 seat 索引(0/1)。单赢家或平局。
	if len(result.Winners) == 1 {
		seat := result.Winners[0]
		if seat == 0 {
			hr.WinnerPID = p1ID
		} else {
			hr.WinnerPID = p2ID
		}
	} else if len(result.Winners) > 1 {
		hr.IsDraw = true
	}

	// 收集底牌 / 公共牌 / 动作
	holeCards := [2][]engine.Card{}
	var community []engine.Card
	seq := 0
	for _, ev := range events {
		switch ev.Type {
		case engine.DealtHole:
			if ev.Seat == 0 {
				holeCards[0] = append([]engine.Card{}, ev.Cards...)
			} else if ev.Seat == 1 {
				holeCards[1] = append([]engine.Card{}, ev.Cards...)
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
				PlayerID:   seatToPlayerID(ev.Seat, p1ID, p2ID),
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

	hr.P1Hole = cardsToStr(holeCards[0])
	hr.P2Hole = cardsToStr(holeCards[1])
	hr.Community = cardsToStr(community)
	return hr
}

func seatToPlayerID(seat int, p1ID, p2ID int64) int64 {
	if seat == 0 {
		return p1ID
	}
	return p2ID
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
