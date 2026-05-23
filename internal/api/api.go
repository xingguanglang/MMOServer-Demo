// Package api exposes a small HTTP/JSON control API over the game server:
// spawn players at a coordinate, push exact coordinates to them, and query
// where everyone is. It lets external tools drive and observe the world
// without speaking the binary TCP protocol.
package api

import (
	_ "embed"
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/xingguanglang/MMOServer-Demo/internal/client"
	"github.com/xingguanglang/MMOServer-Demo/internal/config"
)

//go:embed web/index.html
var indexHTML []byte

// Metrics 是管理台轮询的运行指标。
type Metrics struct {
	Players     int    `json:"players"`     // 场景内玩家数
	Connections int    `json:"connections"` // 当前连接数
	SentBytes   uint64 `json:"sentBytes"`   // 累计发出字节(前端按差值算带宽)
	AOIEnabled  bool   `json:"aoiEnabled"`  // 是否开启 AOI
	TickHz      int    `json:"tickHz"`      // 逻辑帧率
	AOIHz       int    `json:"aoiHz"`       // AOI 同步频率
	AllHz       int    `json:"allHz"`       // 全场同步频率
}

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
	metrics  func() Metrics

	launch   func(spectate bool) error      // 可选:启动一个 ebiten 客户端窗口(由 cmd/server 注入)
	setRates func(tickHz, aoiHz, allHz int) // 可选:运行时改帧率(由 cmd/server 注入)
	setAOI   func(enabled bool)             // 可选:运行时切 AOI(由 cmd/server 注入)

	mu   sync.Mutex
	bots map[int64]*client.Client // 经本 API 生成的玩家:playerID -> 连接,供 /api/move 控制
}

// SetLauncher 注入"启动客户端窗口"的能力(服务器在本机 exec 客户端二进制)。
func (s *Server) SetLauncher(launch func(spectate bool) error) { s.launch = launch }

// SetRateSetter 注入"运行时改帧率"的能力。
func (s *Server) SetRateSetter(setRates func(tickHz, aoiHz, allHz int)) { s.setRates = setRates }

// SetAOISetter 注入"运行时切 AOI"的能力。
func (s *Server) SetAOISetter(setAOI func(enabled bool)) { s.setAOI = setAOI }

// NewServer 创建 API。gameAddr 是游戏服务器的可拨号地址(如 127.0.0.1:9000);
// snapshot 返回全场玩家位置,metrics 返回运行指标。
func NewServer(gameAddr string, snapshot func() []PlayerPos, metrics func() Metrics) *Server {
	return &Server{
		gameAddr: gameAddr,
		snapshot: snapshot,
		metrics:  metrics,
		bots:     make(map[int64]*client.Client),
	}
}

// Handler 返回路由好的 http.Handler(含管理台页面)。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex) // 管理台首页
	mux.HandleFunc("GET /api/metrics", s.handleMetrics)
	mux.HandleFunc("POST /api/spawn", s.handleSpawn)
	mux.HandleFunc("POST /api/move", s.handleMove)
	mux.HandleFunc("POST /api/loadtest", s.handleLoadtest)
	mux.HandleFunc("POST /api/launch", s.handleLaunch)
	mux.HandleFunc("POST /api/rates", s.handleRates)
	mux.HandleFunc("POST /api/aoi", s.handleAOI)
	mux.HandleFunc("GET /api/players", s.handlePlayers)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	return mux
}

type launchReq struct {
	Spectate bool `json:"spectate"`
}

// handleLaunch 在服务器本机启动一个 ebiten 客户端窗口(普通或观战)。
// 仅本地演示用;exec 的是固定的客户端二进制 + 固定参数,无注入风险。
func (s *Server) handleLaunch(w http.ResponseWriter, r *http.Request) {
	if s.launch == nil {
		http.Error(w, "launcher not available", http.StatusNotImplemented)
		return
	}
	var req launchReq
	json.NewDecoder(r.Body).Decode(&req) // 空 body 也行,默认普通模式
	if err := s.launch(req.Spectate); err != nil {
		http.Error(w, "launch failed (build binaries first: go build -o bin/ ./cmd/...): "+err.Error(), http.StatusInternalServerError)
		return
	}
	mode := "client"
	if req.Spectate {
		mode = "spectator"
	}
	writeJSON(w, http.StatusOK, map[string]string{"opened": mode})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.metrics())
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

type loadtestReq struct {
	Count int `json:"count"`
}

// handleLoadtest 注入 count 个随机游走的机器人,制造负载。
func (s *Server) handleLoadtest(w http.ResponseWriter, r *http.Request) {
	var req loadtestReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	if req.Count < 1 || req.Count > 2000 {
		http.Error(w, "count must be between 1 and 2000", http.StatusBadRequest)
		return
	}
	started := 0
	for i := 0; i < req.Count; i++ {
		if err := s.spawnWanderingBot(); err != nil {
			log.Printf("api: loadtest bot failed: %v", err)
			continue
		}
		started++
	}
	writeJSON(w, http.StatusOK, map[string]int{"started": started})
}

// spawnWanderingBot 生成一个随机游走的机器人(制造移动负载),连接保持打开。
func (s *Server) spawnWanderingBot() error {
	c, err := client.Dial(s.gameAddr)
	if err != nil {
		return err
	}
	if err := c.Login("loadtest-bot"); err != nil {
		c.Close()
		return err
	}
	go c.Run()
	go func() {
		x := float32(rand.Intn(config.MapMaxX))
		y := float32(rand.Intn(config.MapMaxY))
		c.Move(x, y)
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			x = clampf(x+float32(rand.Intn(21)-10), 0, config.MapMaxX)
			y = clampf(y+float32(rand.Intn(21)-10), 0, config.MapMaxY)
			if err := c.Move(x, y); err != nil {
				return
			}
		}
	}()
	s.mu.Lock()
	s.bots[c.PlayerID()] = c
	s.mu.Unlock()
	return nil
}

func clampf(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

type ratesReq struct {
	TickHz int `json:"tickHz"`
	AOIHz  int `json:"aoiHz"`
	AllHz  int `json:"allHz"`
}

// handleRates 运行时修改帧率(<1 的字段会被场景忽略,等于不变)。
func (s *Server) handleRates(w http.ResponseWriter, r *http.Request) {
	if s.setRates == nil {
		http.Error(w, "rate control not available", http.StatusNotImplemented)
		return
	}
	var req ratesReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	s.setRates(req.TickHz, req.AOIHz, req.AllHz)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type aoiReq struct {
	Enabled bool `json:"enabled"`
}

// handleAOI 运行时开/关 AOI(关 = 退回全场广播,带宽明显变大)。
func (s *Server) handleAOI(w http.ResponseWriter, r *http.Request) {
	if s.setAOI == nil {
		http.Error(w, "AOI control not available", http.StatusNotImplemented)
		return
	}
	var req aoiReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	s.setAOI(req.Enabled)
	writeJSON(w, http.StatusOK, map[string]bool{"aoiEnabled": req.Enabled})
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
