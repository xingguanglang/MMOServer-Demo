// Package api exposes a small HTTP/JSON control API over the game server:
// spawn players at a coordinate, push exact coordinates to them, and query
// where everyone is. It lets external tools drive and observe the world
// without speaking the binary TCP protocol.
package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/xingguanglang/MMOServer-Demo/internal/client"
)

// PlayerPos 是 API 返回的单个玩家位置。
type PlayerPos struct {
	ID int64   `json:"id"`
	X  float32 `json:"x"`
	Y  float32 `json:"y"`
}

// Server 是 HTTP 控制 API。它通过 gameAddr 用真实 TCP 客户端生成玩家,
// 通过 snapshot 回调读取全场玩家位置(由网关提供)。
type Server struct {
	gameAddr string
	snapshot func() []PlayerPos

	mu   sync.Mutex
	bots map[int64]*client.Client // 经本 API 生成的玩家:playerID -> 连接,供 /api/move 控制
}

// NewServer 创建 API。gameAddr 是游戏服务器的可拨号地址(如 127.0.0.1:9000)。
func NewServer(gameAddr string, snapshot func() []PlayerPos) *Server {
	return &Server{
		gameAddr: gameAddr,
		snapshot: snapshot,
		bots:     make(map[int64]*client.Client),
	}
}

// Handler 返回路由好的 http.Handler。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/spawn", s.handleSpawn)
	mux.HandleFunc("POST /api/move", s.handleMove)
	mux.HandleFunc("GET /api/players", s.handlePlayers)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	return mux
}

type spawnReq struct {
	Count int     `json:"count"`
	X     float32 `json:"x"`
	Y     float32 `json:"y"`
}

// handleSpawn 在精确坐标 (x,y) 生成 count 个玩家,返回它们的 playerID。
func (s *Server) handleSpawn(w http.ResponseWriter, r *http.Request) {
	var req spawnReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	if req.Count < 1 || req.Count > 1000 {
		http.Error(w, "count must be between 1 and 1000", http.StatusBadRequest)
		return
	}

	ids := make([]int64, 0, req.Count)
	for i := 0; i < req.Count; i++ {
		id, err := s.spawnBot(req.X, req.Y)
		if err != nil {
			log.Printf("api: spawn bot failed: %v", err)
			continue
		}
		ids = append(ids, id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"spawned": len(ids), "ids": ids})
}

// spawnBot 连上游戏服务器、登录,放到精确坐标 (x,y),返回服务器分配的 playerID。
// 连接保持打开(Run 在后台读),并记入 bots 表供后续 /api/move 控制。
func (s *Server) spawnBot(x, y float32) (int64, error) {
	c, err := client.Dial(s.gameAddr)
	if err != nil {
		return 0, err
	}
	if err := c.Login("api-bot"); err != nil {
		c.Close()
		return 0, err
	}
	go c.Run()
	c.Move(x, y)

	id := c.PlayerID()
	s.mu.Lock()
	s.bots[id] = c
	s.mu.Unlock()
	return id, nil
}

type moveReq struct {
	ID int64   `json:"id"`
	X  float32 `json:"x"`
	Y  float32 `json:"y"`
}

// handleMove 把某个由本 API 生成的玩家直接挪到精确坐标 (x,y)。
func (s *Server) handleMove(w http.ResponseWriter, r *http.Request) {
	var req moveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	c := s.bots[req.ID]
	s.mu.Unlock()
	if c == nil {
		http.Error(w, "unknown player id (only API-spawned players can be moved)", http.StatusNotFound)
		return
	}
	if err := c.Move(req.X, req.Y); err != nil {
		http.Error(w, "move failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": req.ID, "x": req.X, "y": req.Y})
}

func (s *Server) handlePlayers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.snapshot())
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]int{"players": len(s.snapshot())})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
