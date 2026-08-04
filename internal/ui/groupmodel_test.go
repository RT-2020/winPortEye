package ui

import (
	"testing"

	"win/internal/core"
)

// rowsPids 是测试辅助：返回当前 view 的 PID 序列，便于断言。
func rowsPids(m *ProcessGroupModel) []int32 {
	out := make([]int32, 0, len(m.view))
	for _, r := range m.view {
		out = append(out, r.Pid)
	}
	return out
}

func assertPids(t *testing.T, m *ProcessGroupModel, want ...int32) {
	t.Helper()
	got := rowsPids(m)
	if len(got) != len(want) {
		t.Fatalf("PID 数量不符: want %v, got %v", want, got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("PID 序列不符: want %v, got %v", want, got)
		}
	}
}

// TestDiffFirstLoad 首次加载（旧 view 空）应直接 reset，view 正确。
func TestDiffFirstLoad(t *testing.T) {
	m := NewProcessGroupModel()
	m.SetRaw([]core.Connection{
		{Pid: 100, LocalPort: 80},
		{Pid: 4, LocalPort: 443},
	})
	// 默认按 PID 升序：4, 100
	assertPids(t, m, 4, 100)
}

// TestDiffPureInsert 旧数据全部保留 + 新增，diff 后应包含全部，顺序正确。
func TestDiffPureInsert(t *testing.T) {
	m := NewProcessGroupModel()
	m.SetRaw([]core.Connection{
		{Pid: 100, LocalPort: 80},
	})
	m.SetRaw([]core.Connection{
		{Pid: 100, LocalPort: 80},
		{Pid: 200, LocalPort: 3000},
	})
	assertPids(t, m, 100, 200)
}

// TestDiffPureRemove 旧数据部分消失，diff 后应只剩保留的。
func TestDiffPureRemove(t *testing.T) {
	m := NewProcessGroupModel()
	m.SetRaw([]core.Connection{
		{Pid: 100, LocalPort: 80},
		{Pid: 200, LocalPort: 3000},
		{Pid: 300, LocalPort: 8080},
	})
	m.SetRaw([]core.Connection{
		{Pid: 200, LocalPort: 3000},
	})
	assertPids(t, m, 200)
}

// TestDiffMixedInsertRemove 同时增删，验证双指针 diff 不串位。
func TestDiffMixedInsertRemove(t *testing.T) {
	m := NewProcessGroupModel()
	m.SetRaw([]core.Connection{
		{Pid: 100, LocalPort: 80},
		{Pid: 200, LocalPort: 3000},
		{Pid: 300, LocalPort: 8080},
	})
	// 删 200，加 400，保留 100/300
	m.SetRaw([]core.Connection{
		{Pid: 100, LocalPort: 80},
		{Pid: 300, LocalPort: 8080},
		{Pid: 400, LocalPort: 9000},
	})
	assertPids(t, m, 100, 300, 400)
}

// TestDiffPortCountChange 同一 PID 端口数变化，应反映在 PortCount。
func TestDiffPortCountChange(t *testing.T) {
	m := NewProcessGroupModel()
	m.SetRaw([]core.Connection{
		{Pid: 100, LocalPort: 80},
	})
	if m.rows[0].PortCount != 1 {
		t.Fatalf("初始端口数应为 1，实为 %d", m.rows[0].PortCount)
	}
	m.SetRaw([]core.Connection{
		{Pid: 100, LocalPort: 80},
		{Pid: 100, LocalPort: 81},
		{Pid: 100, LocalPort: 82},
	})
	// 找到 PID 100 的行
	g, idx := m.findByPid(100)
	if idx < 0 {
		t.Fatal("PID 100 应仍存在")
	}
	if g.PortCount != 3 {
		t.Errorf("端口数应变为 3，实为 %d", g.PortCount)
	}
}

// TestDiffIndexOfPidAfterChange 多轮刷新后 IndexOfPid 仍能正确找回。
func TestDiffIndexOfPidAfterChange(t *testing.T) {
	m := NewProcessGroupModel()
	m.SetRaw([]core.Connection{
		{Pid: 1, LocalPort: 80}, {Pid: 2, LocalPort: 80}, {Pid: 3, LocalPort: 80},
	})
	if idx := m.IndexOfPid(2); idx != 1 {
		t.Errorf("初始 PID 2 应在 index 1，实为 %d", idx)
	}
	// 删掉 PID 1，PID 2 位移到 0
	m.SetRaw([]core.Connection{
		{Pid: 2, LocalPort: 80}, {Pid: 3, LocalPort: 80},
	})
	if idx := m.IndexOfPid(2); idx != 0 {
		t.Errorf("删 1 后 PID 2 应在 index 0，实为 %d", idx)
	}
}

// TestDiffAccessDenied 拿不到名/路径的进程应标记 AccessDenied。
func TestDiffAccessDenied(t *testing.T) {
	m := NewProcessGroupModel()
	m.SetRaw([]core.Connection{
		{Pid: 100, LocalPort: 80, ProcessName: "node", ProcessPath: "C:\\node.exe"},
		{Pid: 4, LocalPort: 443, ProcessName: "", ProcessPath: ""},
	})
	g4, idx := m.findByPid(4)
	if idx < 0 {
		t.Fatal("PID 4 应存在")
	}
	if !g4.AccessDenied {
		t.Error("PID 4 无名无路径，应标记 AccessDenied")
	}
	g100, _ := m.findByPid(100)
	if g100.AccessDenied {
		t.Error("PID 100 有名有路径，不应标记 AccessDenied")
	}
}

