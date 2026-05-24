package aoi

type Cell struct {
	ID      int
	players map[int64]struct{}
}

func newCell(id int) *Cell {
	return &Cell{
		ID:      id,
		players: make(map[int64]struct{}),
	}
}

func (c *Cell) add(playerID int64) {
	c.players[playerID] = struct{}{}
}
func (c *Cell) remove(playerID int64) {
	delete(c.players, playerID)
}

type Manager struct {
	minX, minY, cellSize int
	cntX, cntY           int
	cells                map[int]*Cell
}

func NewManager(minX, minY, maxX, maxY, cellSize int) *Manager {
	// 向上取整:即使地图边长不能被格子大小整除,格子也能完整覆盖整张地图
	// (最后一格略微超出 maxX/maxY)。配合 gridID 里的 clamp,任意尺寸都安全。
	cntX := (maxX - minX + cellSize - 1) / cellSize
	cntY := (maxY - minY + cellSize - 1) / cellSize
	m := &Manager{
		minX:     minX,
		minY:     minY,
		cellSize: cellSize,
		cntX:     cntX,
		cntY:     cntY,
		cells:    make(map[int]*Cell),
	}
	for i := 0; i < cntX*cntY; i++ {
		m.cells[i] = newCell(i)
	}
	return m
}
func (m *Manager) gridID(x, y float32) int {
	col := clamp((int(x)-m.minX)/m.cellSize, 0, m.cntX-1)
	row := clamp((int(y)-m.minY)/m.cellSize, 0, m.cntY-1)
	return row*m.cntX + col
}
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (m *Manager) surroundCellIDs(gridID int) []int {
	col := gridID % m.cntX
	row := gridID / m.cntX
	ids := make([]int, 0, 9)
	for r := row - 1; r <= row+1; r++ {
		if r < 0 || r >= m.cntY {
			continue
		}
		for c := col - 1; c <= col+1; c++ {
			if c < 0 || c >= m.cntX {
				continue
			}
			ids = append(ids, r*m.cntX+c)
		}
	}
	return ids
}
func (m *Manager) playersAround(x, y float32) map[int64]struct{} {
	result := make(map[int64]struct{})
	for _, id := range m.surroundCellIDs(m.gridID(x, y)) {
		for pid := range m.cells[id].players {
			result[pid] = struct{}{}
		}
	}
	return result
}
func (m *Manager) ViewPlayers(x, y float32) []int64 {
	ids := make([]int64, 0)
	for pid := range m.playersAround(x, y) {
		ids = append(ids, pid)
	}
	return ids
}
func (m *Manager) Enter(playerID int64, x, y float32) []int64 {
	inView := make([]int64, 0)
	for pid := range m.playersAround(x, y) {
		inView = append(inView, pid)
	}
	m.cells[m.gridID(x, y)].add(playerID)
	return inView
}
func (m *Manager) Leave(playerID int64, x, y float32) []int64 {
	m.cells[m.gridID(x, y)].remove(playerID)
	wasInView := make([]int64, 0)
	for pid := range m.playersAround(x, y) {
		wasInView = append(wasInView, pid)
	}
	return wasInView
}
func (m *Manager) Move(playerID int64, oldX, oldY, newX, newY float32) (entered, left []int64) {
	oldGrid := m.gridID(oldX, oldY)
	newGrid := m.gridID(newX, newY)
	if oldGrid == newGrid {
		return nil, nil
	}
	oldView := m.playersAround(oldX, oldY)
	delete(oldView, playerID) // 自己不算
	m.cells[oldGrid].remove(playerID)
	m.cells[newGrid].add(playerID)
	newView := m.playersAround(newX, newY)
	delete(newView, playerID)
	// entered = newView - oldView(新视野里有、旧视野里没有 → 新出现)
	for pid := range newView {
		if _, ok := oldView[pid]; !ok {
			entered = append(entered, pid)
		}
	}
	// left = oldView - newView(旧视野里有、新视野里没有 → 消失了)
	for pid := range oldView {
		if _, ok := newView[pid]; !ok {
			left = append(left, pid)
		}
	}
	return entered, left
}
