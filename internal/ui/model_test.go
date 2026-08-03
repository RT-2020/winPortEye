package ui

import (
	"testing"

	"win/internal/core"

	"github.com/lxn/walk"
)

func TestSortAscending(t *testing.T) {
	m := NewPortModel()
	m.SetRaw([]core.Connection{
		{Protocol: "tcp", LocalPort: 8080, State: "ESTABLISHED", ProcessName: "node"},
		{Protocol: "tcp", LocalPort: 80, State: "LISTEN", ProcessName: "System"},
		{Protocol: "udp", LocalPort: 53, State: "NONE", ProcessName: "dns"},
	})
	// 按端口列(2)升序
	if err := m.Sort(2, walk.SortAscending); err != nil {
		t.Fatal(err)
	}
	want := []uint16{53, 80, 8080}
	for i, w := range want {
		if got := m.view[i].LocalPort; got != w {
			t.Errorf("row %d: want port %d, got %d", i, w, got)
		}
	}
}

func TestSortDescending(t *testing.T) {
	m := NewPortModel()
	m.SetRaw([]core.Connection{
		{LocalPort: 8080, ProcessName: "node"},
		{LocalPort: 80, ProcessName: "System"},
		{LocalPort: 53, ProcessName: "dns"},
	})
	// 按端口列(2)降序
	if err := m.Sort(2, walk.SortDescending); err != nil {
		t.Fatal(err)
	}
	want := []uint16{8080, 80, 53}
	for i, w := range want {
		if got := m.view[i].LocalPort; got != w {
			t.Errorf("row %d: want port %d, got %d", i, w, got)
		}
	}
}

func TestSortByProcessName(t *testing.T) {
	m := NewPortModel()
	m.SetRaw([]core.Connection{
		{ProcessName: "Node"},
		{ProcessName: "apple"},
		{ProcessName: "Zebra"},
	})
	// 按进程名列(4)升序（不区分大小写）：apple, Node, Zebra
	if err := m.Sort(4, walk.SortAscending); err != nil {
		t.Fatal(err)
	}
	want := []string{"apple", "Node", "Zebra"}
	for i, w := range want {
		if got := m.view[i].ProcessName; got != w {
			t.Errorf("row %d: want %q, got %q", i, w, got)
		}
	}
}

func TestColumnSortable(t *testing.T) {
	m := NewPortModel()
	for col := 0; col <= 5; col++ {
		if !m.ColumnSortable(col) {
			t.Errorf("col %d 应可排序", col)
		}
	}
}

// TestSetRawPreservesSort 验证刷新数据后排序自动维持（核心修复点）。
func TestSetRawPreservesSort(t *testing.T) {
	m := NewPortModel()
	m.SetRaw([]core.Connection{
		{LocalPort: 8080}, {LocalPort: 80}, {LocalPort: 53},
	})
	// 先按端口升序
	m.Sort(2, walk.SortAscending)
	if m.view[0].LocalPort != 53 {
		t.Fatalf("排序后首行应为 53，实为 %d", m.view[0].LocalPort)
	}
	// 刷新数据（顺序打乱）
	m.SetRaw([]core.Connection{
		{LocalPort: 3306}, {LocalPort: 21}, {LocalPort: 8080}, {LocalPort: 443},
	})
	// 排序应被自动重新应用：升序 → 21, 443, 3306, 8080
	want := []uint16{21, 443, 3306, 8080}
	for i, w := range want {
		if got := m.view[i].LocalPort; got != w {
			t.Errorf("刷新后排序丢失 row %d: want %d, got %d", i, w, got)
		}
	}
}

// TestSetKeywordPreservesSort 验证改关键字后排序维持。
func TestSetKeywordPreservesSort(t *testing.T) {
	m := NewPortModel()
	m.SetRaw([]core.Connection{
		{LocalPort: 8080, ProcessName: "node"},
		{LocalPort: 80, ProcessName: "System"},
		{LocalPort: 3000, ProcessName: "node"},
	})
	m.Sort(2, walk.SortAscending) // 端口升序：80, 3000, 8080
	m.SetKeyword("node")          // 过滤后剩 3000, 8080（应仍升序）
	if len(m.view) != 2 {
		t.Fatalf("过滤后应剩 2 行，实为 %d", len(m.view))
	}
	if m.view[0].LocalPort != 3000 || m.view[1].LocalPort != 8080 {
		t.Errorf("过滤后排序应维持升序: got %d, %d",
			m.view[0].LocalPort, m.view[1].LocalPort)
	}
}
