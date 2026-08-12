// Package ui 实现 GUI 主窗口（基于 lxn/walk）。
package ui

import (
	"fmt"
	"sort"
	"strings"

	"win/internal/core"

	"github.com/lxn/walk"
)

// PortModel 是子表（detail）的数据模型：展示某个进程占用的端口明细。
//
// 角色说明：主表（ProcessGroupModel）按 PID 聚合，选中一个进程后，
// 用本模型展示该进程占用的所有端口。本模型只持有端口维度的扁平列表。
//
// 设计要点：model 持有「数据 + 排序状态」，刷新时应用排序保证显示稳定。
// 外部调 SetConns 替换数据即可。列顺序：0协议 1本地地址 2端口 3远端地址 4状态。
type PortModel struct {
	walk.TableModelBase
	walk.SorterBase

	conns     []core.Connection // 当前展示的端口列表
	sortedCol int               // 当前排序列（-1 = 未排序）
	sortOrder walk.SortOrder    // 当前排序方向
}

func NewPortModel() *PortModel {
	return &PortModel{sortedCol: -1}
}

// RowCount 实现 walk.TableModel。
func (m *PortModel) RowCount() int {
	return len(m.conns)
}

// Value 实现 walk.TableModel。列顺序：0协议 1本地地址 2端口 3远端地址 4状态。
func (m *PortModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.conns) {
		return nil
	}
	c := m.conns[row]
	switch col {
	case 0:
		return string(c.Protocol)
	case 1:
		return c.LocalAddr
	case 2:
		return int(c.LocalPort)
	case 3:
		if c.RemoteAddr == "" {
			return "-"
		}
		return fmt.Sprintf("%s:%d", c.RemoteAddr, c.RemotePort)
	case 4:
		return c.State
	}
	return nil
}

// ColumnSortable 实现 walk.Sorter。
func (m *PortModel) ColumnSortable(col int) bool {
	return col >= 0 && col <= 4
}

// Sort 实现 walk.Sorter：记录排序状态并重排。
func (m *PortModel) Sort(col int, order walk.SortOrder) error {
	m.sortedCol = col
	m.sortOrder = order
	m.applySort()
	m.SorterBase.Sort(col, order)
	m.PublishRowsReset()
	return nil
}

// SetConns 替换端口列表并按当前排序状态重排。主表选中变化 / reload 时调用。
//
// 防抖：如果新数据与旧数据「内容相同」（长度相等且逐条相等），直接返回不发
// PublishRowsReset——避免 watcher 每 3 秒 reload 时子表整片重绘闪烁。
// 只有数据真正变化时才 reset。
func (m *PortModel) SetConns(conns []core.Connection) {
	if connsEqual(m.conns, conns) {
		return // 内容未变，跳过 reset，避免无谓重绘
	}
	m.conns = conns
	m.applySort()
	m.PublishRowsReset()
}

// connsEqual 比较两个连接切片是否逐条相等（顺序敏感）。
func connsEqual(a, b []core.Connection) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// applySort 按当前排序列重排 conns。
func (m *PortModel) applySort() {
	if m.sortedCol < 0 || len(m.conns) < 2 {
		return
	}
	less := func(i, j int) bool { return connLess(m.conns[i], m.conns[j], m.sortedCol) }
	if m.sortOrder == walk.SortDescending {
		sort.Slice(m.conns, func(i, j int) bool { return less(j, i) })
	} else {
		sort.Slice(m.conns, less)
	}
}

// connLess 比较两个连接在指定列上的大小。端口列按数值，其余按字符串。
func connLess(a, b core.Connection, col int) bool {
	switch col {
	case 0:
		return a.Protocol < b.Protocol
	case 1:
		return a.LocalAddr < b.LocalAddr
	case 2:
		return a.LocalPort < b.LocalPort
	case 3:
		return a.RemoteAddr < b.RemoteAddr
	case 4:
		return a.State < b.State
	}
	return false
}

// contains 用于关键字匹配（主表 ProcessGroupModel 复用）。
func contains(s, sub string) bool {
	if sub == "" {
		return true
	}
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

// indexOf 是不依赖 strings.Contains 的子串查找（主表复用）。
func indexOf(s, sub string) int {
	return strings.Index(s, sub)
}
