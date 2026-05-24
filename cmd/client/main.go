// Command client is an ebiten visualization client for the MMO demo.
// Move with WASD / arrow keys; other players in your AOI appear and move
// smoothly (10Hz server state interpolated to 60fps).
package main

import (
	"flag"
	"fmt"
	"image/color"
	"log"
	"math/rand"
	"sync/atomic"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/xingguanglang/MMOServer-Demo/internal/client"
	"github.com/xingguanglang/MMOServer-Demo/internal/config"
)

const (
	mapSize    = config.MapMaxX  // 世界尺寸(单位),与服务器一致
	cellSize   = config.CellSize // AOI 格子边长,仅用于画网格
	scale      = 2               // 1 世界单位 = 2 像素
	screenSize = mapSize * scale // 窗口边长(像素)
	tps        = 60.0            // ebiten 默认每秒 60 帧
	moveSpeed  = 90.0            // 移动速度(世界单位/秒)
	lerpAlpha  = 0.2             // 插值系数:每帧朝目标靠近的比例
)

// renderPlayer 是某个远程玩家的渲染状态:rx,ry 是当前画在屏幕上的位置,
// 每帧朝网络给的目标位置插值靠近;near 表示它当前在不在我的 AOI 视野内
//(在 → 高频更新、亮色;不在 → 只靠全局低频快照、暗色)。
type renderPlayer struct {
	rx, ry float32
	near   bool
}

// Game 是 ebiten 的游戏对象:持有网络客户端 + 本地渲染状态。
type Game struct {
	c            *client.Client
	rendered     map[int64]*renderPlayer
	selfX        float32
	selfY        float32
	spectate     bool        // 观战模式:画全部玩家,不画自己、不响应键盘
	disconnected atomic.Bool // 连接断开(如被服务器一键清空)→ 关闭窗口
}

// Update 每帧(60fps)调用:处理键盘输入、上报移动、把远程玩家朝目标插值。
func (g *Game) Update() error {
	if g.disconnected.Load() {
		return ebiten.Termination // 服务器断开了连接 → 优雅退出、关闭窗口
	}
	if !g.spectate {
		g.updateSelf()
	}
	g.interpolate()
	return nil
}

// updateSelf 读键盘移动自己,并上报服务器。
func (g *Game) updateSelf() {
	const dt = float32(1.0 / tps)

	moved := false
	if ebiten.IsKeyPressed(ebiten.KeyW) || ebiten.IsKeyPressed(ebiten.KeyArrowUp) {
		g.selfY -= moveSpeed * dt
		moved = true
	}
	if ebiten.IsKeyPressed(ebiten.KeyS) || ebiten.IsKeyPressed(ebiten.KeyArrowDown) {
		g.selfY += moveSpeed * dt
		moved = true
	}
	if ebiten.IsKeyPressed(ebiten.KeyA) || ebiten.IsKeyPressed(ebiten.KeyArrowLeft) {
		g.selfX -= moveSpeed * dt
		moved = true
	}
	if ebiten.IsKeyPressed(ebiten.KeyD) || ebiten.IsKeyPressed(ebiten.KeyArrowRight) {
		g.selfX += moveSpeed * dt
		moved = true
	}
	g.selfX = clampf(g.selfX, 0, mapSize)
	g.selfY = clampf(g.selfY, 0, mapSize)
	if moved {
		g.c.Move(g.selfX, g.selfY) // 上报自己的新位置
	}
}

// interpolate 维护全场所有玩家的渲染位置:
//   - 全局快照(MinimapSync,低频)打底,覆盖到所有人 → 远处玩家也显示;
//   - AOI 快照(StateSync,高频)覆盖近处玩家的目标,使其更新更快、标记为 near。
// 每帧把渲染位置朝目标插值,平滑移动。
func (g *Game) interpolate() {
	type target struct {
		x, y float32
		near bool
	}
	targets := make(map[int64]target)
	for _, p := range g.c.Minimap() { // 全局:所有人,低频
		targets[p.ID] = target{p.X, p.Y, false}
	}
	for _, p := range g.c.Players() { // AOI:近处,高频(覆盖)
		targets[p.ID] = target{p.X, p.Y, true}
	}

	selfID := g.c.PlayerID()
	for id, t := range targets {
		if id == selfID {
			continue // 自己单独画
		}
		rp := g.rendered[id]
		if rp == nil {
			g.rendered[id] = &renderPlayer{rx: t.x, ry: t.y, near: t.near}
			continue
		}
		rp.rx += (t.x - rp.rx) * lerpAlpha
		rp.ry += (t.y - rp.ry) * lerpAlpha
		rp.near = t.near
	}
	// 清掉离场的玩家(既不在 AOI 也不在全局快照里)。
	for id := range g.rendered {
		if _, ok := targets[id]; !ok || id == selfID {
			delete(g.rendered, id)
		}
	}
}

