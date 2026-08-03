package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"win/internal/core"

	"github.com/lxn/walk"
)

// ProcessGroupRow 是主表的一行：一个 PID 聚合后的进程。
type ProcessGroupRow struct {
	Pid         int32
	ProcessName string
	ProcessPath string
	PortCount   int    // 该进程占用的端口数
	PortSummary string // 端口摘要，超长自动截断，避免单元格撑爆
}

// ProcessGroupModel 是主表（master）的数据模型：按 PID 聚合，一行一个进程。
//
// 设计要点：
//   - 外部（watcher / reload）只推原始 []core.Connection，聚合在本模型内完成，
//     与采集层解耦。
//   - 选中态由调用方（window.go）记录 PID，刷新后若该 PID 仍在，可调 IndexOfPid
//     恢复选中行号，避免 3 秒 watcher 刷新打断操作。
//   - 过滤 + 排序套路与 PortModel 一致。
type ProcessGroupModel struct {
	walk.TableModelBase
	walk.SorterBase

	raw       []core.Connection          // 原始全量数据（来自扫描）
	groups    map[int32][]core.Connection // PID -> 该进程的连接列表（聚合结果，未过滤）
	rows      []ProcessGroupRow          // 聚合后的全量行（未过滤）
	keyword   string                     // 当前搜索关键字
	sortedCol int                        // 当前排序列（-1 = 未排序）
	sortOrder walk.SortOrder             // 当前排序方向
	view      []ProcessGroupRow          // 实际展示的数据（过滤+排序后）
}

// 列顺序：0 PID · 1 进程 · 2 端口数 · 3 端口摘要 · 4 路径
const (
	colGid     = 0
	colGName   = 1
	colGCount  = 2
	colGSummary = 3
	colGPath   = 4
)

func NewProcessGroupModel() *ProcessGroupModel {
	return &ProcessGroupModel{sortedCol: -1, groups: make(map[int32][]core.Connection)}
}

// RowCount 实现 walk.TableModel。
func (m *ProcessGroupModel) RowCount() int {
	return len(m.view)
}

// Value 实现 walk.TableModel。
func (m *ProcessGroupModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.view) {
		return nil
	}
	r := m.view[row]
	switch col {
	case colGid:
		return int(r.Pid)
	case colGName:
		return r.ProcessName
	case colGCount:
		return r.PortCount
	case colGSummary:
		return r.PortSummary
	case colGPath:
		return r.ProcessPath
	}
	return nil
}

// ColumnSortable 实现 walk.Sorter。
func (m *ProcessGroupModel) ColumnSortable(col int) bool {
	return col >= colGid && col <= colGPath
}

// Sort 实现 walk.Sorter：记录排序状态并重算视图。
func (m *ProcessGroupModel) Sort(col int, order walk.SortOrder) error {
	m.sortedCol = col
	m.sortOrder = order
	m.rebuildView()
	m.SorterBase.Sort(col, order)
	m.PublishRowsReset()
	return nil
}

// SetRaw 替换原始全量数据，内部按 PID 聚合，再按当前 keyword/排序状态重算视图。
// watcher 和刷新按钮调用的入口。
func (m *ProcessGroupModel) SetRaw(conns []core.Connection) {
	m.raw = conns
	m.aggregate()
	m.rebuildView()
	m.PublishRowsReset()
}

// SetKeyword 设置搜索关键字并重算视图。
func (m *ProcessGroupModel) SetKeyword(kw string) {
	m.keyword = kw
	m.rebuildView()
	m.PublishRowsReset()
}

// aggregate 按 PID 聚合 m.raw，生成 m.rows 与 m.groups。
// 顺序保持稳定：先按 PID 收集，再按 PID 升序输出行。
func (m *ProcessGroupModel) aggregate() {
	m.groups = make(map[int32][]core.Connection, len(m.rows)+8)
	for _, c := range m.raw {
		m.groups[c.Pid] = append(m.groups[c.Pid], c)
	}

	pids := make([]int32, 0, len(m.groups))
	for pid := range m.groups {
		pids = append(pids, pid)
	}
	sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })

	rows := make([]ProcessGroupRow, 0, len(pids))
	for _, pid := range pids {
		conns := m.groups[pid]
		// 同一进程的进程名/路径取第一条非空（理论上各连接一致）
		var name, path string
		for _, c := range conns {
			if name == "" {
				name = c.ProcessName
			}
			if path == "" {
				path = c.ProcessPath
			}
			if name != "" && path != "" {
				break
			}
		}
		rows = append(rows, ProcessGroupRow{
			Pid:         pid,
			ProcessName: name,
			ProcessPath: path,
			PortCount:   len(conns),
			PortSummary: buildPortSummary(conns),
		})
	}
	m.rows = rows
}

