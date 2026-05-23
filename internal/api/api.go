// Package api exposes a small HTTP/JSON control API over the game server:
// spawn players at a coordinate, and query where everyone is. It lets
// external tools drive and observe the world without speaking the binary
// TCP protocol.
package api

import (
	"encoding/json"
	"log"
	"math/rand"
	"net/http"

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
}

// NewServer 创建 API。gameAddr 是游戏服务器的可拨号地址(如 127.0.0.1:9000)。
func NewServer(gameAddr string, snapshot func() []PlayerPos) *Server {
	return &Server{gameAddr: gameAddr, snapshot: snapshot}
}

// Handler 返回路由好的 http.Handler。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/spawn", s.handleSpawn)
	mux.HandleFunc("GET /api/players", s.handlePlayers)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	return mux
}

type spawnReq struct {
	Count int     `json:"count"`
	X     float32 `json:"x"`
	Y     float32 `json:"y"`
}

// handleSpawn 在 (x,y) 附近生成 count 个玩家(真实 TCP 连接)。
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

	spawned := 0
	for i := 0; i < req.Count; i++ {
		if err := s.spawnBot(req.X, req.Y); err != nil {
			log.Printf("api: spawn bot failed: %v", err)
			continue
		}
		spawned++
	}
	writeJSON(w, http.StatusOK, map[string]int{"spawned": spawned})
}

// spawnBot 连上游戏服务器、登录,并移动到 (x,y) 附近一个随机小偏移处。
// 连接保持打开(Run 在后台读),因此该玩家会持续存在于世界里。
func (s *Server) spawnBot(x, y float32) error {
	c, err := client.Dial(s.gameAddr)
	if err != nil {
		return err
	}
	if err := c.Login("api-bot"); err != nil {
		c.Close()
		return err
	}
	go c.Run()
	c.Move(x+float32(rand.Intn(21)-10), y+float32(rand.Intn(21)-10))
	return nil
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
