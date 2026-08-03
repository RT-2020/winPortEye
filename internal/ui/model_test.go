package ui

import (
	"testing"

	"win/internal/core"

	"github.com/lxn/walk"
)

// ===== PortModel（子表）排序测试 =====

func TestPortModelSortAscending(t *testing.T) {
	m := NewPortModel()
	m.SetConns([]core.Connection{
		{Protocol: "tcp", LocalPort: 8080, State: "ESTABLISHED"},
		{Protocol: "tcp", LocalPort: 80, State: "LISTEN"},
		{Protocol: "udp", LocalPort: 53, State: "NONE"},
	})
	if err := m.Sort(2, walk.SortAscending); err != nil { // 端口列升序
		t.Fatal(err)
	}
	want := []uint16{53, 80, 8080}
	for i, w := range want {
		if got := m.conns[i].LocalPort; got != w {
			t.Errorf("row %d: want port %d, got %d", i, w, got)
		}
	}
}

func TestPortModelSortDescending(t *testing.T) {
	m := NewPortModel()
	m.SetConns([]core.Connection{
		{LocalPort: 8080}, {LocalPort: 80}, {LocalPort: 53},
	})
	if err := m.Sort(2, walk.SortDescending); err != nil {
		t.Fatal(err)
	}
	want := []uint16{8080, 80, 53}
	for i, w := range want {
		if got := m.conns[i].LocalPort; got != w {
			t.Errorf("row %d: want port %d, got %d", i, w, got)
		}
	}
}

func TestPortModelColumnSortable(t *testing.T) {
	m := NewPortModel()
	for col := 0; col <= 4; col++ {
		if !m.ColumnSortable(col) {
			t.Errorf("col %d 应可排序", col)
		}
	}
	if m.ColumnSortable(5) {
		t.Error("col 5 不应可排序（只有 0-4 列）")
	}
}

// TestPortModelSetConnsPreservesSort 验证替换数据后排序自动维持。
func TestPortModelSetConnsPreservesSort(t *testing.T) {
	m := NewPortModel()
	m.SetConns([]core.Connection{{LocalPort: 8080}, {LocalPort: 80}, {LocalPort: 53}})
	m.Sort(2, walk.SortAscending)
	if m.conns[0].LocalPort != 53 {
		t.Fatalf("排序后首行应为 53，实为 %d", m.conns[0].LocalPort)
	}
	// 替换数据（顺序打乱），排序应自动重新应用
	m.SetConns([]core.Connection{{LocalPort: 3306}, {LocalPort: 21}, {LocalPort: 8080}, {LocalPort: 443}})
	want := []uint16{21, 443, 3306, 8080}
	for i, w := range want {
		if got := m.conns[i].LocalPort; got != w {
			t.Errorf("刷新后排序丢失 row %d: want %d, got %d", i, w, got)
		}
	}
}

// ===== ProcessGroupModel（主表）聚合测试 =====

func TestGroupModelAggregate(t *testing.T) {
	m := NewProcessGroupModel()
	m.SetRaw([]core.Connection{
		{Pid: 100, LocalPort: 8080, ProcessName: "node", ProcessPath: "C:\\node.exe"},
		{Pid: 100, LocalPort: 8081, ProcessName: "node", ProcessPath: "C:\\node.exe"},
		{Pid: 4, LocalPort: 80, ProcessName: "System", ProcessPath: ""},
		{Pid: 100, LocalPort: 8082, ProcessName: "node", ProcessPath: "C:\\node.exe"},
	})
	// 应聚合成 2 行：PID 4（1 端口）、PID 100（3 端口）
	if got := len(m.rows); got != 2 {
		t.Fatalf("应聚合为 2 行，实为 %d", got)
	}
	// rows 按 PID 升序
	if m.rows[0].Pid != 4 || m.rows[1].Pid != 100 {
		t.Errorf("聚合行顺序错误: %d, %d", m.rows[0].Pid, m.rows[1].Pid)
	}
	// PID 100 应有 3 个端口
	row100 := m.rows[1]
	if row100.PortCount != 3 {
		t.Errorf("PID 100 端口数应为 3，实为 %d", row100.PortCount)
	}
	if row100.ProcessName != "node" {
		t.Errorf("PID 100 进程名应为 node，实为 %q", row100.ProcessName)
	}
}

func TestGroupModelConnsOf(t *testing.T) {
	m := NewProcessGroupModel()
	m.SetRaw([]core.Connection{
		{Pid: 100, LocalPort: 8080},
		{Pid: 100, LocalPort: 8081},
		{Pid: 200, LocalPort: 3000},
	})
	conns := m.ConnsOf(100)
	if len(conns) != 2 {
		t.Fatalf("PID 100 应有 2 个连接，实为 %d", len(conns))
	}
	if m.ConnsOf(999) != nil {
		t.Error("不存在的 PID 应返回 nil")
	}
}

