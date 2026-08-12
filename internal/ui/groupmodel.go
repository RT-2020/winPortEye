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
	Pid          int32
	ProcessName  string // 原始进程名（权限不足时为空）
	ProcessPath  string // 原始路径（权限不足时为空）
	AccessDenied bool   // 该进程拿不到名/路径（权限不足），驱动降级展示
	PortCount    int    // 该进程占用的端口数
	PortSummary  string // 端口摘要，超长自动截断，避免单元格撑爆
}

// 列顺序：0 PID · 1 进程 · 2 端口数 · 3 端口摘要 · 4 路径
const (
	colGid      = 0
	colGName    = 1
	colGCount   = 2
	colGSummary = 3
	colGPath    = 4
)

// ProcessGroupModel 是主表（master）的数据模型：按 PID 聚合，一行一个进程。
//
// 防抖核心：本模型采用「按 PID 增量 diff」更新（类似 Vue :key），而非全量
// PublishRowsReset。walk 底层是 LVS_OWNERDATA 虚拟模式，只要不发 Reset，
// ListView 的滚动位置、焦点、单选选中位都会原样保留——从而彻底消除抖动。
//
//   - 外部（watcher / reload）调 SetRaw 推原始连接，内部聚合 + diff；
//   - diff 以 PID 为 key，算出 inserted/removed/changed 区间，发对应细粒度事件；
//   - 实现 IDProvider（ID 返回 PID），作为万一走 reset 路径时的选中找回兜底。
//
// 多选选中态的保留在 window.go 层处理（publish 前后 snapshot/还原 PID 集合），
// 因为 walk 在 insert/remove 涉及 currentIndex 时会清空多选。
type ProcessGroupModel struct {
	walk.TableModelBase
	walk.SorterBase

	raw       []core.Connection          // 原始全量数据（来自扫描）
	groups    map[int32][]core.Connection // PID -> 该进程的连接列表（聚合结果）
	rows      []ProcessGroupRow          // 聚合后的全量行（未过滤）
	keyword   string                     // 当前搜索关键字
	sortedCol int                        // 当前排序列（-1 = 未排序）
	sortOrder walk.SortOrder             // 当前排序方向
	view      []ProcessGroupRow          // 实际展示的数据（过滤+排序后）
	elevated  bool                       // 当前是否已提权；决定 AccessDenied 行的降级文案
}

func NewProcessGroupModel() *ProcessGroupModel {
	return &ProcessGroupModel{sortedCol: -1, groups: make(map[int32][]core.Connection)}
}

// SetElevated 设置当前是否以管理员权限运行，影响 AccessDenied 行的降级文案：
//   - 未提权：提示「权限不足 / 需管理员权限」，引导用户点「以管理员重启」；
//   - 已提权：仍打不开的进程是受保护的系统进程（System/csrss/lsass 等 PPL 或
//     内核进程），再提权也拿不到，继续提示「权限不足」会误导，改显示「系统进程 / 受保护」。
//
// 提权靠重启进程实现，运行期间该状态不会变化，启动时设置一次即可。
func (m *ProcessGroupModel) SetElevated(elevated bool) {
	m.elevated = elevated
}

// RowCount 实现 walk.TableModel。
func (m *ProcessGroupModel) RowCount() int {
	return len(m.view)
}

// Value 实现 walk.TableModel。权限不足的行做降级展示。
func (m *ProcessGroupModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.view) {
		return nil
	}
	r := m.view[row]
	switch col {
	case colGid:
		return int(r.Pid)
	case colGName:
		if r.AccessDenied && r.ProcessName == "" {
			if m.elevated {
				return fmt.Sprintf("(PID %d · 系统进程)", r.Pid)
			}
			return fmt.Sprintf("(PID %d · 权限不足)", r.Pid)
		}
		return r.ProcessName
	case colGCount:
		return r.PortCount
	case colGSummary:
		return r.PortSummary
	case colGPath:
		if r.AccessDenied && r.ProcessPath == "" {
			if m.elevated {
				return "(受保护)"
			}
			return "(需管理员权限)"
		}
		return r.ProcessPath
	}
	return nil
}