// TestDiffConnsOfAfterRemove 删除进程后 ConnsOf 应返回 nil。
func TestDiffConnsOfAfterRemove(t *testing.T) {
	m := NewProcessGroupModel()
	m.SetRaw([]core.Connection{
		{Pid: 100, LocalPort: 80},
		{Pid: 100, LocalPort: 81},
	})
	if len(m.ConnsOf(100)) != 2 {
		t.Fatal("PID 100 应有 2 个连接")
	}
	m.SetRaw([]core.Connection{
		{Pid: 200, LocalPort: 80},
	})
	if m.ConnsOf(100) != nil {
		t.Error("PID 100 已消失，ConnsOf 应返回 nil")
	}
}

// TestRetainedPidIndexStable 验证防抖的核心不变量：
// 默认按 PID 排序时，只要某 PID 还在数据里，多轮刷新后它的 view 索引必须保持不变。
// 这是「保留行只发 RowChanged、行数不变、不触发 ENSUREVISIBLE」的数学前提。
// 若此不变量被破坏，说明 diff 把保留行误判成 remove+insert，会导致滚动弹回。
func TestRetainedPidIndexStable(t *testing.T) {
	m := NewProcessGroupModel()
	// 初始：5 个进程，PID 10/20/30/40/50
	m.SetRaw([]core.Connection{
		{Pid: 10, LocalPort: 1000}, {Pid: 20, LocalPort: 2000},
		{Pid: 30, LocalPort: 3000}, {Pid: 40, LocalPort: 4000},
		{Pid: 50, LocalPort: 5000},
	})
	idx30Before := m.IndexOfPid(30)
	if idx30Before != 2 {
		t.Fatalf("初始 PID 30 应在 index 2，实为 %d", idx30Before)
	}

	// 模拟 watcher 刷新：PID 30 端口数变化（内容变但 PID 在），其他 PID 端口也有增减
	// 但 10/30/50 三个 PID 保留，20/40 消失，新增 60
	m.SetRaw([]core.Connection{
		{Pid: 10, LocalPort: 1000},
		{Pid: 30, LocalPort: 3000}, {Pid: 30, LocalPort: 3001}, // 30 端口变多
		{Pid: 50, LocalPort: 5000},
		{Pid: 60, LocalPort: 6000},
	})
	// 按 PID 升序：10,30,50,60 → 30 在 index 1（因前序 20 删除而前移，这是正确的）
	if idx := m.IndexOfPid(30); idx != 1 {
		t.Errorf("删 20 后 PID 30 应在 index 1，实为 %d", idx)
	}

	// 更强的断言：连续多轮刷新，保留 PID 的索引应【单调一致】（按 PID 升序）
	for round := 0; round < 5; round++ {
		m.SetRaw([]core.Connection{
			{Pid: 10, LocalPort: 1000},
			{Pid: 30, LocalPort: uint16(3000 + round)}, // 端口每轮变
			{Pid: 50, LocalPort: 5000},
			{Pid: 60, LocalPort: 6000},
		})
	}
	// 5 轮后，PID 序列应稳定为 10,30,50,60
	assertPids(t, m, 10, 30, 50, 60)
	// PID 30 的 PortCount 在某轮应该=1（最后轮），且行号稳定
	g, idx := m.findByPid(30)
	if idx != 1 {
		t.Errorf("多轮刷新后 PID 30 应稳定在 index 1，实为 %d", idx)
	}
	if g.PortCount != 1 {
		t.Errorf("最后轮 PID 30 应 1 端口，实为 %d", g.PortCount)
	}
}

// TestRetainedPidIndexStableWithContentChange 验证：保留 PID 仅内容变时，
// view 的 PID 集合与顺序完全不变（只有行的字段值变）。这是 RowChanged 的前提。
func TestRetainedPidIndexStableWithContentChange(t *testing.T) {
	m := NewProcessGroupModel()
	m.SetRaw([]core.Connection{
		{Pid: 100, LocalPort: 80, ProcessName: "v1"},
		{Pid: 200, LocalPort: 443, ProcessName: "svc"},
	})
	pidsBefore := rowsPids(m)

	// 仅改 PID 100 的端口数（内容变），PID 集合不变
	m.SetRaw([]core.Connection{
		{Pid: 100, LocalPort: 80, ProcessName: "v1"},
		{Pid: 100, LocalPort: 81, ProcessName: "v1"},
		{Pid: 200, LocalPort: 443, ProcessName: "svc"},
	})
	pidsAfter := rowsPids(m)
	if len(pidsBefore) != len(pidsAfter) {
		t.Fatalf("PID 集合大小应不变: before %v after %v", pidsBefore, pidsAfter)
	}
	for i := range pidsBefore {
		if pidsBefore[i] != pidsAfter[i] {
			t.Fatalf("保留 PID 顺序应不变: before %v after %v", pidsBefore, pidsAfter)
		}
	}
	// 但 PID 100 的 PortCount 应该变成 2
	g, _ := m.findByPid(100)
	if g.PortCount != 2 {
		t.Errorf("PID 100 端口数应为 2，实为 %d", g.PortCount)
	}
}

// findByPid 在 view 中查找指定 PID，返回行和索引。
func (m *ProcessGroupModel) findByPid(pid int32) (ProcessGroupRow, int) {
	for i, r := range m.view {
		if r.Pid == pid {
			return r, i
		}
	}
	return ProcessGroupRow{}, -1
}
