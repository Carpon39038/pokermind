package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pokermind/internal/store"
)

func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	srv := New(s, "") // 纯 API 模式,不服务静态文件
	return srv, s
}

// seedTwoGames 写入两局(一胜一平)用于测试。
func seedTwoGames(t *testing.T, s *store.Store) (p1ID, p2ID int64) {
	t.Helper()
	p1, err := s.RegisterPlayer("deepseek", "flash", "ds-flash")
	if err != nil {
		t.Fatalf("register p1: %v", err)
	}
	p2, err := s.RegisterPlayer("glm", "glm4", "glm4")
	if err != nil {
		t.Fatalf("register p2: %v", err)
	}
	// 局 1:p1 赢,一手带动作
	_, err = s.RecordGame(store.GameRecord{
		NumSeats:   2,
		Seats:      []store.GameSeat{{PlayerID: p1, FinalChips: 1100, IsWinner: true}, {PlayerID: p2, FinalChips: 900, IsWinner: false}},
		HandsPlayed: 1,
		IsDraw:      false,
		StartedAt:   time.Now(),
		FinishedAt:  time.Now(),
		ConfigJSON:  "{}",
		Hands: []store.HandRecord{{
			HandIndex:   1,
			ButtonSeat:  0,
			WinnerPID:   p1,
			Folded:      true,
			Pot:         30,
			PlayerHoles: []string{"Ac Kc", "Qc Jc"},
			Community:   "",
			Actions: []store.ActionRecord{
				{Seq: 0, Street: "preflop", Seat: 0, PlayerID: p1, ActionType: "raise", Amount: 30, PotBefore: 15, ToCall: 5,
					HasSelfReport: true, Reasoning: "AKs", HandStrength: 0.9, EstEquity: 0.65, IsBluffing: false},
				{Seq: 1, Street: "preflop", Seat: 1, PlayerID: p2, ActionType: "fold", Amount: 0, PotBefore: 30, ToCall: 25,
					HasSelfReport: false},
			},
		}},
	})
	if err != nil {
		t.Fatalf("record game1: %v", err)
	}
	// 局 2:平局
	_, err = s.RecordGame(store.GameRecord{
		NumSeats:    2,
		Seats:       []store.GameSeat{{PlayerID: p1, FinalChips: 1000, IsWinner: false}, {PlayerID: p2, FinalChips: 1000, IsWinner: false}},
		HandsPlayed: 1,
		IsDraw:      true,
		StartedAt:   time.Now(),
		FinishedAt:  time.Now(),
		ConfigJSON:  "{}",
		Hands: []store.HandRecord{{
			HandIndex:   1,
			ButtonSeat:  0,
			IsDraw:      true,
			Folded:      true,
			Pot:         15,
			PlayerHoles: []string{"", ""},
		}},
	})
	if err != nil {
		t.Fatalf("record game2: %v", err)
	}
	return p1, p2
}

func TestHandleGamesList(t *testing.T) {
	srv, st := newTestServer(t)
	seedTwoGames(t, st)

	req := httptest.NewRequest(http.MethodGet, "/api/games", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	var games []store.GameSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &games); err != nil {
		t.Fatalf("unmarshal: %v; body: %s", err, rec.Body.String())
	}
	if len(games) != 2 {
		t.Fatalf("games len = %d, want 2", len(games))
	}
	// DESC 顺序:局 2(平)在前
	if !games[0].IsDraw {
		t.Fatalf("first game should be the draw (newest), got is_draw=%v", games[0].IsDraw)
	}
	// 检查新字段
	if games[0].NumSeats != 2 {
		t.Fatalf("num_seats = %d, want 2", games[0].NumSeats)
	}
	if len(games[0].Players) != 2 {
		t.Fatalf("players len = %d, want 2", len(games[0].Players))
	}
}

func TestHandleGamesListRespectsLimit(t *testing.T) {
	srv, st := newTestServer(t)
	seedTwoGames(t, st)

	req := httptest.NewRequest(http.MethodGet, "/api/games?limit=1", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var games []store.GameSummary
	_ = json.Unmarshal(rec.Body.Bytes(), &games)
	if len(games) != 1 {
		t.Fatalf("len = %d, want 1 (limit)", len(games))
	}
}

func TestHandleGameDetailFound(t *testing.T) {
	srv, st := newTestServer(t)
	seedTwoGames(t, st)
	// 局 1 的 id 应是 1
	req := httptest.NewRequest(http.MethodGet, "/api/games/1", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var g store.GameDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &g); err != nil {
		t.Fatalf("unmarshal: %v; body: %s", err, rec.Body.String())
	}
	// 检查新的 Players 结构
	if len(g.Players) != 2 {
		t.Fatalf("players len = %d, want 2", len(g.Players))
	}
	if g.Players[0].Label != "ds-flash" {
		t.Fatalf("players[0] label = %q", g.Players[0].Label)
	}
	if !g.Players[0].IsWinner {
		t.Fatalf("players[0] should be winner")
	}
	if g.NumSeats != 2 {
		t.Fatalf("num_seats = %d, want 2", g.NumSeats)
	}
	if len(g.Hands) != 1 {
		t.Fatalf("hands len = %d, want 1", len(g.Hands))
	}
	if len(g.Hands[0].Actions) != 2 {
		t.Fatalf("actions len = %d, want 2", len(g.Hands[0].Actions))
	}
	// 第一动作带 self report
	a := g.Hands[0].Actions[0]
	if !a.HasReport || a.Reasoning != "AKs" {
		t.Fatalf("action0 report wrong: %+v", a)
	}
	// 第二动作(rule-bot fold)无 report
	if g.Hands[0].Actions[1].HasReport {
		t.Fatalf("action1 should have no report")
	}
}

func TestHandleGameDetailNotFound(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/games/9999", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleGameDetailInvalidID(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/games/abc", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleGameDetailMethodNotAllowed(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/games/1", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestHandleGamesListEmpty(t *testing.T) {
	srv, _ := newTestServer(t) // 空 DB

	req := httptest.NewRequest(http.MethodGet, "/api/games", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var games []store.GameSummary
	_ = json.Unmarshal(rec.Body.Bytes(), &games)
	if len(games) != 0 {
		t.Fatalf("empty db should give empty list, got %d", len(games))
	}
}