// ID 实现 walk.IDProvider：返回 PID，作为 reset 路径的选中找回兜底。
func (m *ProcessGroupModel) ID(index int) interface{} {
	if index < 0 || index >= len(m.view) {
		return nil
	}
	return int(m.view[index].Pid)
}

// ColumnSortable 实现 walk.Sorter。
func (m *ProcessGroupModel) ColumnSortable(col int) bool {
	return col >= colGid && col <= colGPath
}

// Sort 实现 walk.Sorter：记录排序状态并重算视图。
// 排序改变时行顺序整体变化，用 reset（这是用户主动点表头，可接受）。
func (m *ProcessGroupModel) Sort(col int, order walk.SortOrder) error {
	m.sortedCol = col
	m.sortOrder = order
	m.rebuildView()
	m.SorterBase.Sort(col, order)
	m.PublishRowsReset()
	return nil
}

// SetRaw 替换原始全量数据：聚合 → 重算 view → 增量 diff 发布。
// 这是 watcher 和刷新按钮调用的入口。
func (m *ProcessGroupModel) SetRaw(conns []core.Connection) {
	m.raw = conns
	m.aggregate()
	m.applyDiff()
}

// SetKeyword 设置搜索关键字并重算视图。
// 关键字改变会大幅改变 view 构成，用 reset（用户主动输入，可接受）。
func (m *ProcessGroupModel) SetKeyword(kw string) {
	m.keyword = kw
	m.rebuildView()
	m.PublishRowsReset()
}

// aggregate 按 PID 聚合 m.raw，生成 m.rows 与 m.groups。
func (m *ProcessGroupModel) aggregate() {
	// 复用 map（clear 比 make 省一次分配）；watcher 每 3 秒调一次，降低 GC 压力。
	// map 每个 key 的 value slice 不复用（累加语义下 [:0] 会误清，索性每次新建），
	// 主要收益来自 map 本身的 buckets 复用。
	if m.groups == nil {
		m.groups = make(map[int32][]core.Connection, len(m.rows)+8)
	} else {
		clear(m.groups)
	}
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
			Pid:          pid,
			ProcessName:  name,
			ProcessPath:  path,
			AccessDenied: name == "" && path == "" && pid > 0,
			PortCount:    len(conns),
			PortSummary:  buildPortSummary(conns),
		})
	}
	m.rows = rows
}

