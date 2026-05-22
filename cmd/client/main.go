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

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/xingguanglang/MMOServer-Demo/internal/client"
)

const (
	mapSize    = 256                  // 世界尺寸(单位)
	cellSize   = 32                   // AOI 格子边长,仅用于画网格
	scale      = 2                    // 1 世界单位 = 2 像素
	screenSize = mapSize * scale      // 窗口边长(像素)
	tps        = 60.0                 // ebiten 默认每秒 60 帧
	moveSpeed  = 90.0                 // 移动速度(世界单位/秒)
	lerpAlpha  = 0.2                  // 插值系数:每帧朝目标靠近的比例
)

// renderPlayer 是某个远程玩家的渲染状态:rx,ry 是当前画在屏幕上的位置,
// 每帧朝网络给的目标位置插值靠近。
type renderPlayer struct {
	rx, ry float32
}

// Game 是 ebiten 的游戏对象:持有网络客户端 + 本地渲染状态。
type Game struct {
	c        *client.Client
	rendered map[int64]*renderPlayer
	selfX    float32
	selfY    float32
}

// Update 每帧(60fps)调用:处理键盘输入、上报移动、把远程玩家朝目标插值。
func (g *Game) Update() error {
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

	// 把远程玩家朝服务器给的目标位置插值,得到平滑移动。
	targets := g.c.Players()
	seen := make(map[int64]bool, len(targets))
	for _, p := range targets {
		seen[p.ID] = true
		rp := g.rendered[p.ID]
		if rp == nil {
			g.rendered[p.ID] = &renderPlayer{rx: p.X, ry: p.Y} // 新出现:直接落到目标位置
			continue
		}
		rp.rx += (p.X - rp.rx) * lerpAlpha
		rp.ry += (p.Y - rp.ry) * lerpAlpha
	}
	// 清掉已离开视野的玩家。
	for id := range g.rendered {
		if !seen[id] {
			delete(g.rendered, id)
		}
	}
	return nil
}

// Draw 每帧渲染。
func (g *Game) Draw(screen *ebiten.Image) {
	screen.Fill(color.RGBA{20, 20, 30, 255})
	drawGrid(screen)

	// 远程玩家:灰白色。
	for _, rp := range g.rendered {
		drawPlayer(screen, rp.rx, rp.ry, color.RGBA{210, 210, 210, 255})
	}
	// 自己:绿色。
	drawPlayer(screen, g.selfX, g.selfY, color.RGBA{90, 220, 90, 255})

	ebitenutil.DebugPrint(screen, fmt.Sprintf(
		"id=%d  pos=(%.0f,%.0f)  others in view=%d\nWASD / arrows to move",
		g.c.PlayerID(), g.selfX, g.selfY, len(g.rendered)))
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
	flag.Parse()

	c, err := client.Dial(*addr)
	if err != nil {
		log.Fatalf("connect failed: %v", err)
	}
	if err := c.Login(*name); err != nil {
		log.Fatalf("login failed: %v", err)
	}
	go func() {
		if err := c.Run(); err != nil {
			log.Printf("disconnected: %v", err)
		}
	}()

	// 随机一个出生点并上报,避免多个客户端都堆在原点。
	startX := float32(32 + rand.Intn(mapSize-64))
	startY := float32(32 + rand.Intn(mapSize-64))
	c.Move(startX, startY)

	g := &Game{
		c:        c,
		rendered: make(map[int64]*renderPlayer),
		selfX:    startX,
		selfY:    startY,
	}
	ebiten.SetWindowSize(screenSize, screenSize)
	ebiten.SetWindowTitle("MMOServer-Demo - " + *name)
	if err := ebiten.RunGame(g); err != nil {
		log.Fatal(err)
	}
}
