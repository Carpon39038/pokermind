// Package store 用 SQLite 落库每一手、每个动作、每段内心戏。
package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store 包装一个 SQLite 连接。
type Store struct {
	db *sql.DB
}

// Open 打开(必要时创建)SQLite 文件并执行 schema migration。
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	// SQLite 写入需串行;且 modernc 驱动在多连接时锁文件容易报错。
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭底层连接。
func (s *Store) Close() error { return s.db.Close() }

// migrate 建表(idempotent,IF NOT EXISTS)。
func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS players (
    id      INTEGER PRIMARY KEY,
    provider TEXT NOT NULL,
    model   TEXT NOT NULL,
    label   TEXT NOT NULL,
    elo     INTEGER NOT NULL DEFAULT 1500,
    UNIQUE(provider, model)
);

CREATE TABLE IF NOT EXISTS games (
    id             INTEGER PRIMARY KEY,
    p1_id          INTEGER NOT NULL REFERENCES players(id),
    p2_id          INTEGER NOT NULL REFERENCES players(id),
    hands_played   INTEGER NOT NULL,
    winner_id      INTEGER,
    p1_final_chips INTEGER NOT NULL,
    p2_final_chips INTEGER NOT NULL,
    started_at     TEXT NOT NULL,
    finished_at    TEXT NOT NULL,
    config_json    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS hands (
    id          INTEGER PRIMARY KEY,
    game_id     INTEGER NOT NULL REFERENCES games(id),
    hand_index  INTEGER NOT NULL,
    button_seat INTEGER NOT NULL,
    winner_id   INTEGER,
    folded      INTEGER NOT NULL,
    pot         INTEGER NOT NULL,
    p1_hole     TEXT NOT NULL,
    p2_hole     TEXT NOT NULL,
    community   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS actions (
    id               INTEGER PRIMARY KEY,
    hand_id          INTEGER NOT NULL REFERENCES hands(id),
    seq              INTEGER NOT NULL,
    street           TEXT NOT NULL,
    seat             INTEGER NOT NULL,
    player_id        INTEGER NOT NULL REFERENCES players(id),
    action_type      TEXT NOT NULL,
    amount           INTEGER NOT NULL,
    pot_before       INTEGER NOT NULL,
    to_call          INTEGER NOT NULL,
    reasoning        TEXT,
    hand_strength    REAL,
    estimated_equity REAL,
    is_bluffing      INTEGER
);
CREATE INDEX IF NOT EXISTS idx_actions_hand ON actions(hand_id);
CREATE INDEX IF NOT EXISTS idx_actions_player ON actions(player_id);
CREATE INDEX IF NOT EXISTS idx_hands_game ON hands(game_id);
`
	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	return nil
}

// RegisterPlayer 幂等注册一个 player,返回其 id。已存在则返回原 id。
func (s *Store) RegisterPlayer(provider, model, label string) (int64, error) {
	// 先查
	var id int64
	err := s.db.QueryRow(
		`SELECT id FROM players WHERE provider=? AND model=?`,
		provider, model,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("query player: %w", err)
	}
	res, err := s.db.Exec(
		`INSERT INTO players(provider, model, label) VALUES (?, ?, ?)`,
		provider, model, label,
	)
	if err != nil {
		return 0, fmt.Errorf("insert player: %w", err)
	}
	id, err = res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return id, nil
}

// GetElo 返回某 player 当前 ELO。
func (s *Store) GetElo(playerID int64) (int, error) {
	var elo int
	err := s.db.QueryRow(`SELECT elo FROM players WHERE id=?`, playerID).Scan(&elo)
	if err != nil {
		return 0, fmt.Errorf("get elo: %w", err)
	}
	return elo, nil
}

// SetElo 直接覆盖某 player 的 ELO。用于 Match 结束时按 elo.Update 计算后写回。
func (s *Store) SetElo(playerID int64, elo int) error {
	_, err := s.db.Exec(`UPDATE players SET elo=? WHERE id=?`, elo, playerID)
	if err != nil {
		return fmt.Errorf("set elo: %w", err)
	}
	return nil
}

// HandRecord 是一手牌的完整记录,用于一次性落库。
type HandRecord struct {
	HandIndex  int    // 1-based
	ButtonSeat int    // 0 或 1
	WinnerPID  int64  // player id;0 表示平局(用 0 占位,SQL 里写 NULL)
	IsDraw     bool   // true 时 WinnerPID 忽略,写 NULL
	Folded     bool
	Pot        int
	P1Hole     string // "As Kh" 或 ""
	P2Hole     string
	Community  string // "Th 7c 6h" 或 ""
	Actions    []ActionRecord
}

// ActionRecord 是一个动作的完整记录。
type ActionRecord struct {
	Seq           int
	Street        string
	Seat          int
	PlayerID      int64
	ActionType    string
	Amount        int
	PotBefore     int
	ToCall        int
	HasSelfReport bool
	Reasoning     string
	HandStrength  float64
	EstEquity     float64
	IsBluffing    bool
}

// GameRecord 是一局的完整记录。
type GameRecord struct {
	P1ID         int64
	P2ID         int64
	HandsPlayed  int
	WinnerPID    int64 // 0 表示平局
	IsDraw       bool
	P1FinalChips int
	P2FinalChips int
	StartedAt    time.Time
	FinishedAt   time.Time
	ConfigJSON   string
	Hands        []HandRecord
}

// RecordGame 把一局(含所有手牌与动作)一次性写入。一个事务,失败回滚。
func (s *Store) RecordGame(g GameRecord) (gameID int64, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	winner := sql.NullInt64{}
	if !g.IsDraw && g.WinnerPID != 0 {
		winner = sql.NullInt64{Int64: g.WinnerPID, Valid: true}
	}
	res, err := tx.Exec(
		`INSERT INTO games(p1_id, p2_id, hands_played, winner_id, p1_final_chips, p2_final_chips, started_at, finished_at, config_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		g.P1ID, g.P2ID, g.HandsPlayed, winner,
		g.P1FinalChips, g.P2FinalChips,
		g.StartedAt.Format(time.RFC3339), g.FinishedAt.Format(time.RFC3339),
		g.ConfigJSON,
	)
	if err != nil {
		return 0, fmt.Errorf("insert game: %w", err)
	}
	gameID, err = res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("game last insert id: %w", err)
	}

	for _, h := range g.Hands {
		hWinner := sql.NullInt64{}
		if !h.IsDraw && h.WinnerPID != 0 {
			hWinner = sql.NullInt64{Int64: h.WinnerPID, Valid: true}
		}
		folded := 0
		if h.Folded {
			folded = 1
		}
		hRes, err := tx.Exec(
			`INSERT INTO hands(game_id, hand_index, button_seat, winner_id, folded, pot, p1_hole, p2_hole, community)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			gameID, h.HandIndex, h.ButtonSeat, hWinner, folded, h.Pot,
			h.P1Hole, h.P2Hole, h.Community,
		)
		if err != nil {
			return 0, fmt.Errorf("insert hand %d: %w", h.HandIndex, err)
		}
		handID, err := hRes.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("hand last insert id: %w", err)
		}
		for _, a := range h.Actions {
			var reasoning sql.NullString
			var hs, eq sql.NullFloat64
			var bluff sql.NullInt64
			if a.HasSelfReport {
				reasoning = sql.NullString{String: a.Reasoning, Valid: true}
				hs = sql.NullFloat64{Float64: a.HandStrength, Valid: true}
				eq = sql.NullFloat64{Float64: a.EstEquity, Valid: true}
				if a.IsBluffing {
					bluff = sql.NullInt64{Int64: 1, Valid: true}
				} else {
					bluff = sql.NullInt64{Int64: 0, Valid: true}
				}
			}
			_, err := tx.Exec(
				`INSERT INTO actions(hand_id, seq, street, seat, player_id, action_type, amount, pot_before, to_call, reasoning, hand_strength, estimated_equity, is_bluffing)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				handID, a.Seq, a.Street, a.Seat, a.PlayerID, a.ActionType, a.Amount,
				a.PotBefore, a.ToCall, reasoning, hs, eq, bluff,
			)
			if err != nil {
				return 0, fmt.Errorf("insert action seq=%d: %w", a.Seq, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return gameID, nil
}

// LeaderboardRow 是排行榜一行。
type LeaderboardRow struct {
	PlayerID  int64
	Provider  string
	Model     string
	Label     string
	Elo       int
	Games     int
	Wins      int
	NetChips  int // 累计筹码净值(全部 game 的 final - starting 之和)
}

// Leaderboard 按 ELO 降序返回所有 player 的统计。
func (s *Store) Leaderboard() ([]LeaderboardRow, error) {
	rows, err := s.db.Query(`
		SELECT p.id, p.provider, p.model, p.label, p.elo,
		       COUNT(DISTINCT g.id) AS games,
		       COUNT(DISTINCT CASE WHEN g.winner_id = p.id THEN g.id END) AS wins,
		       COALESCE(SUM(
		         CASE WHEN p.id = g.p1_id THEN g.p1_final_chips - g.p2_final_chips
		              ELSE g.p2_final_chips - g.p1_final_chips END
		       ), 0) AS net
		FROM players p
		LEFT JOIN games g ON (p.id = g.p1_id OR p.id = g.p2_id)
		GROUP BY p.id
		ORDER BY p.elo DESC, p.label ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("leaderboard query: %w", err)
	}
	defer rows.Close()
	var out []LeaderboardRow
	for rows.Next() {
		var r LeaderboardRow
		if err := rows.Scan(&r.PlayerID, &r.Provider, &r.Model, &r.Label, &r.Elo, &r.Games, &r.Wins, &r.NetChips); err != nil {
			return nil, fmt.Errorf("scan leaderboard: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// (未使用 strings 的话删掉 import —— 留着先,后面格式化可能用)
var _ = strings.TrimSpace
