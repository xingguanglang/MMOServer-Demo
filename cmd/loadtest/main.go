// Command loadtest spawns many virtual players that log in and random-walk,
// to stress the server and measure rough throughput.
//
//	go run ./cmd/loadtest -n 1000 -duration 30s
package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xingguanglang/MMOServer-Demo/internal/client"
)

const mapSize = 256

func main() {
	addr := flag.String("addr", "127.0.0.1:9000", "server address")
	n := flag.Int("n", 500, "number of virtual players")
	duration := flag.Duration("duration", 30*time.Second, "test duration")
	moveInterval := flag.Duration("move", 100*time.Millisecond, "per-bot move interval")
	flag.Parse()

	var (
		connected atomic.Int64
		failed    atomic.Int64
		wg        sync.WaitGroup
		mu        sync.Mutex
		clients   = make([]*client.Client, 0, *n)
	)
	stop := make(chan struct{})

	log.Printf("spawning %d bots against %s ...", *n, *addr)
	for i := 0; i < *n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			c, err := client.Dial(*addr)
			if err != nil {
				failed.Add(1)
				return
			}
			if err := c.Login(fmt.Sprintf("bot-%d", id)); err != nil {
				failed.Add(1)
				c.Close()
				return
			}
			connected.Add(1)
			mu.Lock()
			clients = append(clients, c)
			mu.Unlock()
			go c.Run()

			// 随机出生 + 随机游走。
			x := float32(rand.Intn(mapSize))
			y := float32(rand.Intn(mapSize))
			c.Move(x, y)

			ticker := time.NewTicker(*moveInterval)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					c.Close()
					return
				case <-ticker.C:
					x = clampf(x+float32(rand.Intn(21)-10), 0, mapSize)
					y = clampf(y+float32(rand.Intn(21)-10), 0, mapSize)
					if err := c.Move(x, y); err != nil {
						c.Close()
						return
					}
				}
			}
		}(i)
		time.Sleep(2 * time.Millisecond) // 错开连接,避免瞬时风暴
	}

	// 每秒打印一次统计:在线数、失败数、累计收包、约每秒收包(吞吐)。
	statsTicker := time.NewTicker(1 * time.Second)
	defer statsTicker.Stop()
	deadline := time.After(*duration)
	var lastRecv, lastBytes uint64
	for {
		select {
		case <-deadline:
			close(stop)
			wg.Wait()
			log.Printf("done")
			return
		case <-statsTicker.C:
			var totalRecv, totalBytes uint64
			mu.Lock()
			for _, c := range clients {
				totalRecv += c.ReceivedCount()
				totalBytes += c.ReceivedBytes()
			}
			mu.Unlock()
			mbps := float64(totalBytes-lastBytes) / (1024 * 1024)
			log.Printf("connected=%d failed=%d recv/s≈%d down≈%.2f MB/s",
				connected.Load(), failed.Load(), totalRecv-lastRecv, mbps)
			lastRecv = totalRecv
			lastBytes = totalBytes
		}
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
