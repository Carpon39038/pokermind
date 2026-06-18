// Package store 用 SQLite 落库每一手、每个动作、每段内心戏。
package store

import (
	"database/sql"
	"fmt"
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

// GameSummary 是局列表页一行的数据。
type GameSummary struct {
	ID          int64  `json:"id"`
	P1Label     string `json:"p1_label"`
	P2Label     string `json:"p2_label"`
	WinnerLabel string `json:"winner_label"` // 空串表示平局
	IsDraw      bool   `json:"is_draw"`
	HandsPlayed int    `json:"hands_played"`
	StartedAt   string `json:"started_at"`
	P1Final     int    `json:"p1_final"`
	P2Final     int    `json:"p2_final"`
}

// ListGames 返回最近 limit 局(默认按 id desc)。limit<=0 时默认 100。
func (s *Store) ListGames(limit int) ([]GameSummary, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT g.id, p1.label, p2.label,
		       COALESCE(pw.label, '') AS winner_label,
		       (g.winner_id IS NULL) AS is_draw,
		       g.hands_played, g.started_at, g.p1_final_chips, g.p2_final_chips
		FROM games g
		JOIN players p1 ON p1.id = g.p1_id
		JOIN players p2 ON p2.id = g.p2_id
		LEFT JOIN players pw ON pw.id = g.winner_id
		ORDER BY g.id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list games query: %w", err)
	}
	defer rows.Close()
	var out []GameSummary
	for rows.Next() {
		var g GameSummary
		if err := rows.Scan(&g.ID, &g.P1Label, &g.P2Label, &g.WinnerLabel, &g.IsDraw, &g.HandsPlayed, &g.StartedAt, &g.P1Final, &g.P2Final); err != nil {
			return nil, fmt.Errorf("scan game summary: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ActionDetail 是回放页一个动作的完整数据(含内心戏)。
type ActionDetail struct {
	Seq          int     `json:"seq"`
	Street       string  `json:"street"`
	Seat         int     `json:"seat"`
	PlayerLabel  string  `json:"player_label"`
	ActionType   string  `json:"action_type"`
	Amount       int     `json:"amount"`
	PotBefore    int     `json:"pot_before"`
	ToCall       int     `json:"to_call"`
	HasReport    bool    `json:"has_report"`
	Reasoning    string  `json:"reasoning"`
	HandStrength float64 `json:"hand_strength"`
	EstEquity    float64 `json:"estimated_equity"`
	IsBluffing   bool    `json:"is_bluffing"`
}

// HandDetail 是回放页一手牌的完整数据。
type HandDetail struct {
	HandIndex   int            `json:"hand_index"`
	ButtonSeat  int            `json:"button_seat"`
	Folded      bool           `json:"folded"`
	Pot         int            `json:"pot"`
	WinnerLabel string         `json:"winner_label"` // 空串表示平局
	IsDraw      bool           `json:"is_draw"`
	P1Hole      string         `json:"p1_hole"`
	P2Hole      string         `json:"p2_hole"`
	Community   string         `json:"community"`
	Actions     []ActionDetail `json:"actions"`
}

// GameDetail 是单局完整明细(回放页主体数据)。
type GameDetail struct {
	ID          int64        `json:"id"`
	P1Label     string       `json:"p1_label"`
	P2Label     string       `json:"p2_label"`
	P1PlayerID  int64        `json:"p1_player_id"`
	P2PlayerID  int64        `json:"p2_player_id"`
	WinnerLabel string       `json:"winner_label"`
	IsDraw      bool         `json:"is_draw"`
	HandsPlayed int          `json:"hands_played"`
	StartedAt   string       `json:"started_at"`
	FinishedAt  string       `json:"finished_at"`
	P1Final     int          `json:"p1_final"`
	P2Final     int          `json:"p2_final"`
	Hands       []HandDetail `json:"hands"`
}

// GetGame 返回单局完整明细(含所有手与动作)。局不存在返回 (nil, nil)。
func (s *Store) GetGame(gameID int64) (*GameDetail, error) {
	// 先拉局元信息
	var g GameDetail
	var winnerLabel sql.NullString
	err := s.db.QueryRow(`
		SELECT g.id, p1.label, p2.label, g.p1_id, g.p2_id,
		       COALESCE(pw.label, ''), (g.winner_id IS NULL),
		       g.hands_played, g.started_at, g.finished_at,
		       g.p1_final_chips, g.p2_final_chips
		FROM games g
		JOIN players p1 ON p1.id = g.p1_id
		JOIN players p2 ON p2.id = g.p2_id
		LEFT JOIN players pw ON pw.id = g.winner_id
		WHERE g.id = ?
	`, gameID).Scan(
		&g.ID, &g.P1Label, &g.P2Label, &g.P1PlayerID, &g.P2PlayerID,
		&winnerLabel, &g.IsDraw,
		&g.HandsPlayed, &g.StartedAt, &g.FinishedAt,
		&g.P1Final, &g.P2Final,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get game query: %w", err)
	}
	g.WinnerLabel = winnerLabel.String

	// 一次 JOIN 拉所有 hands + actions,Go 侧按 hand_index 分组
	rows, err := s.db.Query(`
		SELECT h.hand_index, h.button_seat, h.folded, h.pot,
		       COALESCE(ph.label, '') AS winner_label,
		       (h.winner_id IS NULL) AS is_draw,
		       h.p1_hole, h.p2_hole, h.community,
		       a.seq, a.street, a.seat, pa.label,
		       a.action_type, a.amount, a.pot_before, a.to_call,
		       a.reasoning, a.hand_strength, a.estimated_equity, a.is_bluffing
		FROM hands h
		LEFT JOIN players ph ON ph.id = h.winner_id
		LEFT JOIN actions a ON a.hand_id = h.id
		LEFT JOIN players pa ON pa.id = a.player_id
		WHERE h.game_id = ?
		ORDER BY h.hand_index, a.seq
	`, gameID)
	if err != nil {
		return nil, fmt.Errorf("get hands+actions query: %w", err)
	}
	defer rows.Close()

	// 按 hand_index 累积;每个新 hand_index 出现时开一个 HandDetail
	handByIndex := map[int]*HandDetail{}
	var handOrder []int
	for rows.Next() {
		var (
			handIndex                 int
			buttonSeat                int
			folded                    int
			pot                       int
			handWinnerLabel           sql.NullString
			isDraw                    int
			p1Hole, p2Hole, community string
			// action 列(可能全 NULL 当该 hand 无动作)
			seq          sql.NullInt64
			street       sql.NullString
			seat         sql.NullInt64
			playerLabel  sql.NullString
			actionType   sql.NullString
			amount       sql.NullInt64
			potBefore    sql.NullInt64
			toCall       sql.NullInt64
			reasoning    sql.NullString
			handStrength sql.NullFloat64
			estEquity    sql.NullFloat64
			isBluffing   sql.NullInt64
		)
		if err := rows.Scan(
			&handIndex, &buttonSeat, &folded, &pot,
			&handWinnerLabel, &isDraw,
			&p1Hole, &p2Hole, &community,
			&seq, &street, &seat, &playerLabel,
			&actionType, &amount, &potBefore, &toCall,
			&reasoning, &handStrength, &estEquity, &isBluffing,
		); err != nil {
			return nil, fmt.Errorf("scan hand+action row: %w", err)
		}

		hd, ok := handByIndex[handIndex]
		if !ok {
			hd = &HandDetail{
				HandIndex:   handIndex,
				ButtonSeat:  buttonSeat,
				Folded:      folded == 1,
				Pot:         pot,
				WinnerLabel: handWinnerLabel.String,
				IsDraw:      isDraw == 1,
				P1Hole:      p1Hole,
				P2Hole:      p2Hole,
				Community:   community,
			}
			handByIndex[handIndex] = hd
			handOrder = append(handOrder, handIndex)
		}

		// 动作列非空则追加
		if seq.Valid {
			ad := ActionDetail{
				Seq:         int(seq.Int64),
				Street:      street.String,
				Seat:        int(seat.Int64),
				PlayerLabel: playerLabel.String,
				ActionType:  actionType.String,
				Amount:      int(amount.Int64),
				PotBefore:   int(potBefore.Int64),
				ToCall:      int(toCall.Int64),
			}
			if reasoning.Valid {
				ad.HasReport = true
				ad.Reasoning = reasoning.String
				ad.HandStrength = handStrength.Float64
				ad.EstEquity = estEquity.Float64
				ad.IsBluffing = isBluffing.Int64 == 1
			}
			hd.Actions = append(hd.Actions, ad)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter hand+action rows: %w", err)
	}

	g.Hands = make([]HandDetail, 0, len(handOrder))
	for _, idx := range handOrder {
		g.Hands = append(g.Hands, *handByIndex[idx])
	}
	return &g, nil
}
