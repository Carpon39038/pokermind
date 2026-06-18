package store

import (
	"path/filepath"
	"testing"
	"time"
)

func freshStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrateIdempotent(t *testing.T) {
	s := freshStore(t)
	// 再调一次 migrate 不应报错(表已存在 IF NOT EXISTS)
	if err := s.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestRegisterPlayerIdempotent(t *testing.T) {
	s := freshStore(t)
	id1, err := s.RegisterPlayer("deepseek", "deepseek-v4-flash", "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("register 1: %v", err)
	}
	id2, err := s.RegisterPlayer("deepseek", "deepseek-v4-flash", "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("register 2: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("idempotent register returned different ids: %d vs %d", id1, id2)
	}
	// 不同 model 应得不同 id
	id3, _ := s.RegisterPlayer("deepseek", "deepseek-v4-pro", "deepseek-v4-pro")
	if id3 == id1 {
		t.Fatalf("different model should get different id")
	}
}

func TestEloDefaultAndUpdate(t *testing.T) {
	s := freshStore(t)
	id, _ := s.RegisterPlayer("p", "m", "lbl")
	elo, err := s.GetElo(id)
	if err != nil {
		t.Fatalf("GetElo: %v", err)
	}
	if elo != 1500 {
		t.Fatalf("default elo = %d, want 1500", elo)
	}
	if err := s.SetElo(id, 1532); err != nil {
		t.Fatalf("SetElo: %v", err)
	}
	got, _ := s.GetElo(id)
	if got != 1532 {
		t.Fatalf("elo after set = %d, want 1532", got)
	}
}

func TestRecordGameAndLeaderboard(t *testing.T) {
	s := freshStore(t)
	p1, _ := s.RegisterPlayer("deepseek", "deepseek-v4-flash", "ds-flash")
	p2, _ := s.RegisterPlayer("glm", "glm-4.6", "glm-4.6")

	g := GameRecord{
		P1ID:         p1,
		P2ID:         p2,
		HandsPlayed:  2,
		WinnerPID:    p1, // p1 赢
		P1FinalChips: 1100,
		P2FinalChips: 900,
		StartedAt:    time.Now(),
		FinishedAt:   time.Now(),
		ConfigJSON:   `{"sb":5,"bb":10}`,
		Hands: []HandRecord{
			{
				HandIndex: 1, ButtonSeat: 0, WinnerPID: p1, Folded: true, Pot: 30,
				P1Hole: "Ac Kc", P2Hole: "Qc Jc", Community: "",
				Actions: []ActionRecord{
					{Seq: 0, Street: "preflop", Seat: 0, PlayerID: p1, ActionType: "raise", Amount: 30, PotBefore: 15, ToCall: 5,
						HasSelfReport: true, Reasoning: "AKs premium", HandStrength: 0.9, EstEquity: 0.65, IsBluffing: false},
					{Seq: 1, Street: "preflop", Seat: 1, PlayerID: p2, ActionType: "fold", Amount: 0, PotBefore: 30, ToCall: 25,
						HasSelfReport: false},
				},
			},
			{
				HandIndex: 2, ButtonSeat: 1, IsDraw: false, WinnerPID: p1, Folded: true, Pot: 20,
				P1Hole: "", P2Hole: "", Community: "",
				Actions: nil,
			},
		},
	}
	gameID, err := s.RecordGame(g)
	if err != nil {
		t.Fatalf("RecordGame: %v", err)
	}
	if gameID == 0 {
		t.Fatalf("gameID should be non-zero")
	}

	// 验证 leaderboard
	lb, err := s.Leaderboard()
	if err != nil {
		t.Fatalf("Leaderboard: %v", err)
	}
	if len(lb) != 2 {
		t.Fatalf("leaderboard len = %d, want 2", len(lb))
	}
	// 两人各 1 局
	var p1Row, p2Row *LeaderboardRow
	for i := range lb {
		if lb[i].PlayerID == p1 {
			p1Row = &lb[i]
		}
		if lb[i].PlayerID == p2 {
			p2Row = &lb[i]
		}
	}
	if p1Row == nil || p2Row == nil {
		t.Fatalf("missing rows in leaderboard: %+v", lb)
	}
	if p1Row.Games != 1 || p1Row.Wins != 1 {
		t.Fatalf("p1 games/wins = %d/%d, want 1/1", p1Row.Games, p1Row.Wins)
	}
	if p1Row.NetChips != 200 { // 1100 - 900 = 200
		t.Fatalf("p1 net chips = %d, want 200", p1Row.NetChips)
	}
	if p2Row.Wins != 0 {
		t.Fatalf("p2 should have 0 wins, got %d", p2Row.Wins)
	}
}

func TestRecordGameAtomicRollbackOnError(t *testing.T) {
	// 故意构造一个 FK 违反(player_id 不存在),验证整事务回滚
	s := freshStore(t)
	p1, _ := s.RegisterPlayer("a", "x", "x")
	// p2 不注册,用一个不存在的 id
	badPID := int64(9999)
	g := GameRecord{
		P1ID:         p1,
		P2ID:         badPID,
		HandsPlayed:  1,
		WinnerPID:    p1,
		P1FinalChips: 1000,
		P2FinalChips: 1000,
		StartedAt:    time.Now(),
		FinishedAt:   time.Now(),
		ConfigJSON:   "{}",
		Hands: []HandRecord{{
			HandIndex: 1, ButtonSeat: 0, WinnerPID: p1, Folded: true, Pot: 10,
			P1Hole: "", P2Hole: "", Community: "",
			Actions: []ActionRecord{{
				Seq: 0, Street: "preflop", Seat: 0, PlayerID: badPID,
				ActionType: "fold", Amount: 0, PotBefore: 10, ToCall: 5,
			}},
		}},
	}
	_, err := s.RecordGame(g)
	if err == nil {
		t.Fatalf("expected error for FK violation, got nil")
	}
	// 验证 games 表里没留下半局
	lb, _ := s.Leaderboard()
	for _, r := range lb {
		if r.Games != 0 {
			t.Fatalf("transaction should have rolled back, but player %d has %d games", r.PlayerID, r.Games)
		}
	}
}

func TestListGamesEmpty(t *testing.T) {
	s := freshStore(t)
	games, err := s.ListGames(0)
	if err != nil {
		t.Fatalf("ListGames: %v", err)
	}
	if len(games) != 0 {
		t.Fatalf("empty db ListGames len = %d, want 0", len(games))
	}
}

func TestListGamesReturnsRecentFirst(t *testing.T) {
	s := freshStore(t)
	p1, _ := s.RegisterPlayer("a", "x", "ax")
	p2, _ := s.RegisterPlayer("a", "y", "ay")
	// 写 3 局
	for i := 0; i < 3; i++ {
		_, err := s.RecordGame(GameRecord{
			P1ID: p1, P2ID: p2,
			HandsPlayed: 1, WinnerPID: p1,
			P1FinalChips: 1100, P2FinalChips: 900,
			StartedAt: time.Now(), FinishedAt: time.Now(),
			ConfigJSON: "{}",
			Hands: []HandRecord{{
				HandIndex: 1, ButtonSeat: 0, WinnerPID: p1, Folded: true, Pot: 10,
				P1Hole: "", P2Hole: "", Community: "",
			}},
		})
		if err != nil {
			t.Fatalf("RecordGame %d: %v", i, err)
		}
	}
	games, err := s.ListGames(0)
	if err != nil {
		t.Fatalf("ListGames: %v", err)
	}
	if len(games) != 3 {
		t.Fatalf("len = %d, want 3", len(games))
	}
	// DESC:第一行应是最新(id 最大)
	if games[0].ID <= games[2].ID {
		t.Fatalf("expected DESC order, got ids %d %d %d", games[0].ID, games[1].ID, games[2].ID)
	}
	// label 正确填充
	if games[0].P1Label != "ax" || games[0].P2Label != "ay" {
		t.Fatalf("labels = %q/%q", games[0].P1Label, games[0].P2Label)
	}
	if games[0].WinnerLabel != "ax" {
		t.Fatalf("winner label = %q, want ax", games[0].WinnerLabel)
	}
}

func TestListGamesRespectsLimit(t *testing.T) {
	s := freshStore(t)
	p1, _ := s.RegisterPlayer("a", "x", "x")
	p2, _ := s.RegisterPlayer("a", "y", "y")
	for i := 0; i < 5; i++ {
		_, _ = s.RecordGame(GameRecord{
			P1ID: p1, P2ID: p2, HandsPlayed: 1, WinnerPID: p1,
			P1FinalChips: 1000, P2FinalChips: 1000,
			StartedAt: time.Now(), FinishedAt: time.Now(),
			ConfigJSON: "{}",
			Hands: []HandRecord{{HandIndex: 1, ButtonSeat: 0, WinnerPID: p1, Folded: true, Pot: 10}},
		})
	}
	games, _ := s.ListGames(2)
	if len(games) != 2 {
		t.Fatalf("len = %d, want 2 (limit)", len(games))
	}
}

func TestGetGameNotFound(t *testing.T) {
	s := freshStore(t)
	g, err := s.GetGame(9999)
	if err != nil {
		t.Fatalf("GetGame unknown should return (nil, nil), got err: %v", err)
	}
	if g != nil {
		t.Fatalf("GetGame unknown should return nil, got %+v", g)
	}
}

func TestGetGameFullTreeWithSelfReport(t *testing.T) {
	s := freshStore(t)
	p1, _ := s.RegisterPlayer("deepseek", "flash", "ds-flash")
	p2, _ := s.RegisterPlayer("glm", "glm4", "glm4")
	gameID, err := s.RecordGame(GameRecord{
		P1ID: p1, P2ID: p2,
		HandsPlayed: 2, WinnerPID: p1,
		P1FinalChips: 1100, P2FinalChips: 900,
		StartedAt: time.Now(), FinishedAt: time.Now(),
		ConfigJSON: `{"sb":5}`,
		Hands: []HandRecord{
			{
				HandIndex: 1, ButtonSeat: 0, WinnerPID: p1, Folded: true, Pot: 30,
				P1Hole: "Ac Kc", P2Hole: "Qc Jc", Community: "",
				Actions: []ActionRecord{
					{Seq: 0, Street: "preflop", Seat: 0, PlayerID: p1, ActionType: "raise", Amount: 30, PotBefore: 15, ToCall: 5,
						HasSelfReport: true, Reasoning: "premium AKs", HandStrength: 0.9, EstEquity: 0.65, IsBluffing: false},
					{Seq: 1, Street: "preflop", Seat: 1, PlayerID: p2, ActionType: "fold", Amount: 0, PotBefore: 30, ToCall: 25,
						HasSelfReport: false},
				},
			},
			{
				HandIndex: 2, ButtonSeat: 1, IsDraw: false, WinnerPID: p2, Folded: false, Pot: 50,
				P1Hole: "Th Td", P2Hole: "2h 7c", Community: "5s 9d Kc",
				Actions: []ActionRecord{
					{Seq: 0, Street: "flop", Seat: 1, PlayerID: p2, ActionType: "call", Amount: 0, PotBefore: 50, ToCall: 0,
						HasSelfReport: true, Reasoning: "free card", HandStrength: 0.1, EstEquity: 0.15, IsBluffing: true},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("RecordGame: %v", err)
	}

	g, err := s.GetGame(gameID)
	if err != nil {
		t.Fatalf("GetGame: %v", err)
	}
	if g == nil {
		t.Fatalf("GetGame returned nil for existing game %d", gameID)
	}
	if g.P1Label != "ds-flash" || g.P2Label != "glm4" {
		t.Fatalf("labels = %q/%q", g.P1Label, g.P2Label)
	}
	if g.WinnerLabel != "ds-flash" {
		t.Fatalf("winner = %q, want ds-flash", g.WinnerLabel)
	}
	if len(g.Hands) != 2 {
		t.Fatalf("hands len = %d, want 2", len(g.Hands))
	}
	// 第一手:2 个动作
	h1 := g.Hands[0]
	if h1.HandIndex != 1 || len(h1.Actions) != 2 {
		t.Fatalf("hand1 index/actions = %d/%d", h1.HandIndex, len(h1.Actions))
	}
	if h1.P1Hole != "Ac Kc" || h1.Community != "" {
		t.Fatalf("hand1 hole/community = %q/%q", h1.P1Hole, h1.Community)
	}
	// 第一手第一个动作带 self report
	a := h1.Actions[0]
	if !a.HasReport || a.Reasoning != "premium AKs" || a.HandStrength != 0.9 || a.IsBluffing {
		t.Fatalf("hand1 action0 self-report wrong: %+v", a)
	}
	// 第一手第二个动作无 self report(rule-bot fold)
	if g.Hands[0].Actions[1].HasReport {
		t.Fatalf("hand1 action1 should have no report")
	}
	// 第二手:1 个动作 + 诈唬标记
	h2 := g.Hands[1]
	if len(h2.Actions) != 1 || !h2.Actions[0].IsBluffing {
		t.Fatalf("hand2 bluff not captured: %+v", h2.Actions)
	}
	if h2.Community != "5s 9d Kc" {
		t.Fatalf("hand2 community = %q", h2.Community)
	}
}
