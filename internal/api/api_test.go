package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
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
	})
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

// TestSpawnRejectsBadCount 验证参数校验。
func TestSpawnRejectsBadCount(t *testing.T) {
	apiSrv := NewServer("127.0.0.1:1", func() []PlayerPos { return nil })
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