// Draw 每帧渲染:主地图显示全场所有人——近处(AOI,高频)亮色,远处(全局,低频)暗色。
func (g *Game) Draw(screen *ebiten.Image) {
	screen.Fill(color.RGBA{20, 20, 30, 255})
	drawGrid(screen)

	near := 0
	for _, rp := range g.rendered {
		clr := color.RGBA{110, 110, 130, 255} // 远处:暗灰(全局低频)
		if rp.near {
			clr = color.RGBA{230, 230, 230, 255} // 近处:亮白(AOI 高频)
			near++
		}
		drawPlayer(screen, rp.rx, rp.ry, clr)
	}

	if g.spectate {
		ebitenutil.DebugPrint(screen, fmt.Sprintf("SPECTATOR (god view)  players=%d", len(g.rendered)))
		return
	}
	// 自己:绿色。
	drawPlayer(screen, g.selfX, g.selfY, color.RGBA{90, 220, 90, 255})
	ebitenutil.DebugPrint(screen, fmt.Sprintf(
		"id=%d  pos=(%.0f,%.0f)  near(AOI)=%d  far(global)=%d\nWASD / arrows to move",
		g.c.PlayerID(), g.selfX, g.selfY, near, len(g.rendered)-near))
}

func (g *Game) Layout(int, int) (int, int) { return screenSize, screenSize }

// drawPlayer 把世界坐标换算成屏幕坐标,画一个实心圆。
func drawPlayer(screen *ebiten.Image, x, y float32, clr color.Color) {
	vector.DrawFilledCircle(screen, x*scale, y*scale, 5, clr, true)
}

// drawGrid 画出 AOI 格子边界,方便观察"走出格子时对方消失"。
func drawGrid(screen *ebiten.Image) {
	gridClr := color.RGBA{50, 50, 70, 255}
	for v := 0; v <= mapSize; v += cellSize {
		p := float32(v * scale)
		vector.StrokeLine(screen, p, 0, p, screenSize, 1, gridClr, false)
		vector.StrokeLine(screen, 0, p, screenSize, p, 1, gridClr, false)
	}
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

func main() {
	addr := flag.String("addr", "127.0.0.1:9000", "server address")
	name := flag.String("name", "player", "username")
	spectate := flag.Bool("spectate", false, "god-view spectator: see all players (no AOI)")
	flag.Parse()

	c, err := client.Dial(*addr)
	if err != nil {
		log.Fatalf("connect failed: %v", err)
	}

	g := &Game{
		c:        c,
		rendered: make(map[int64]*renderPlayer),
		spectate: *spectate,
	}

	if *spectate {
		if err := c.Spectate(); err != nil {
			log.Fatalf("spectate failed: %v", err)
		}
	} else {
		if err := c.Login(*name); err != nil {
			log.Fatalf("login failed: %v", err)
		}
		// 随机一个出生点并上报,避免多个客户端都堆在原点。
		g.selfX = float32(32 + rand.Intn(mapSize-64))
		g.selfY = float32(32 + rand.Intn(mapSize-64))
		c.Move(g.selfX, g.selfY)
	}

	go func() {
		if err := c.Run(); err != nil {
			log.Printf("disconnected: %v", err)
		}
		g.disconnected.Store(true) // 连接结束 → 让 Update 关闭窗口
	}()

	title := "MMOServer-Demo - " + *name
	if *spectate {
		title = "MMOServer-Demo - SPECTATOR"
	}
	ebiten.SetWindowSize(screenSize, screenSize)
	ebiten.SetWindowTitle(title)
	if err := ebiten.RunGame(g); err != nil {
		log.Fatal(err)
	}
}
