// Package store 用 SQLite 落库每一手、每个动作、每段内心戏。
package store

import (
	"database/sql"
	"encoding/json"
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
    num_seats      INTEGER NOT NULL,
    hands_played   INTEGER NOT NULL,
    winner_id      INTEGER,
    started_at     TEXT NOT NULL,
    finished_at    TEXT NOT NULL,
    config_json    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS game_seats (
    id          INTEGER PRIMARY KEY,
    game_id     INTEGER NOT NULL REFERENCES games(id),
    seat        INTEGER NOT NULL,
    player_id   INTEGER NOT NULL REFERENCES players(id),
    final_chips INTEGER NOT NULL,
    is_winner   INTEGER NOT NULL,
    UNIQUE(game_id, seat)
);
CREATE INDEX IF NOT EXISTS idx_game_seats_game ON game_seats(game_id);
CREATE INDEX IF NOT EXISTS idx_game_seats_player ON game_seats(player_id);

CREATE TABLE IF NOT EXISTS hands (
    id          INTEGER PRIMARY KEY,
    game_id     INTEGER NOT NULL REFERENCES games(id),
    hand_index  INTEGER NOT NULL,
    button_seat INTEGER NOT NULL,
    winner_id   INTEGER,
    folded      INTEGER NOT NULL,
    pot         INTEGER NOT NULL,
    player_holes TEXT NOT NULL,
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

// GameSeat 是局中一个座位的信息。
type GameSeat struct {
	PlayerID   int64
	FinalChips int
	IsWinner   bool
}

// HandRecord 是一手牌的完整记录,用于一次性落库。
type HandRecord struct {
	HandIndex   int    // 1-based
	ButtonSeat  int    // 0 或 1
	WinnerPID   int64  // player id;0 表示平局(用 0 占位,SQL 里写 NULL)
	IsDraw      bool   // true 时 WinnerPID 忽略,写 NULL
	Folded      bool
	Pot         int
	PlayerHoles []string // seat 索引,空字符串表示该座位无底牌(未摊牌/已弃牌)
	Community   string   // "Th 7c 6h" 或 ""
	Actions     []ActionRecord
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
	NumSeats    int              // 2-6
	Seats       []GameSeat       // length == NumSeats, seat 索引
	HandsPlayed int
	IsDraw      bool             // true = 无明确赢家(平局/破产)
	StartedAt   time.Time
	FinishedAt  time.Time
	ConfigJSON  string
	Hands       []HandRecord
}

// RecordGame 把一局(含所有手牌与动作)一次性写入。一个事务,失败回滚。
func (s *Store) RecordGame(g GameRecord) (gameID int64, err error) {
	if len(g.Seats) != g.NumSeats {
		return 0, fmt.Errorf("RecordGame: Seats length %d != NumSeats %d", len(g.Seats), g.NumSeats)
	}
	for _, h := range g.Hands {
		if len(h.PlayerHoles) != g.NumSeats {
			return 0, fmt.Errorf("RecordGame: Hand %d PlayerHoles length %d != NumSeats %d", h.HandIndex, len(h.PlayerHoles), g.NumSeats)
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// 确定 winner_id:取第一个 IsWinner 的玩家 ID;平局/无赢家则为 NULL
	var winnerID sql.NullInt64
	for _, seat := range g.Seats {
		if seat.IsWinner && !g.IsDraw {
			winnerID = sql.NullInt64{Int64: seat.PlayerID, Valid: true}
			break
		}
	}

	res, err := tx.Exec(
		`INSERT INTO games(num_seats, hands_played, winner_id, started_at, finished_at, config_json)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		g.NumSeats, g.HandsPlayed, winnerID,
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

	// 插入 game_seats
	for seatIdx, seat := range g.Seats {
		isWinner := 0
		if seat.IsWinner {
			isWinner = 1
		}
		_, err := tx.Exec(
			`INSERT INTO game_seats(game_id, seat, player_id, final_chips, is_winner)
			 VALUES (?, ?, ?, ?, ?)`,
			gameID, seatIdx, seat.PlayerID, seat.FinalChips, isWinner,
		)
		if err != nil {
			return 0, fmt.Errorf("insert game_seats seat=%d: %w", seatIdx, err)
		}
	}

	// 插入 hands + actions
	for _, h := range g.Hands {
		hWinner := sql.NullInt64{}
		if !h.IsDraw && h.WinnerPID != 0 {
			hWinner = sql.NullInt64{Int64: h.WinnerPID, Valid: true}
		}
		folded := 0
		if h.Folded {
			folded = 1
		}

		// 将 PlayerHoles []string 序列化为 JSON: ["As Kh", "Qd 9s", "", "", "", ""]
		holesJSON, err := json.Marshal(h.PlayerHoles)
		if err != nil {
			return 0, fmt.Errorf("marshal player_holes: %w", err)
		}

		hRes, err := tx.Exec(
			`INSERT INTO hands(game_id, hand_index, button_seat, winner_id, folded, pot, player_holes, community)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			gameID, h.HandIndex, h.ButtonSeat, hWinner, folded, h.Pot,
			string(holesJSON), h.Community,
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
	// 统计逻辑:
	// - games:该玩家参与的局数(DISTINCT game_id)
	// - wins:该玩家为赢家(is_winner=1)的局数
	// - net_chips:Σ(该玩家的 final_chips) - (该局参与人数 * starting_stack)
	//   简化计算:SUM(final_chips) - games * starting_stack(假设所有局都是 1000 起始)
	//   更精确的是从 config_json 解析,但这里用固定 1000 足够
	const startingStack = 1000
	rows, err := s.db.Query(`
		SELECT p.id, p.provider, p.model, p.label, p.elo,
		       COUNT(DISTINCT gs.game_id) AS games,
		       SUM(CASE WHEN gs.is_winner = 1 THEN 1 ELSE 0 END) AS wins,
		       COALESCE(SUM(gs.final_chips), 0) - COALESCE(COUNT(DISTINCT gs.game_id) * ?, 0) AS net_chips
		FROM players p
		LEFT JOIN game_seats gs ON gs.player_id = p.id
		GROUP BY p.id
		ORDER BY p.elo DESC, p.label ASC
	`, startingStack)
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

// PlayerSummary 是局列表页中一个玩家的摘要。
type PlayerSummary struct {
	Label      string `json:"label"`
	FinalChips int    `json:"final_chips"`
	IsWinner   bool   `json:"is_winner"`
}

// GameSummary 是局列表页一行的数据。
type GameSummary struct {
	ID          int64           `json:"id"`
	NumSeats    int             `json:"num_seats"`
	Players     []PlayerSummary `json:"players"` // seat 顺序
	HandsPlayed int             `json:"hands_played"`
	StartedAt   string          `json:"started_at"`
	IsDraw      bool            `json:"is_draw"`
}

// ListGames 返回最近 limit 局(默认按 id desc)。limit<=0 时默认 100。
func (s *Store) ListGames(limit int) ([]GameSummary, error) {
	if limit <= 0 {
		limit = 100
	}

	// 两段查询:先拉游戏元信息,再批量拉座位
	gameRows, err := s.db.Query(`
		SELECT id, num_seats, hands_played, started_at, (winner_id IS NULL) AS is_draw
		FROM games
		ORDER BY id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list games query: %w", err)
	}
	defer gameRows.Close()

	type gameMeta struct {
		ID          int64
		NumSeats    int
		HandsPlayed int
		StartedAt   string
		IsDraw      bool
	}
	var games []gameMeta
	var gameIDs []int64
	for gameRows.Next() {
		var g gameMeta
		if err := gameRows.Scan(&g.ID, &g.NumSeats, &g.HandsPlayed, &g.StartedAt, &g.IsDraw); err != nil {
			return nil, fmt.Errorf("scan game meta: %w", err)
		}
		games = append(games, g)
		gameIDs = append(gameIDs, g.ID)
	}
	if err := gameRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate game rows: %w", err)
	}

	if len(games) == 0 {
		return []GameSummary{}, nil
	}

	// 批量拉座位信息:SELECT ... WHERE game_id IN (?, ?, ...)
	seatQuery := `SELECT gs.game_id, gs.seat, p.label, gs.final_chips, gs.is_winner
	              FROM game_seats gs
	              JOIN players p ON p.id = gs.player_id
	              WHERE gs.game_id IN (`
	seatArgs := make([]any, len(gameIDs))
	for i, id := range gameIDs {
		if i > 0 {
			seatQuery += ",?"
		} else {
			seatQuery += "?"
		}
		seatArgs[i] = id
	}
	seatQuery += ") ORDER BY gs.game_id, gs.seat"

	seatRows, err := s.db.Query(seatQuery, seatArgs...)
	if err != nil {
		return nil, fmt.Errorf("list seats query: %w", err)
	}
	defer seatRows.Close()

	// 按 game_id 组织座位
	seatsByGame := map[int64][]PlayerSummary{}
	for seatRows.Next() {
		var gameID int64
		var seat int
		var label string
		var finalChips int
		var isWinner int
		if err := seatRows.Scan(&gameID, &seat, &label, &finalChips, &isWinner); err != nil {
			return nil, fmt.Errorf("scan seat: %w", err)
		}
		seatsByGame[gameID] = append(seatsByGame[gameID], PlayerSummary{
			Label:      label,
			FinalChips: finalChips,
			IsWinner:   isWinner == 1,
		})
	}
	if err := seatRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate seat rows: %w", err)
	}

	// 组装最终结果
	out := make([]GameSummary, 0, len(games))
	for _, g := range games {
		out = append(out, GameSummary{
			ID:          g.ID,
			NumSeats:    g.NumSeats,
			Players:     seatsByGame[g.ID],
			HandsPlayed: g.HandsPlayed,
			StartedAt:   g.StartedAt,
			IsDraw:      g.IsDraw,
		})
	}
	return out, nil
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
	PlayerHoles []string       `json:"player_holes"` // seat 索引
	Community   string         `json:"community"`
	Actions     []ActionDetail `json:"actions"`
}

// PlayerDetail 是回放页中一个玩家的明细。
type PlayerDetail struct {
	Seat       int    `json:"seat"`
	PlayerID   int64  `json:"player_id"`
	Label      string `json:"label"`
	FinalChips int    `json:"final_chips"`
	IsWinner   bool   `json:"is_winner"`
}

// GameDetail 是单局完整明细(回放页主体数据)。
type GameDetail struct {
	ID          int64          `json:"id"`
	NumSeats    int            `json:"num_seats"`
	Players     []PlayerDetail `json:"players"` // seat 顺序
	HandsPlayed int            `json:"hands_played"`
	StartedAt   string         `json:"started_at"`
	FinishedAt  string         `json:"finished_at"`
	IsDraw      bool           `json:"is_draw"`
	Hands       []HandDetail   `json:"hands"`
}

// GetGame 返回单局完整明细(含所有手与动作)。局不存在返回 (nil, nil)。
func (s *Store) GetGame(gameID int64) (*GameDetail, error) {
	// 先拉局元信息
	var g GameDetail
	err := s.db.QueryRow(`
		SELECT id, num_seats, hands_played, started_at, finished_at, (winner_id IS NULL)
		FROM games
		WHERE id = ?
	`, gameID).Scan(
		&g.ID, &g.NumSeats, &g.HandsPlayed, &g.StartedAt, &g.FinishedAt, &g.IsDraw,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get game query: %w", err)
	}

	// 拉座位信息
	seatRows, err := s.db.Query(`
		SELECT gs.seat, gs.player_id, p.label, gs.final_chips, gs.is_winner
		FROM game_seats gs
		JOIN players p ON p.id = gs.player_id
		WHERE gs.game_id = ?
		ORDER BY gs.seat
	`, gameID)
	if err != nil {
		return nil, fmt.Errorf("get seats query: %w", err)
	}
	defer seatRows.Close()

	g.Players = make([]PlayerDetail, 0, g.NumSeats)
	for seatRows.Next() {
		var seat int
		var playerID int64
		var label string
		var finalChips int
		var isWinner int
		if err := seatRows.Scan(&seat, &playerID, &label, &finalChips, &isWinner); err != nil {
			return nil, fmt.Errorf("scan seat: %w", err)
		}
		g.Players = append(g.Players, PlayerDetail{
			Seat:       seat,
			PlayerID:   playerID,
			Label:      label,
			FinalChips: finalChips,
			IsWinner:   isWinner == 1,
		})
	}
	if err := seatRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate seat rows: %w", err)
	}

	// 一次 JOIN 拉所有 hands + actions,Go 侧按 hand_index 分组
	rows, err := s.db.Query(`
		SELECT h.hand_index, h.button_seat, h.folded, h.pot,
		       COALESCE(ph.label, '') AS winner_label,
		       (h.winner_id IS NULL) AS is_draw,
		       h.player_holes, h.community,
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
			handIndex     int
			buttonSeat    int
			folded        int
			pot           int
			handWinnerLabel sql.NullString
			isDraw        int
			playerHolesJSON string
			community      string
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
			&playerHolesJSON, &community,
			&seq, &street, &seat, &playerLabel,
			&actionType, &amount, &potBefore, &toCall,
			&reasoning, &handStrength, &estEquity, &isBluffing,
		); err != nil {
			return nil, fmt.Errorf("scan hand+action row: %w", err)
		}

		hd, ok := handByIndex[handIndex]
		if !ok {
			// 解析 player_holes JSON
			var playerHoles []string
			if err := json.Unmarshal([]byte(playerHolesJSON), &playerHoles); err != nil {
				return nil, fmt.Errorf("unmarshal player_holes: %w", err)
			}
			if len(playerHoles) != g.NumSeats {
				return nil, fmt.Errorf("player_holes length %d != num_seats %d", len(playerHoles), g.NumSeats)
			}

			hd = &HandDetail{
				HandIndex:   handIndex,
				ButtonSeat:  buttonSeat,
				Folded:      folded == 1,
				Pot:         pot,
				WinnerLabel: handWinnerLabel.String,
				IsDraw:      isDraw == 1,
				PlayerHoles: playerHoles,
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
