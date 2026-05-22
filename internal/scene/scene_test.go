package scene

import (
	"sync"
	"testing"
	"time"

	"github.com/xingguanglang/MMOServer-Demo/internal/aoi"
)

// recordedMove 记录一次 BroadcastMove 调用的内容,供断言。
type recordedMove struct {
	viewers []int64
	moverID int64
	x, y    float32
}

// recordedView 记录一次进/出视野通知(x,y 仅对 enter 有意义)。
type recordedView struct {
	observerID, subjectID int64
	x, y                  float32
}

// fakeNotifier 是 Notifier 接口的测试替身:不发网络,只把每次调用记下来。
// 加锁是为了支持"用 Run() 起真 tick goroutine"的那个测试(两个 goroutine 会同时读写)。
type fakeNotifier struct {
	mu     sync.Mutex
	moves  []recordedMove
	enters []recordedView
	leaves []recordedView
}

func (f *fakeNotifier) BroadcastMove(viewerIDs []int64, moverID int64, x, y float32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	vs := append([]int64(nil), viewerIDs...) // 复制一份,避免外部切片被复用导致数据被改
	f.moves = append(f.moves, recordedMove{viewers: vs, moverID: moverID, x: x, y: y})
}

func (f *fakeNotifier) NotifyEnter(observerID, subjectID int64, x, y float32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enters = append(f.enters, recordedView{observerID: observerID, subjectID: subjectID, x: x, y: y})
}

func (f *fakeNotifier) NotifyLeave(observerID, subjectID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leaves = append(f.leaves, recordedView{observerID: observerID, subjectID: subjectID})
}

// hasView 判断 list 里是否存在某个 (observer, subject) 通知。
func hasView(list []recordedView, observerID, subjectID int64) bool {
	for _, v := range list {
		if v.observerID == observerID && v.subjectID == subjectID {
			return true
		}
	}
	return false
}

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.moves)
}

func (f *fakeNotifier) last() recordedMove {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.moves[len(f.moves)-1]
}

func newTestScene(fake *fakeNotifier) *Scene {
	mgr := aoi.NewManager(0, 0, 256, 256, 32) // 8x8 格
	return NewScene(mgr, fake, 30)
}

// sameSet 断言切片是同一个集合(忽略顺序)。
func sameSet(t *testing.T, name string, got []int64, want ...int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: 集合大小不符 got=%v want=%v", name, got, want)
	}
	set := make(map[int64]bool, len(got))
	for _, id := range got {
		set[id] = true
	}
	for _, id := range want {
		if !set[id] {
			t.Fatalf("%s: 缺少 %d, got=%v want=%v", name, id, got, want)
		}
	}
}

// TestMoveBroadcastsToViewersOnly:移动只广播给视野内的其他玩家,远处的和自己都排除。
func TestMoveBroadcastsToViewersOnly(t *testing.T) {
	fake := &fakeNotifier{}
	s := newTestScene(fake)

	// 三个玩家进场:1、2 在同一格附近,3 在远处。
	s.Join(1, 10, 10)
	s.Join(2, 20, 20)
	s.Join(3, 200, 200)
	s.tick() // 直接同步驱动一帧,处理上面三个 join

	// 玩家 1 移动一下。
	s.Move(1, 15, 15)
	s.tick()

	if fake.count() != 1 {
		t.Fatalf("期望 1 次广播, got %d", fake.count())
	}
	mv := fake.last()
	if mv.moverID != 1 {
		t.Errorf("moverID got %d want 1", mv.moverID)
	}
	if mv.x != 15 || mv.y != 15 {
		t.Errorf("coords got (%v,%v) want (15,15)", mv.x, mv.y)
	}
	// 视野内只该有玩家 2;玩家 3 太远、玩家 1 是自己,都不在。
	sameSet(t, "viewers", mv.viewers, 2)
}

// TestMoveUnknownPlayerIgnored:没进场过的玩家发来移动,应被忽略、不产生广播。
func TestMoveUnknownPlayerIgnored(t *testing.T) {
	fake := &fakeNotifier{}
	s := newTestScene(fake)

	s.Move(99, 1, 1) // 99 从没 Join 过
	s.tick()

	if fake.count() != 0 {
		t.Fatalf("未知玩家移动不应广播, got %d", fake.count())
	}
}

// TestLeaveRemovesPlayer:玩家离场后,它不再出现在别人的视野里。
func TestLeaveRemovesPlayer(t *testing.T) {
	fake := &fakeNotifier{}
	s := newTestScene(fake)

	s.Join(1, 10, 10)
	s.Join(2, 20, 20)
	s.Leave(2)
	s.tick()

	// 1 移动,此时 2 已离场,视野里没别人,广播对象为空。
	s.Move(1, 15, 15)
	s.tick()

	mv := fake.last()
	sameSet(t, "viewers after leave", mv.viewers)
}

// TestJoinNotifiesMutualEnter:玩家进场时,与视野内已有玩家互相收到"进入视野"。
func TestJoinNotifiesMutualEnter(t *testing.T) {
	fake := &fakeNotifier{}
	s := newTestScene(fake)

	s.Join(1, 10, 10)
	s.Join(2, 20, 20) // 与玩家 1 同格,互相在视野内
	s.tick()

	if !hasView(fake.enters, 1, 2) {
		t.Error("玩家 1 应收到 玩家 2 进入视野")
	}
	if !hasView(fake.enters, 2, 1) {
		t.Error("玩家 2 应收到 玩家 1 进入视野")
	}
}

// TestMoveEntersView:玩家移动跨格、把远处玩家纳入视野时,双方互相收到 enter。
func TestMoveEntersView(t *testing.T) {
	fake := &fakeNotifier{}
	s := newTestScene(fake)

	s.Join(1, 10, 10) // grid0
	s.Join(3, 10, 70) // grid16,初始不在 1 的视野
	s.tick()
	if hasView(fake.enters, 1, 3) || hasView(fake.enters, 3, 1) {
		t.Fatal("初始两人相距较远,不应有进入视野通知")
	}

	// 1 移到 grid8,grid16 进入其九宫格 → 3 进入视野。
	s.Move(1, 10, 50)
	s.tick()
	if !hasView(fake.enters, 1, 3) {
		t.Error("玩家 1 应收到 玩家 3 进入视野")
	}
	if !hasView(fake.enters, 3, 1) {
		t.Error("玩家 3 应收到 玩家 1 进入视野")
	}
}

// TestLeaveNotifiesViewers:玩家离场,原本能看到它的玩家收到"离开视野"。
func TestLeaveNotifiesViewers(t *testing.T) {
	fake := &fakeNotifier{}
	s := newTestScene(fake)

	s.Join(1, 10, 10)
	s.Join(2, 20, 20)
	s.tick()

	s.Leave(2)
	s.tick()

	if !hasView(fake.leaves, 1, 2) {
		t.Error("玩家 1 应收到 玩家 2 离开视野")
	}
}

// TestRunDrivesTick:用真正的 Run() 起 tick 主循环,验证定时器能驱动输入被处理。
func TestRunDrivesTick(t *testing.T) {
	fake := &fakeNotifier{}
	s := newTestScene(fake)

	go s.Run() // 真 tick 循环(测试结束随进程退出,这里的 goroutine 泄漏可接受)

	s.Join(1, 10, 10)
	s.Join(2, 20, 20)
	s.Move(1, 15, 15)

	// 轮询等待:30Hz 下一帧约 33ms,2 秒内必然处理完。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && fake.count() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if fake.count() == 0 {
		t.Fatal("tick 主循环未在超时内处理移动")
	}
}
