package scene

import (
	"sync"
	"testing"
	"time"

	"github.com/xingguanglang/MMOServer-Demo/internal/aoi"
)

// recordedSync 记录一次 SyncState 调用。
type recordedSync struct {
	observerID int64
	states     []PlayerState
}

// recordedView 记录一次进/出视野通知(x,y 仅对 enter 有意义)。
type recordedView struct {
	observerID, subjectID int64
	x, y                  float32
}

// fakeNotifier 是 Notifier 接口的测试替身:不发网络,只把每次调用记下来。
// 加锁是为了支持"用 Run() 起真 tick goroutine"的那个测试(两个 goroutine 同时读写)。
type fakeNotifier struct {
	mu     sync.Mutex
	syncs  []recordedSync
	enters []recordedView
	leaves []recordedView
}

func (f *fakeNotifier) SyncState(observerID int64, states []PlayerState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := append([]PlayerState(nil), states...) // 复制一份,避免外部切片被复用
	f.syncs = append(f.syncs, recordedSync{observerID: observerID, states: cp})
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

func (f *fakeNotifier) syncCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.syncs)
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

// syncFor 找到发给某个 observer 的状态同步。
func syncFor(syncs []recordedSync, observerID int64) (recordedSync, bool) {
	for _, s := range syncs {
		if s.observerID == observerID {
			return s, true
		}
	}
	return recordedSync{}, false
}

// hasState 判断快照里是否含某个玩家。
func hasState(states []PlayerState, id int64) bool {
	for _, st := range states {
		if st.ID == id {
			return true
		}
	}
	return false
}

func newTestScene(fake *fakeNotifier) *Scene {
	mgr := aoi.NewManager(0, 0, 256, 256, 32) // 8x8 格
	return NewScene(mgr, fake, 30, 10)        // 30Hz tick,10Hz 同步
}

// TestJoinNotifiesMutualEnter:玩家进场时,与视野内已有玩家互相收到"进入视野"。
func TestJoinNotifiesMutualEnter(t *testing.T) {
	fake := &fakeNotifier{}
	s := newTestScene(fake)

	s.Join(1, 10, 10)
	s.Join(2, 20, 20) // 与玩家 1 同格,互相在视野内
	s.drainInputs()

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
	s.drainInputs()
	if hasView(fake.enters, 1, 3) || hasView(fake.enters, 3, 1) {
		t.Fatal("初始两人相距较远,不应有进入视野通知")
	}

	// 1 移到 grid8,grid16 进入其九宫格 → 3 进入视野。
	s.Move(1, 10, 50)
	s.drainInputs()
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
	s.drainInputs()

	s.Leave(2)
	s.drainInputs()

	if !hasView(fake.leaves, 1, 2) {
		t.Error("玩家 1 应收到 玩家 2 离开视野")
	}
}

// TestStateSyncContainsViewers:状态同步只包含视野内的其他玩家(排除自己和远处的人)。
func TestStateSyncContainsViewers(t *testing.T) {
	fake := &fakeNotifier{}
	s := newTestScene(fake)

	s.Join(1, 10, 10)
	s.Join(2, 20, 20)   // 与 1 同格
	s.Join(3, 200, 200) // 远处
	s.drainInputs()
	s.broadcastState() // 直接触发一次状态广播

	sync1, ok := syncFor(fake.syncs, 1)
	if !ok {
		t.Fatal("玩家 1 应收到状态同步")
	}
	if !hasState(sync1.states, 2) {
		t.Error("玩家 1 的快照应含玩家 2")
	}
	if hasState(sync1.states, 3) {
		t.Error("玩家 1 的快照不应含远处的玩家 3")
	}
	if hasState(sync1.states, 1) {
		t.Error("快照不应含玩家自己")
	}

	// 玩家 3 视野内没别人,不该收到状态同步。
	if _, ok := syncFor(fake.syncs, 3); ok {
		t.Error("玩家 3 视野内无人,不该收到状态同步")
	}
}

// TestRunDrivesStateSync:用真正的 Run() 起 tick 主循环,验证定时器能驱动状态同步。
func TestRunDrivesStateSync(t *testing.T) {
	fake := &fakeNotifier{}
	s := newTestScene(fake)

	go s.Run() // 真 tick 循环(测试结束随进程退出,这里的 goroutine 泄漏可接受)

	s.Join(1, 10, 10)
	s.Join(2, 20, 20)

	// 10Hz 同步,一拍约 100ms,2 秒内必然产生状态同步。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && fake.syncCount() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if fake.syncCount() == 0 {
		t.Fatal("tick 主循环未在超时内产生状态同步")
	}
}