// applyDiff 是防抖的核心：以 PID 为业务主键做 diff，发细粒度事件，绝不发 Reset。
//
// 设计原则（数据驱动，view 变化最小化）：
//   - PID 是行的身份。新旧都有的 PID → 视为「同一行」，优先原地更新内容，
//     只发 RowChanged（行数不变 → walk 不触发 SetCurrentIndex → 不滚动）。
//   - 只有 PID 真正消失/出现，才发 remove/insert。删除从后往前，避免索引偏移。
//   - walk 的 RowsRemoved/RowsInserted handler 在索引偏移时会调 SetCurrentIndex，
//     进而触发 LVM_ENSUREVISIBLE 强制滚动——所以保留类行绝不走 remove+insert。
//
// 行号说明：按 PID 排序时保留 PID 的行号绝对稳定（位置不变）；按其他列排序时，
// 内容变化可能改变行序，但虚拟模式下 ListView 显示顺序由 model 的 Value 决定，
// 保留类行发 RowChanged 即可让其重绘。选中态按索引存，排序变化可能轻微错位，
// 这比「每 3 秒 reset 弹回顶部」的体验好得多。
func (m *ProcessGroupModel) applyDiff() {
	oldView := m.view
	m.rebuildView() // 生成新 view（已过滤+排序）
	newView := m.view

	// 极端情况：首次加载（oldView 空）→ 直接 reset
	if len(oldView) == 0 {
		m.PublishRowsReset()
		return
	}

	// 构建 PID -> 索引映射
	oldIdx := make(map[int32]int, len(oldView))
	for i, r := range oldView {
		oldIdx[r.Pid] = i
	}
	newIdx := make(map[int32]int, len(newView))
	for i, r := range newView {
		newIdx[r.Pid] = i
	}

	// === 阶段 1：removed（PID 消失）===
	// 从后往前删，保证前面索引不变。
	type span struct{ from, to int }
	var removedIdxs []int
	for pid := range oldIdx {
		if _, ok := newIdx[pid]; !ok {
			removedIdxs = append(removedIdxs, oldIdx[pid])
		}
	}
	sort.Ints(removedIdxs)
	// 合并连续区间，从后往前发布
	for i := len(removedIdxs) - 1; i >= 0; {
		to := removedIdxs[i]
		from := to
		for i-1 >= 0 && removedIdxs[i-1] == from-1 {
			from = removedIdxs[i-1]
			i--
		}
		m.PublishRowsRemoved(from, to)
		i--
	}

	// === 阶段 2：inserted（PID 新增）===
	// 此时 view 已是删除后的状态（len 已减）。新 view 比旧 view 多出的行就是新增。
	// 由于按 PID 升序，新增 PID 一定排在它应在的位置，用新 view 索引插入。
	var insertedIdxs []int
	for pid := range newIdx {
		if _, ok := oldIdx[pid]; !ok {
			insertedIdxs = append(insertedIdxs, newIdx[pid])
		}
	}
	sort.Ints(insertedIdxs)
	for i := 0; i < len(insertedIdxs); {
		from := insertedIdxs[i]
		to := from
		for i+1 < len(insertedIdxs) && insertedIdxs[i+1] == to+1 {
			to = insertedIdxs[i+1]
			i++
		}
		m.PublishRowsInserted(from, to)
		i++
	}

	// === 阶段 3：changed（PID 保留，内容变化）===
	// 保留 PID 的行号不变（按 PID 排序保证），原地发 RowChanged。
	for _, r := range newView {
		oi, ok := oldIdx[r.Pid]
		if !ok {
			continue
		}
		old := oldView[oi]
		if old.PortCount != r.PortCount ||
			old.PortSummary != r.PortSummary ||
			old.ProcessName != r.ProcessName ||
			old.ProcessPath != r.ProcessPath ||
			old.AccessDenied != r.AccessDenied {
			m.PublishRowChanged(newIdx[r.Pid])
		}
	}
}

// rebuildView 应用过滤 + 排序，重新生成 view（不发任何事件）。
//
// 排序规则：
//   - 用户点过表头（sortedCol >= 0）→ 按该列排序；
//   - 默认（sortedCol < 0）→ 按进程名升序，系统进程（PID 4 / 无名 / 权限不足）垫底。
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
	} else {
		// 默认排序：普通进程按名称升序，系统进程垫底（再按 PID 升序）
		sort.SliceStable(out, func(i, j int) bool { return defaultRowLess(out[i], out[j]) })
	}
	m.view = out
}

// isSystemRow 判定是否为系统进程行（排到列表末尾）：
// PID 4（System）、进程名为空、或权限不足拿不到名。
func isSystemRow(r ProcessGroupRow) bool {
	return r.Pid == 4 || r.AccessDenied || r.ProcessName == ""
}

// defaultRowLess 默认排序比较：普通进程在前（按名称不区分大小写），系统进程垫底（按 PID）。
// 同组内名称相同的按 PID 兜底，保证顺序稳定。
func defaultRowLess(a, b ProcessGroupRow) bool {
	aSys, bSys := isSystemRow(a), isSystemRow(b)
	if aSys != bSys {
		return !aSys // 普通行（非系统）排在前
	}
	if aSys {
		return a.Pid < b.Pid // 系统组按 PID
	}
	na, nb := strings.ToLower(a.ProcessName), strings.ToLower(b.ProcessName)
	if na != nb {
		return na < nb
	}
	return a.Pid < b.Pid // 名称相同按 PID 兜底
}

// matchGroupKeyword 判断聚合行是否匹配关键字（PID/进程名/路径/端口摘要）。
// 权限不足时也允许按 PID 搜到。
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
// 用于多选保选中：刷新后按 PID 找回新索引。
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
// 规则：去重排序 → 连续段合并成 "起-止"，超过 maxShow 个用 "... 共 N 个" 收尾。
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
