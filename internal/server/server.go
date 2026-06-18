// Package server 提供 Web 回放服务的 HTTP 层。
// JSON API:/api/games、/api/games/{id}。静态文件:/ 与 /static/。
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"pokermind/internal/store"
)

// Server 包装 HTTP 服务与 store 引用。
type Server struct {
	store     *store.Store
	staticDir string
	mux       *http.ServeMux
}

// New 构造一个 Server。staticDir 是前端静态文件目录(绝对或相对路径)。
// 传入空串则不服务静态文件(纯 API 模式,便于测试)。
func New(s *store.Store, staticDir string) *Server {
	srv := &Server{store: s, staticDir: staticDir, mux: http.NewServeMux()}
	srv.routes()
	return srv
}

// routes 注册全部路由。
func (s *Server) routes() {
	s.mux.HandleFunc("/api/games", s.handleGamesList)
	s.mux.HandleFunc("/api/games/", s.handleGameDetail) // 末尾斜杠匹配子路径
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
