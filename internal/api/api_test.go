package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xingguanglang/MMOServer-Demo/internal/gateway"
)

// TestSpawnAndQuery 端到端验证 HTTP API:POST /api/spawn 生成玩家,
// GET /api/players 能查到它们。
func TestSpawnAndQuery(t *testing.T) {
	// 起一个真实游戏服务器。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	gw := gateway.NewServer(true)
	go gw.Serve(ln)
	gameAddr := ln.Addr().String()

	// 起 API 服务器(httptest)。
	apiSrv := NewServer(gameAddr, func() []PlayerPos {
		snap := gw.Snapshot()
		out := make([]PlayerPos, 0, len(snap))
		for _, p := range snap {
			out = append(out, PlayerPos{ID: p.ID, X: p.X, Y: p.Y})
		}
		return out
	}, func() Metrics { return Metrics{} })
	ts := httptest.NewServer(apiSrv.Handler())
	defer ts.Close()

	// 生成 3 个玩家。
	resp, err := http.Post(ts.URL+"/api/spawn", "application/json",
		strings.NewReader(`{"count":3,"x":100,"y":100}`))
	if err != nil {
		t.Fatalf("spawn request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("spawn status: %d", resp.StatusCode)
	}

	// 轮询 /api/players,直到看到 3 个玩家(状态同步 10Hz,几百 ms 内出现)。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r, err := http.Get(ts.URL + "/api/players")
		if err != nil {
			t.Fatalf("players request: %v", err)
		}
		var players []PlayerPos
		json.NewDecoder(r.Body).Decode(&players)
		r.Body.Close()
		if len(players) >= 3 {
			return // 通过
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("expected at least 3 players via /api/players")
}

// TestSpawnReturnsIDsAndMove:spawn 返回 id;再用 /api/move 把该玩家推到精确坐标,
// 通过 /api/players 验证它确实到了新位置。
func TestSpawnReturnsIDsAndMove(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	gw := gateway.NewServer(true)
	go gw.Serve(ln)

	apiSrv := NewServer(ln.Addr().String(), func() []PlayerPos {
		snap := gw.Snapshot()
		out := make([]PlayerPos, 0, len(snap))
		for _, p := range snap {
			out = append(out, PlayerPos{ID: p.ID, X: p.X, Y: p.Y})
		}
		return out
	}, func() Metrics { return Metrics{} })
	ts := httptest.NewServer(apiSrv.Handler())
	defer ts.Close()

	// spawn 一个,拿到它的 id。
	resp, err := http.Post(ts.URL+"/api/spawn", "application/json",
		strings.NewReader(`{"count":1,"x":10,"y":10}`))
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	var spawnResp struct {
		Spawned int     `json:"spawned"`
		IDs     []int64 `json:"ids"`
	}
	json.NewDecoder(resp.Body).Decode(&spawnResp)
	resp.Body.Close()
	if len(spawnResp.IDs) != 1 {
		t.Fatalf("expected 1 id, got %v", spawnResp.IDs)
	}
	id := spawnResp.IDs[0]

	// 把它推到精确坐标 (50,60)。
	mvResp, err := http.Post(ts.URL+"/api/move", "application/json",
		strings.NewReader(`{"id":`+itoa(id)+`,"x":50,"y":60}`))
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	mvResp.Body.Close()
	if mvResp.StatusCode != http.StatusOK {
		t.Fatalf("move status: %d", mvResp.StatusCode)
	}

	// 验证它到了 (50,60)。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r, _ := http.Get(ts.URL + "/api/players")
		var players []PlayerPos
		json.NewDecoder(r.Body).Decode(&players)
		r.Body.Close()
		for _, p := range players {
			if p.ID == id && p.X == 50 && p.Y == 60 {
				return // 通过
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("玩家未移动到 /api/move 指定的坐标")
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// TestLaunch 验证 /api/launch 调用注入的启动器(用桩,不真开窗口)。
func TestLaunch(t *testing.T) {
	apiSrv := NewServer("127.0.0.1:1", func() []PlayerPos { return nil }, func() Metrics { return Metrics{} })
	var called, gotSpectate bool
	apiSrv.SetLauncher(func(spectate bool) error {
		called = true
		gotSpectate = spectate
		return nil
	})
	ts := httptest.NewServer(apiSrv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/launch", "application/json", strings.NewReader(`{"spectate":true}`))
	if err != nil {
		t.Fatalf("launch request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("launch status: %d", resp.StatusCode)
	}
	if !called || !gotSpectate {
		t.Fatalf("launcher not called with spectate=true (called=%v spectate=%v)", called, gotSpectate)
	}
}

// TestSpawnRejectsBadCount 验证参数校验。
func TestSpawnRejectsBadCount(t *testing.T) {
	apiSrv := NewServer("127.0.0.1:1", func() []PlayerPos { return nil }, func() Metrics { return Metrics{} })
	ts := httptest.NewServer(apiSrv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/spawn", "application/json",
		strings.NewReader(`{"count":0,"x":1,"y":1}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for count=0, got %d", resp.StatusCode)
	}
}
