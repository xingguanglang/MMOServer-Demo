package aoi

import "testing"

// 测试统一用一张 256x256、格子边长 32 的地图 → 8x8 共 64 格。
// gridID = row*8 + col,其中 col = x/32,row = y/32。
func newTestManager() *Manager {
	return NewManager(0, 0, 256, 256, 32)
}

// sameSet 断言 got 和 want 是同一个集合(忽略顺序,因为 map 遍历顺序随机)。
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

// TestGridID 锁定坐标→格子编号的换算。
func TestGridID(t *testing.T) {
	m := newTestManager()
	cases := []struct {
		x, y float32
		want int
	}{
		{0, 0, 0},      // col0,row0
		{40, 40, 9},    // col1,row1 → 1*8+1
		{10, 70, 16},   // col0,row2 → 2*8+0
		{255, 255, 63}, // col7,row7 → 7*8+7
		{300, 300, 63}, // 越界,夹到右上角 (7,7)
	}
	for _, c := range cases {
		if got := m.gridID(c.x, c.y); got != c.want {
			t.Errorf("gridID(%v,%v)=%d, want %d", c.x, c.y, got, c.want)
		}
	}
}

// TestEnterSeesExistingNearbyPlayers 验证:进场时能看到附近已有玩家,看不到远处的。
func TestEnterSeesExistingNearbyPlayers(t *testing.T) {
	m := newTestManager()

	// 玩家 1 先进场 (10,10),四下无人。
	sameSet(t, "p1 enter", m.Enter(1, 10, 10))

	// 玩家 2 进同一格 (20,20),应看到玩家 1。
	sameSet(t, "p2 enter", m.Enter(2, 20, 20), 1)

	// 玩家 3 进远处 (200,200),看不到 1 和 2。
	sameSet(t, "p3 enter", m.Enter(3, 200, 200))

	// 此刻 (10,10) 视野里应是 1、2;远处的 3 不在。
	sameSet(t, "view@(10,10)", m.ViewPlayers(10, 10), 1, 2)
}

// TestMoveSameCellNoChange 验证:同格子内移动,视野不变。
func TestMoveSameCellNoChange(t *testing.T) {
	m := newTestManager()
	m.Enter(1, 10, 10)
	m.Enter(2, 20, 20)

	entered, left := m.Move(1, 10, 10, 15, 15) // 仍在 grid0
	if entered != nil || left != nil {
		t.Errorf("同格移动应无视野变化, got entered=%v left=%v", entered, left)
	}
}

// TestMoveAcrossCellsEnterAndLeave 验证:跨格移动产生正确的进/出视野事件。
func TestMoveAcrossCellsEnterAndLeave(t *testing.T) {
	m := newTestManager()
	m.Enter(1, 10, 10)  // A: grid0 (col0,row0)
	m.Enter(2, 10, 100) // B: grid24 (col0,row3)
	// 初始 A 在 grid0,视野 9 格不含 grid24,看不见 B。
	// 注意 ViewPlayers 按坐标查,会含站在那里的 A 自己(id=1),排除自己是调用方的责任,
	// 所以这里期望只有 A、不含 B,即证明 B 不在视野内。
	sameSet(t, "A 初始视野", m.ViewPlayers(10, 10), 1)

	// A 上移到 (10,70) → grid16 (col0,row2),它的 9 格含 grid24,B 进入视野。
	entered, left := m.Move(1, 10, 10, 10, 70)
	sameSet(t, "A 前进-entered", entered, 2)
	sameSet(t, "A 前进-left", left)

	// A 再退回 (10,10) → grid0,B 离开视野。
	entered, left = m.Move(1, 10, 70, 10, 10)
	sameSet(t, "A 后退-entered", entered)
	sameSet(t, "A 后退-left", left, 2)
}

// TestLeaveReturnsViewers 验证:离场时返回原本能看到它的玩家。
func TestLeaveReturnsViewers(t *testing.T) {
	m := newTestManager()
	m.Enter(1, 10, 10)
	m.Enter(2, 20, 20)

	// 玩家 1 离场,原本能看到它的应是玩家 2。
	sameSet(t, "p1 leave", m.Leave(1, 10, 10), 2)

	// 离场后 (10,10) 视野里只剩玩家 2。
	sameSet(t, "view after leave", m.ViewPlayers(10, 10), 2)
}
