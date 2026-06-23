// Package server 提供 Web 回放服务的 HTTP 层。
// JSON API:/api/games、/api/games/{id}。静态文件:/ 与 /static/。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"pokermind/internal/engine"
	"pokermind/internal/match"
	"pokermind/internal/players"
	"pokermind/internal/players/providers"
	"pokermind/internal/store"
)

// Server 包装 HTTP 服务与 store 引用。
type Server struct {
	store     *store.Store
	staticDir string
	mux       *http.ServeMux

	liveMu sync.Mutex
	live   *liveMatch

	thinkingEvents chan thinkingHint // wrapper 写入,forwardThinking 转发到 live
}

// thinkingHint 是 wrapper 给 server 的轻量提示,server 拿到后包装成完整 LiveEvent。
type thinkingHint struct {
	seat   int
	model  string
	street string
}

type liveMatch struct {
	id        string
	cancel    context.CancelFunc
	startedAt time.Time
	subsMu    sync.Mutex
	history   []match.LiveEvent // 已发出的事件,轮询端按 since 切片读
	curHand   int               // 最近一次 hand_started 的 hand 编号,供 thinking 事件填值
	done      chan struct{}     // RunLive 结束后关闭
}

// New 构造一个 Server。staticDir 是前端静态文件目录(绝对或相对路径)。
// 传入空串则不服务静态文件(纯 API 模式,便于测试)。
func New(s *store.Store, staticDir string) *Server {
	srv := &Server{
		store:          s,
		staticDir:      staticDir,
		mux:            http.NewServeMux(),
		thinkingEvents: make(chan thinkingHint, 256),
	}
	go srv.forwardThinking()
	srv.routes()
	return srv
}

// routes 注册全部路由。
func (s *Server) routes() {
	s.mux.HandleFunc("/api/games", s.handleGamesList)
	s.mux.HandleFunc("/api/games/", s.handleGameDetail) // 末尾斜杠匹配子路径
	s.mux.HandleFunc("/api/providers", s.handleProviders)
	s.mux.HandleFunc("/api/providers/", s.handleProviderByName)
	s.mux.HandleFunc("/api/matches", s.handleMatchesStart)
	s.mux.HandleFunc("/api/matches/current", s.handleMatchCurrent)
	s.mux.HandleFunc("/api/matches/current/events", s.handleMatchEvents)
	s.mux.HandleFunc("/api/matches/current/stop", s.handleMatchStop)
	if s.staticDir != "" {
		// /static/* 直接映射到 staticDir 下文件
		fs := http.FileServer(http.Dir(s.staticDir))
		s.mux.Handle("/static/", http.StripPrefix("/static/", fs))
		// / 服务 index.html(其余 hash route 由前端处理)
		s.mux.HandleFunc("/", s.handleIndex)
	}
}

// ServeHTTP 实现 http.Handler。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// handleGamesList: GET /api/games[?limit=N]
func (s *Server) handleGamesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	games, err := s.store.ListGames(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, games)
}

// handleGameDetail: GET /api/games/{id}
func (s *Server) handleGameDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	idStr := strings.TrimPrefix(r.URL.Path, "/api/games/")
	if idStr == "" || strings.Contains(idStr, "/") {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid game id", http.StatusBadRequest)
		return
	}
	g, err := s.store.GetGame(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if g == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

// handleIndex: 服务 SPA 入口 index.html(/ 路径与未匹配的 hash route 都回 index)
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		// 未匹配 API/static 的路径回 index.html,让前端 hash route 接管
		// 例如 /game/123 实际由前端 #/game/123 处理,但用户可能直接访问
		// 简化:非 / 一律 404,前端用 hash route 不需要这个 fallback
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.staticDir, "index.html"))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// 头已写,只能记日志;此处简化不记
		_ = err
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": fmt.Sprintf("%v", err)})
}

// maskedKey 把 apiKey 脱敏成 "***1234"(后 4 位)。
func maskedKey(key string) string {
	if len(key) <= 4 {
		return "***"
	}
	return "***" + key[len(key)-4:]
}

// providerJSON 是 HTTP 返回的 provider 结构(apiKey 脱敏)。
type providerJSON struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"` // 脱敏
}

func toProviderJSON(p store.ProviderCfg) providerJSON {
	return providerJSON{ID: p.ID, Name: p.Name, Kind: p.Kind, BaseURL: p.BaseURL, APIKey: maskedKey(p.APIKey)}
}

