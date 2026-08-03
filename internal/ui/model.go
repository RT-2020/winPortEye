// Package ui 实现 GUI 主窗口（基于 lxn/walk）。
package ui

import (
	"fmt"
	"sort"
	"strings"

	"win/internal/core"

	"github.com/lxn/walk"
)

// PortModel 是 TableView 的数据模型。
//
// 设计要点：model 自己持有「原始数据 + 关键字 + 排序状态」三件套，
// 任何刷新都先应用过滤再应用排序，保证显示顺序稳定。
// 外部只需调 SetRaw / SetKeyword，无需关心排序/过滤何时被冲掉。
type PortModel struct {
	walk.TableModelBase
	walk.SorterBase

	raw       []core.Connection // 原始全量数据（来自扫描）
	keyword   string            // 当前搜索关键字
	sortedCol int               // 当前排序列（-1 = 未排序）
	sortOrder walk.SortOrder    // 当前排序方向
	view      []core.Connection // 实际展示的数据（过滤+排序后）
}

func NewPortModel() *PortModel {
	m := &PortModel{sortedCol: -1}
	return m
}

// RowCount 实现 walk.TableModel。
func (m *PortModel) RowCount() int {
	return len(m.view)
}

// Value 实现 walk.TableModel。列顺序：0协议 1地址 2端口 3状态 4进程名 5路径。
func (m *PortModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.view) {
		return nil
	}
	c := m.view[row]
	switch col {
	case 0:
		return string(c.Protocol)
	case 1:
		return c.LocalAddr
	case 2:
		return int(c.LocalPort)
	case 3:
		return c.State
	case 4:
		return c.ProcessName
	case 5:
		return c.ProcessPath
	}
	return nil
}

// ColumnSortable 实现 walk.Sorter。
func (m *PortModel) ColumnSortable(col int) bool {
	return col >= 0 && col <= 5
}

// Sort 实现 walk.Sorter：记录排序状态并重算视图。
func (m *PortModel) Sort(col int, order walk.SortOrder) error {
	m.sortedCol = col
	m.sortOrder = order
	m.rebuildView()
	// 通知 SorterBase 记录状态（驱动表头箭头显示）
	m.SorterBase.Sort(col, order)
	m.PublishRowsReset()
	return nil
}

// SetRaw 替换原始全量数据，自动按当前 keyword/排序状态重算视图。
// 这是 watcher 和刷新按钮调用的入口。
func (m *PortModel) SetRaw(conns []core.Connection) {
	m.raw = conns
	m.rebuildView()
	m.PublishRowsReset()
}

// SetKeyword 设置搜索关键字并重算视图。
func (m *PortModel) SetKeyword(kw string) {
	m.keyword = kw
	m.rebuildView()
	m.PublishRowsReset()
}

// rebuildView 应用过滤 + 排序，重新生成 view。
// 所有数据变化都经此函数，保证显示一致。
func (m *PortModel) rebuildView() {
	// 1. 过滤
	out := make([]core.Connection, 0, len(m.raw))
	for _, c := range m.raw {
		if matchKeyword(c, m.keyword) {
			out = append(out, c)
		}
	}
	// 2. 排序（若有当前排序列）
	if m.sortedCol >= 0 {
		less := func(i, j int) bool { return connLess(out[i], out[j], m.sortedCol) }
		if m.sortOrder == walk.SortDescending {
			sort.Slice(out, func(i, j int) bool { return less(j, i) })
		} else {
			sort.Slice(out, less)
		}
	}
	m.view = out
}

// matchKeyword 判断连接是否匹配搜索关键字（端口/进程名/路径/地址/状态/协议）。
func matchKeyword(c core.Connection, kw string) bool {
	if kw == "" {
		return true
	}
	return contains(c.ProcessName, kw) ||
		contains(c.ProcessPath, kw) ||
		contains(c.LocalAddr, kw) ||
		contains(fmt.Sprint(c.LocalPort), kw) ||
		contains(c.State, kw) ||
		contains(string(c.Protocol), kw)
}

// At 返回指定行的连接（供详情/杀进程用）。
func (m *PortModel) At(row int) (core.Connection, bool) {
	if row < 0 || row >= len(m.view) {
		return core.Connection{}, false
	}
	return m.view[row], true
}

// connLess 比较两个连接在指定列上的大小。
// 数字列（端口/PID）按数值，其余按字符串（进程名/路径不区分大小写）。
func connLess(a, b core.Connection, col int) bool {
	switch col {
	case 0:
		return a.Protocol < b.Protocol
	case 1:
		return a.LocalAddr < b.LocalAddr
	case 2:
		return a.LocalPort < b.LocalPort
	case 3:
		return a.State < b.State
	case 4:
		return strings.ToLower(a.ProcessName) < strings.ToLower(b.ProcessName)
	case 5:
		return strings.ToLower(a.ProcessPath) < strings.ToLower(b.ProcessPath)
	}
	return false
}

func contains(s, sub string) bool {
	if sub == "" {
		return true
	}
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