func TestGroupModelIndexOfPid(t *testing.T) {
	m := NewProcessGroupModel()
	m.SetRaw([]core.Connection{
		{Pid: 100, LocalPort: 8080},
		{Pid: 4, LocalPort: 80},
		{Pid: 200, LocalPort: 3000},
	})
	// view 默认按 PID 升序：4, 100, 200
	m.SetKeyword("") // 触发 rebuildView
	if idx := m.IndexOfPid(100); idx != 1 {
		t.Errorf("PID 100 应在 index 1，实为 %d", idx)
	}
	if idx := m.IndexOfPid(999); idx != -1 {
		t.Errorf("不存在的 PID 应返回 -1，实为 %d", idx)
	}
}

func TestGroupModelKeywordFilter(t *testing.T) {
	m := NewProcessGroupModel()
	m.SetRaw([]core.Connection{
		{Pid: 100, LocalPort: 8080, ProcessName: "node"},
		{Pid: 200, LocalPort: 3000, ProcessName: "chrome"},
	})
	// 搜 "node" → 只剩 PID 100
	m.SetKeyword("node")
	if len(m.view) != 1 {
		t.Fatalf("过滤后应剩 1 行，实为 %d", len(m.view))
	}
	if m.view[0].Pid != 100 {
		t.Errorf("应命中 PID 100，实为 %d", m.view[0].Pid)
	}
	// 搜端口 "8080" → 通过端口摘要命中 PID 100
	m.SetKeyword("8080")
	if len(m.view) != 1 || m.view[0].Pid != 100 {
		t.Errorf("按端口摘要过滤应命中 PID 100，实为 %v", m.view)
	}
	// 搜 PID "200" → 命中 PID 200
	m.SetKeyword("200")
	if len(m.view) != 1 || m.view[0].Pid != 200 {
		t.Errorf("按 PID 过滤应命中 PID 200，实为 %v", m.view)
	}
}

// TestGroupModelSortByPortCount 验证按端口数排序（数字列）。
func TestGroupModelSortByPortCount(t *testing.T) {
	m := NewProcessGroupModel()
	m.SetRaw([]core.Connection{
		{Pid: 1, LocalPort: 80},                                          // 1 端口
		{Pid: 2, LocalPort: 80}, {Pid: 2, LocalPort: 81}, {Pid: 2, LocalPort: 82}, // 3 端口
		{Pid: 3, LocalPort: 80}, {Pid: 3, LocalPort: 81},                 // 2 端口
	})
	m.Sort(colGCount, walk.SortDescending) // 端口数降序

	type wantRow struct {
		pid       int32
		portCount int
	}
	want := []wantRow{{2, 3}, {3, 2}, {1, 1}}
	for i, w := range want {
		if m.view[i].Pid != w.pid {
			t.Errorf("row %d: want PID %d, got PID %d", i, w.pid, m.view[i].Pid)
		}
		if m.view[i].PortCount != w.portCount {
			t.Errorf("row %d: want %d 端口, got %d", i, w.portCount, m.view[i].PortCount)
		}
	}
}

// ===== buildPortSummary 测试 =====

func TestBuildPortSummary(t *testing.T) {
	cases := []struct {
		name string
		conns []core.Connection
		want  string
	}{
		{"单端口", []core.Connection{{LocalPort: 80}}, "80"},
		{"连续段", []core.Connection{{LocalPort: 80}, {LocalPort: 81}, {LocalPort: 82}}, "80-82"},
		{"多段", []core.Connection{{LocalPort: 80}, {LocalPort: 81}, {LocalPort: 443}, {LocalPort: 8080}, {LocalPort: 8081}}, "80-81, 443, 8080-8081"},
		{"去重", []core.Connection{{LocalPort: 80}, {LocalPort: 80}, {LocalPort: 81}}, "80-81"},
		{"乱序", []core.Connection{{LocalPort: 8081}, {LocalPort: 80}, {LocalPort: 8080}}, "80, 8080-8081"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildPortSummary(tc.conns)
			if got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestBuildPortSummaryTruncation(t *testing.T) {
	// 生成 20 个端口，超过 maxShow(8)，应截断
	conns := make([]core.Connection, 20)
	for i := range conns {
		conns[i].LocalPort = uint16(100 + i)
	}
	got := buildPortSummary(conns)
	if got == "" {
		t.Fatal("摘要不应为空")
	}
	// 应包含 "共 20 个" 收尾
	want := "... 共 20 个"
	if len(got) < len(want) || got[len(got)-len(want):] != want {
		t.Errorf("超长摘要应以 %q 结尾，实为 %q", want, got)
	}
}