// handleProviders: GET 列表(脱敏),POST upsert。
func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := s.store.ListProviders()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		out := make([]providerJSON, 0, len(list))
		for _, p := range list {
			out = append(out, toProviderJSON(p))
		}
		writeJSON(w, http.StatusOK, out)

	case http.MethodPost:
		var body struct {
			Name    string `json:"name"`
			Kind    string `json:"kind"`
			BaseURL string `json:"base_url"`
			APIKey  string `json:"api_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
			return
		}
		if body.Name == "" || body.Kind == "" || body.BaseURL == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("name, kind, base_url required"))
			return
		}
		p, err := s.store.UpsertProvider(body.Name, body.Kind, body.BaseURL, body.APIKey)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, toProviderJSON(*p))

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleProviderByName: GET 单查(脱敏),DELETE 删除。
func (s *Server) handleProviderByName(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/providers/")
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		p, err := s.store.GetProviderByName(name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if p == nil {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, toProviderJSON(*p))
	case http.MethodDelete:
		if err := s.store.DeleteProvider(name); err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleMatchesStart: POST /api/matches
// body: {seats:[{provider, model}], hands, seed?, sb?, bb?, starting_stack?}
func (s *Server) handleMatchesStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.liveMu.Lock()
	if s.live != nil {
		// 若上一局已结束(done 已关闭),清掉它允许开新局
		select {
		case <-s.live.done:
			s.live = nil
		default:
			s.liveMu.Unlock()
			writeError(w, http.StatusConflict, fmt.Errorf("a match is already running"))
			return
		}
	}
	s.liveMu.Unlock()

	var body struct {
		Seats []struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"seats"`
		Hands         int   `json:"hands"`
		Seed          int64 `json:"seed"`
		SmallBlind    int   `json:"sb"`
		BigBlind      int   `json:"bb"`
		StartingStack int   `json:"starting_stack"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	if len(body.Seats) < 2 || len(body.Seats) > 6 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("seats must be 2-6, got %d", len(body.Seats)))
		return
	}
	if body.Hands <= 0 {
		body.Hands = 20
	}
	if body.SmallBlind <= 0 {
		body.SmallBlind = 5
	}
	if body.BigBlind <= 0 {
		body.BigBlind = 10
	}
	if body.StartingStack <= 0 {
		body.StartingStack = 1000
	}

	httpClient := providers.DefaultHTTPClient(0)
	var specs []match.PlayerSpec
	var makePlayers []func() engine.Player
	for i, seat := range body.Seats {
		pcfg, err := s.store.GetProviderByName(seat.Provider)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("lookup provider %q: %w", seat.Provider, err))
			return
		}
		if pcfg == nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("provider %q not found", seat.Provider))
			return
		}
		if pcfg.APIKey == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("provider %q has empty api_key", seat.Provider))
			return
		}
		specs = append(specs, match.PlayerSpec{
			Provider: seat.Provider,
			Model:    seat.Model,
			Label:    fmt.Sprintf("%s:%s", seat.Provider, seat.Model),
		})
		seatIdx := i
		kind, baseURL, apiKey, model := pcfg.Kind, pcfg.BaseURL, pcfg.APIKey, seat.Model
		makePlayers = append(makePlayers, func() engine.Player {
			p, err := providers.ByKind(kind, baseURL, apiKey, httpClient)
			if err != nil {
				return newPanicPlayer(err)
			}
			inner := &players.LLMPlayer{Provider: p, Model: model}
			return &thinkingWrapper{seat: seatIdx, model: model, inner: inner, notify: s.thinkingEvents}
		})
	}

	matchID := fmt.Sprintf("m-%d", time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())

	s.liveMu.Lock()
	if s.live != nil {
		s.liveMu.Unlock()
		cancel()
		writeError(w, http.StatusConflict, fmt.Errorf("a match is already running"))
		return
	}
	s.live = &liveMatch{
		id:        matchID,
		cancel:    cancel,
		startedAt: time.Now(),
		done:      make(chan struct{}),
	}
	lm := s.live
	s.liveMu.Unlock()

	go s.runLiveAndDistribute(ctx, lm, specs, makePlayers, body.Hands, body.Seed,
		engine.Config{SmallBlind: body.SmallBlind, BigBlind: body.BigBlind, StartingStack: body.StartingStack})

	writeJSON(w, http.StatusOK, map[string]any{
		"match_id": matchID,
	})
}

// runLiveAndDistribute 跑 RunLive,把它发出的事件 fan-out 到所有订阅者。
func (s *Server) runLiveAndDistribute(
	ctx context.Context,
	lm *liveMatch,
	specs []match.PlayerSpec,
	makePlayers []func() engine.Player,
	hands int,
	seed int64,
	cfg engine.Config,
) {
	defer close(lm.done)
	defer func() {
		if r := recover(); r != nil {
			ev := match.LiveEvent{Type: match.EvError}
			ev.Payload, _ = json.Marshal(map[string]any{"error": fmt.Sprintf("panic: %v", r)})
			s.appendEvent(lm, ev)
		}
	}()

	out := make(chan match.LiveEvent, 256)
	doneRun := make(chan struct{})
	go func() {
		defer close(doneRun)
		defer close(out)
		_, _ = match.RunLive(ctx, specs, makePlayers, hands, cfg, seed, s.store, out)
	}()

	for ev := range out {
		s.appendEvent(lm, ev)
	}
	<-doneRun

	// 不立即把 s.live 置 nil:保留 history 让前端在 match_finished 之后还能拉到剩余事件。
	// 下一次 handleMatchesStart 会检测 lm.done 已关闭 → 清掉旧的 lm → 开新局。
}

// appendEvent 把 ev 追加到 lm.history。前端通过 /api/matches/current/events?since=N 轮询读取。
// 若 ev 是 hand_started,同步更新 lm.curHand。
func (s *Server) appendEvent(lm *liveMatch, ev match.LiveEvent) {
	lm.subsMu.Lock()
	lm.history = append(lm.history, ev)
	if ev.Type == match.EvHandStarted {
		// 解析 payload 拿 hand 编号
		var p struct{ Hand int `json:"hand"` }
		_ = json.Unmarshal(ev.Payload, &p)
		lm.curHand = p.Hand
	}
	lm.subsMu.Unlock()
}

// handleMatchCurrent: GET /api/matches/current
func (s *Server) handleMatchCurrent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.liveMu.Lock()
	lm := s.live
	s.liveMu.Unlock()
	if lm == nil {
		writeJSON(w, http.StatusOK, map[string]any{"running": false})
		return
	}
	// lm 存在但 done 已关闭 → 上局已结束,waiting for 下次开新局
	select {
	case <-lm.done:
		writeJSON(w, http.StatusOK, map[string]any{"running": false, "match_id": lm.id})
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"running":    true,
			"match_id":   lm.id,
			"started_at": lm.startedAt.Format(time.RFC3339),
		})
	}
}

// handleMatchStop: POST /api/matches/current/stop
func (s *Server) handleMatchStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.liveMu.Lock()
	lm := s.live
	s.liveMu.Unlock()
	if lm == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("no running match"))
		return
	}
	select {
	case <-lm.done:
		writeError(w, http.StatusNotFound, fmt.Errorf("match already finished"))
		return
	default:
	}
	lm.cancel()
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelling"})
}

// handleMatchEvents: GET /api/matches/current/events?since=<seq>
//
//	从 lm.history 返回 seq > since 的所有事件。
//	若没有 running match,返回 running:false + 空 events(前端据此停止轮询)。
//	若 match 已结束但 lm 还在(history 未被清),仍可返回剩余事件 + running:false。
//
// seq 是 history 切片的索引(0-based)。客户端首次用 since=0 拉全部,
// 之后用上次返回的 next_since 继续追。
func (s *Server) handleMatchEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sinceStr := r.URL.Query().Get("since")
	since, _ := strconv.Atoi(sinceStr)
	if since < 0 {
		since = 0
	}

	s.liveMu.Lock()
	lm := s.live
	s.liveMu.Unlock()

	resp := struct {
		Running    bool                `json:"running"`
		MatchID    string              `json:"match_id,omitempty"`
		NextSince  int                 `json:"next_since"`
		Events     []match.LiveEvent   `json:"events"`
	}{}

	if lm != nil {
		lm.subsMu.Lock()
	hist := lm.history
		if since > len(hist) {
			since = len(hist)
		}
		newEvts := append([]match.LiveEvent(nil), hist[since:]...)
		resp.Events = newEvts
		resp.NextSince = len(hist)
		running := true
		select {
		case <-lm.done:
			running = false
		default:
		}
		resp.Running = running
		resp.MatchID = lm.id
		lm.subsMu.Unlock()
	}

	writeJSON(w, http.StatusOK, resp)
}

// panicPlayer 用于 provider 工厂失败时占位,Decide 直接 fold。
type panicPlayer struct{ err error }

func newPanicPlayer(err error) *panicPlayer { return &panicPlayer{err: err} }

func (p *panicPlayer) Decide(obs engine.Observation) engine.Action {
	return engine.Action{Type: engine.Fold}
}

// thinkingWrapper 包装一个 LLMPlayer,在 Decide 入口前给 server 发 hint。
// server 负责把 hint 包装成 thinking 事件并填入当前 hand 编号。
type thinkingWrapper struct {
	seat   int
	model  string
	inner  engine.Player
	notify chan<- thinkingHint
}

func (w *thinkingWrapper) Decide(obs engine.Observation) engine.Action {
	select {
	case w.notify <- thinkingHint{seat: w.seat, model: w.model, street: obs.Street.String()}:
	default:
	}
	return w.inner.Decide(obs)
}

// forwardThinking 把 hint 包装成 thinking LiveEvent,append 到当前 live 的 history。
// hand 编号取自 lm.curHand(由 hand_started 事件更新)。
func (s *Server) forwardThinking() {
	for hint := range s.thinkingEvents {
		s.liveMu.Lock()
		lm := s.live
		s.liveMu.Unlock()
		if lm == nil {
			continue
		}
		lm.subsMu.Lock()
		hand := lm.curHand
		lm.subsMu.Unlock()
		ev := match.LiveEvent{Type: match.EvThinking}
		ev.Payload, _ = json.Marshal(struct {
			Hand   int    `json:"hand"`
			Seat   int    `json:"seat"`
			Model  string `json:"model"`
			Street string `json:"street"`
		}{Hand: hand, Seat: hint.seat, Model: hint.model, Street: hint.street})
		s.appendEvent(lm, ev)
	}
}