// rebuildView 应用过滤 + 排序，重新生成 view。
func (m *ProcessGroupModel) rebuildView() {
	out := make([]ProcessGroupRow, 0, len(m.rows))
	for _, r := range m.rows {
		if matchGroupKeyword(r, m.keyword) {
			out = append(out, r)
		}
	}
	if m.sortedCol >= 0 {
		less := func(i, j int) bool { return groupRowLess(out[i], out[j], m.sortedCol) }
		if m.sortOrder == walk.SortDescending {
			sort.Slice(out, func(i, j int) bool { return less(j, i) })
		} else {
			sort.Slice(out, less)
		}
	}
	m.view = out
}

// matchGroupKeyword 判断聚合行是否匹配关键字（PID/进程名/路径/端口摘要）。
func matchGroupKeyword(r ProcessGroupRow, kw string) bool {
	if kw == "" {
		return true
	}
	return contains(strconv.Itoa(int(r.Pid)), kw) ||
		contains(r.ProcessName, kw) ||
		contains(r.ProcessPath, kw) ||
		contains(r.PortSummary, kw)
}

// At 返回指定行的聚合行（供详情/杀进程用）。
func (m *ProcessGroupModel) At(row int) (ProcessGroupRow, bool) {
	if row < 0 || row >= len(m.view) {
		return ProcessGroupRow{}, false
	}
	return m.view[row], true
}

// ConnsOf 返回指定 PID 占用的所有连接（供子表填充）。
func (m *ProcessGroupModel) ConnsOf(pid int32) []core.Connection {
	return m.groups[pid]
}

// IndexOfPid 返回 PID 在当前 view 中的行号，找不到返回 -1。
// 用于 watcher 刷新后恢复选中态。
func (m *ProcessGroupModel) IndexOfPid(pid int32) int {
	for i, r := range m.view {
		if r.Pid == pid {
			return i
		}
	}
	return -1
}

// groupRowLess 比较两行在指定列上的大小。端口数/PID 按数值，其余按字符串。
func groupRowLess(a, b ProcessGroupRow, col int) bool {
	switch col {
	case colGid:
		return a.Pid < b.Pid
	case colGName:
		return strings.ToLower(a.ProcessName) < strings.ToLower(b.ProcessName)
	case colGCount:
		if a.PortCount != b.PortCount {
			return a.PortCount < b.PortCount
		}
		return a.Pid < b.Pid
	case colGSummary:
		return a.PortSummary < b.PortSummary
	case colGPath:
		return strings.ToLower(a.ProcessPath) < strings.ToLower(b.ProcessPath)
	}
	return false
}

// buildPortSummary 把一组连接压缩成端口摘要。
// 规则：去重排序 → 连续段合并成 "起-止"，超过 maxSummaryPorts 个用 "... 共 N 个" 收尾。
//
// 例：[80,81,82,443,8080,8081] -> "80-82, 443, 8080-8081"
// 例：[1..100] -> "1-20, ... 共 100 个"
func buildPortSummary(conns []core.Connection) string {
	seen := make(map[uint16]struct{}, len(conns))
	ports := make([]uint16, 0, len(conns))
	for _, c := range conns {
		if _, ok := seen[c.LocalPort]; ok {
			continue
		}
		seen[c.LocalPort] = struct{}{}
		ports = append(ports, c.LocalPort)
	}
	if len(ports) == 0 {
		return ""
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i] < ports[j] })

	const maxShow = 8
	var b strings.Builder
	showed := 0
	i := 0
	for i < len(ports) {
		if showed >= maxShow {
			break
		}
		start := ports[i]
		end := start
		// 找连续段
		for i+1 < len(ports) && ports[i+1] == end+1 {
			end = ports[i+1]
			i++
		}
		if showed > 0 {
			b.WriteString(", ")
		}
		if start == end {
			b.WriteString(strconv.Itoa(int(start)))
		} else {
			fmt.Fprintf(&b, "%d-%d", start, end)
		}
		showed++
		i++
	}
	if len(ports) > maxShow {
		fmt.Fprintf(&b, ", ... 共 %d 个", len(ports))
	}
	return b.String()
}
