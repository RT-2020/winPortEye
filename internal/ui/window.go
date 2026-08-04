package ui

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"win/internal/core"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// 关键系统进程名（小写匹配）。批量杀时若命中，弹红字警告。
var criticalProcessNames = map[string]bool{
	"csrss": true, "wininit": true, "winlogon": true, "services": true,
	"lsass": true, "smss": true, "svchost": true, "fontdrvhost": true,
	"dwm": true, "explorer": false, // explorer 可杀但会丢任务栏，列为提示级
}

// Run 启动 GUI 主窗口。阻塞直到窗口关闭。
func Run() {
	setLogFile()

	groupModel := NewProcessGroupModel()
	detailModel := NewPortModel()
	var mw *walk.MainWindow
	var masterTV *walk.TableView
	var detailTV *walk.TableView
	var searchBox *walk.LineEdit
	var detailLabel *walk.Label
	var excludeHintLabel *walk.Label // 端口排除范围提示条（搜索命中时显示）
	var killBtn *walk.PushButton
	var elevateBtn *walk.PushButton

	// updateExcludeHint 根据搜索框内容判断是否命中 Windows 端口排除范围，
	// 命中则显示黄色提示条。只对纯数字端口查询触发。
	//
	// 三种状态的处理：
	//   - 检测成功且命中 → 显示具体排除段提示
	//   - 检测成功但未命中 → 不提示（端口既无占用也无预留）
	//   - netsh 不可用 → 若该端口在主表也无进程占用，提示"可能被内核预留（netsh 不可用，无法检测）"
	//   - 未检测（预热未完成）→ 不提示
	updateExcludeHint := func() {
		if excludeHintLabel == nil {
			return
		}
		kw := ""
		if searchBox != nil {
			kw = strings.TrimSpace(searchBox.Text())
		}
		if kw == "" {
			excludeHintLabel.SetVisible(false)
			return
		}
		port, err := strconv.Atoi(kw)
		if err != nil || port < 1 || port > 65535 {
			excludeHintLabel.SetVisible(false)
			return
		}

		// 只读缓存，绝不跑 netsh（避免 UI 卡顿和弹控制台窗口）。
		ranges, status := core.ListExcludedPortRangesNoBlock()

		switch status {
		case core.ExcludedStatusOK:
			matched := core.FindExcludedPort(uint16(port), ranges)
			if len(matched) == 0 {
				excludeHintLabel.SetVisible(false)
				return
			}
			// 命中：构建具体提示文案
			r := matched[0]
			protoStr := "TCP/UDP"
			if len(matched) == 1 {
				protoStr = strings.ToUpper(r.Protocol)
			}
			hint := fmt.Sprintf("⚠ 端口 %d 被 Windows 内核预留（%s 排除段 %d-%d，无进程占用）。\n"+
				"应用会无法绑定此端口。建议：换一个不在排除范围的端口，或调整排除范围。",
				port, protoStr, r.Start, r.End)
			excludeHintLabel.SetText(hint)
			excludeHintLabel.SetVisible(true)

		case core.ExcludedStatusUnavailable:
			// netsh 不可用：仅当该端口在主表也无进程占用时，提示"可能被预留"
			// （有进程占用时，占用才是主要矛盾，不混淆用户）
			if groupModel.RowCount() == 0 {
				hint := fmt.Sprintf("端口 %d 无进程占用，但可能被 Windows 内核预留（netsh 不可用，无法检测）。\n"+
					"若应用绑定此端口报错，可能是 Hyper-V/WSL 动态预留，建议更换端口。",
					port)
				excludeHintLabel.SetText(hint)
				excludeHintLabel.SetVisible(true)
			} else {
				excludeHintLabel.SetVisible(false)
			}

		default: // ExcludedStatusUnknown：预热未完成，不提示
			excludeHintLabel.SetVisible(false)
		}
	}

	// updateKillBtn 根据主表当前选中数量更新终止按钮文字与可用状态。
	updateKillBtn := func() {
		if killBtn == nil || masterTV == nil {
			return
		}
		n := len(masterTV.SelectedIndexes())
		if n <= 1 {
			// 0 或 1 个时显示单数文案；0 个时置灰
			killBtn.SetText("终止选中进程")
			killBtn.SetEnabled(n == 1)
		} else {
			killBtn.SetText(fmt.Sprintf("终止选中进程（%d 个）", n))
			killBtn.SetEnabled(true)
		}
	}

	// refreshDetail 根据主表当前选中行，更新子表与详情标签。
	// 多选时取 currentIndex（焦点行）作为详情展示对象。
	refreshDetail := func() {
		updateDetail(detailLabel, masterTV, groupModel)
		row := masterTV.CurrentIndex()
		g, ok := groupModel.At(row)
		if !ok {
			detailModel.SetConns(nil)
			return
		}
		detailModel.SetConns(groupModel.ConnsOf(g.Pid))
	}

	// reload 重新扫描端口并推入主表 model。
	//
	// 防抖关键（实测验证后的结论）：
	//   - 真实环境 3 秒内 PID 保留率约 98%，model 的 diff 几乎只发 RowChanged，
	//     walk 虚拟模式下选中/焦点/滚动自动保留，【无需任何额外操作】。
	//   - 只有当选中的 PID 真的消失（进程退出/被杀）时，才需要 SetSelectedIndexes
	//     清理失效选中。绝不在 PID 全部保留时调 SetSelectedIndexes——它会先清空全部
	//     item 的选中态再重设（LVM_SETITEMSTATE ^0），导致整表闪烁 + 焦点丢失。
	//   - 绝不调 SetCurrentIndex——它触发 LVM_ENSUREVISIBLE 强制滚动。
	// 所有刷新路径（首次加载/刷新按钮/watcher/kill后）都走这个闭包。
	reload := func() {
		conns, err := core.ListConnections(core.KindAll)
		if err != nil {
			walk.MsgBox(mw, "错误", err.Error(), walk.MsgBoxIconError)
			return
		}

		// publish 前 snapshot 选中的 PID 集合
		var prevPids []int32
		if masterTV != nil {
			for _, i := range masterTV.SelectedIndexes() {
				if g, ok := groupModel.At(i); ok {
					prevPids = append(prevPids, g.Pid)
				}
			}
			if len(prevPids) == 0 {
				if g, ok := groupModel.At(masterTV.CurrentIndex()); ok {
					prevPids = append(prevPids, g.Pid)
				}
			}
		}

		groupModel.SetRaw(conns) // 内部聚合 + diff + 细粒度发布（98% 情况只发 RowChanged）

		// 只有当选中 PID 有消失时，才需要清理失效选中。
		// PID 全部保留时绝不调 SetSelectedIndexes——那会清空整表选中态导致闪烁。
		if masterTV != nil && len(prevPids) > 0 {
			newIdxs := make([]int, 0, len(prevPids))
			lost := false
			for _, pid := range prevPids {
				idx := groupModel.IndexOfPid(pid)
				if idx >= 0 {
					newIdxs = append(newIdxs, idx)
				} else {
					lost = true // 有选中 PID 消失了
				}
			}
			if lost {
				// 只有真的丢了选中项才重设（清理失效的）
				if len(newIdxs) > 0 {
					masterTV.SetSelectedIndexes(newIdxs)
				}
			}
			// lost==false：所有选中 PID 都还在，walk 虚拟模式已自动保留选中，什么都不做
		}
		refreshDetail()
		updateKillBtn()
	}

	if err := (MainWindow{
		AssignTo: &mw,
		Title:    "PortEye",
		Size:     Size{Width: 960, Height: 660},
		MinSize:  Size{Width: 720, Height: 480},
		Layout:   VBox{MarginsZero: true, SpacingZero: true},
		Children: []Widget{
			// 顶部工具栏：搜索 + 刷新 + 提权 + 设置
			Composite{
				Layout: HBox{MarginsZero: false, Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 6}, Spacing: 8},
				Children: []Widget{
					Label{Text: "搜索", MinSize: Size{Width: 36, Height: 0}},
					LineEdit{
						AssignTo:  &searchBox,
						CueBanner: "输入 PID / 端口 / 进程名 过滤...",
						OnTextChanged: func() {
							kw := ""
							if searchBox != nil {
								kw = searchBox.Text()
							}
							groupModel.SetKeyword(kw)
							refreshDetail()
							updateKillBtn()
							updateExcludeHint()
						},
					},
					PushButton{Text: "刷新", OnClicked: func() { reload() }},
					// 提权按钮：已提权时置灰
					PushButton{
						AssignTo: &elevateBtn,
						Text:     "以管理员重启",
						OnClicked: func() {
							if err := core.RelaunchElevated(""); err != nil {
								walk.MsgBox(mw, "失败", err.Error(), walk.MsgBoxIconError)
								return
							}
							// 重启成功，退出当前进程
							mw.Close()
						},
					},
					PushButton{Text: "⚙ 设置", OnClicked: func() { showSettings(mw) }},
				},
			},
			// 端口排除范围提示条（搜索命中预留端口时显示，默认隐藏）
			Composite{
				Layout: HBox{MarginsZero: false, Margins: Margins{Left: 10, Top: 0, Right: 10, Bottom: 4}, Spacing: 0},
				Children: []Widget{
					Label{
						AssignTo:  &excludeHintLabel,
						Visible:   false,
						TextColor: walk.RGB(180, 120, 0), // 暗黄，醒目但不刺眼
						MinSize:   Size{Height: 32},
					},
				},
			},
			// 主表（master）：进程聚合，支持多选
			TableView{
				AssignTo:            &masterTV,
				MinSize:             Size{Height: 200},
				LastColumnStretched: true,
				HeaderHidden:        false,
				MultiSelection:      true,
				Columns: []TableViewColumn{
					{Title: "PID", Width: 70},
					{Title: "进程", Width: 150},
					{Title: "端口数", Width: 60},
					{Title: "端口摘要", Width: 240},
					{Title: "路径", Width: 320},
				},
				Model: groupModel,
				OnCurrentIndexChanged: func() {
					refreshDetail()
				},
				OnSelectedIndexesChanged: func() {
					updateKillBtn()
				},
			},
			// 子表标签分隔
			Composite{
				Layout: HBox{MarginsZero: false, Margins: Margins{Left: 10, Top: 2, Right: 10, Bottom: 2}, Spacing: 8},
				Children: []Widget{
					Label{Text: "端口明细（选中上方进程查看）"},
				},
			},
			// 子表（detail）：选中进程占用的端口
			TableView{
				AssignTo:            &detailTV,
				MinSize:             Size{Height: 120},
				LastColumnStretched: true,
				HeaderHidden:        false,
				Columns: []TableViewColumn{
					{Title: "协议", Width: 56},
					{Title: "本地地址", Width: 150},
					{Title: "端口", Width: 70},
					{Title: "远端地址", Width: 200},
					{Title: "状态", Width: 110},
				},
				Model: detailModel,
			},
			// 底部：进程详情 + 操作按钮
			Composite{
				Layout: HBox{MarginsZero: false, Margins: Margins{Left: 10, Top: 6, Right: 10, Bottom: 10}, Spacing: 8},
				Children: []Widget{
					Label{AssignTo: &detailLabel, Text: "选中主表进程查看详情 · 点表头可排序"},
					HSpacer{},
					PushButton{
						AssignTo:  &killBtn,
						Text:      "终止选中进程",
						Enabled:   false,
						OnClicked: func() { killSelected(mw, masterTV, groupModel, reload) },
					},
				},
			},
		},
	}.Create()); err != nil {
		log.Fatalf("创建主窗口失败: %v", err)
	}

	// 初始化提权按钮状态
	if core.IsElevated() {
		elevateBtn.SetText("已是管理员")
		elevateBtn.SetEnabled(false)
	}

	// 首次加载数据
	reload()

	// 后台预热端口排除范围缓存（netsh 较慢约 1-2 秒，提前加载避免首次搜索时卡 UI）。
	// 用户还没搜索时就把数据准备好，搜索框回调只读缓存（毫秒级），不阻塞 UI 线程。
	go func() { _, _, _ = core.ListExcludedPortRanges() }()

	// 创建托盘图标 + 拦截关闭按钮
	if _, err := setupTray(mw); err != nil {
		log.Printf("托盘创建失败（不影响主功能）: %v", err)
	}
	hookCloseButton(mw)

	// 启动后台轮询（定时在 UI 线程触发 reload，扫描/聚合/diff/选中恢复统一在 reload 内）
	go startWatcher(mw, reload)

	mw.Show()
	mw.Run()
}

// updateDetail 更新底部详情标签为当前主表选中进程。
func updateDetail(label *walk.Label, tv *walk.TableView, model *ProcessGroupModel) {
	if label == nil || tv == nil {
		return
	}
	row := tv.CurrentIndex()
	g, ok := model.At(row)
	if !ok {
		label.SetText("选中主表进程查看详情 · 点表头可排序")
		return
	}
	name := g.ProcessName
	if g.AccessDenied && name == "" {
		name = "(权限不足)"
	}
	label.SetText(fmt.Sprintf("PID %d  %s  %s  ·  占用 %d 个端口", g.Pid, name, g.ProcessPath, g.PortCount))
}

// killSelected 批量终止主表所有选中进程，弹确认框 + 系统进程警告，逐个杀后汇总结果。
func killSelected(mw *walk.MainWindow, tv *walk.TableView, model *ProcessGroupModel, reload func()) {
	indexes := tv.SelectedIndexes()
	if len(indexes) == 0 {
		// 兜底：多选为空时尝试 currentIndex
		if i := tv.CurrentIndex(); i >= 0 {
			indexes = []int{i}
		}
	}
	if len(indexes) == 0 {
		walk.MsgBox(mw, "提示", "请先在主表选中一个或多个进程", walk.MsgBoxIconInformation)
		return
	}

	// 收集选中行（去重 PID，同一 PID 不重复杀）
	seen := make(map[int32]bool)
	var targets []ProcessGroupRow
	for _, i := range indexes {
		g, ok := model.At(i)
		if !ok || g.Pid <= 0 || seen[g.Pid] {
			continue
		}
		seen[g.Pid] = true
		targets = append(targets, g)
	}
	if len(targets) == 0 {
		return
	}

	// 检测关键系统进程，构建警告信息
	var dangerous []string
	for _, g := range targets {
		if g.Pid == 4 {
			dangerous = append(dangerous, fmt.Sprintf("PID %d (System)", g.Pid))
			continue
		}
		if g.ProcessName == "" {
			dangerous = append(dangerous, fmt.Sprintf("PID %d (未知进程)", g.Pid))
			continue
		}
		if criticalProcessNames[strings.ToLower(g.ProcessName)] {
			dangerous = append(dangerous, fmt.Sprintf("PID %d (%s)", g.Pid, g.ProcessName))
		}
	}

	// 二次确认
	msg := fmt.Sprintf("确定终止选中的 %d 个进程吗？\n这些进程占用的所有端口将一并释放。", len(targets))
	if len(dangerous) > 0 {
		msg += "\n\n⚠ 警告：以下可能是系统关键进程，终止后可能导致系统不稳定甚至蓝屏：\n"
		for _, d := range dangerous {
			msg += "  · " + d + "\n"
		}
		msg += "请确认你知道自己在做什么。"
	}
	if walk.MsgBox(mw, "确认", msg, walk.MsgBoxYesNo|walk.MsgBoxIconWarning) != walk.DlgCmdYes {
		return
	}

	// 逐个终止
	var okCount, failCount int
	var failMsgs []string
	for _, g := range targets {
		result := core.KillProcess(g.Pid)
		if result.Success {
			okCount++
		} else {
			failCount++
			failMsgs = append(failMsgs, fmt.Sprintf("PID %d (%s): %s", g.Pid, g.ProcessName, result.Message))
		}
	}

	// 汇总结果
	if failCount == 0 {
		walk.MsgBox(mw, "成功", fmt.Sprintf("已终止 %d 个进程", okCount), walk.MsgBoxIconInformation)
	} else {
		summary := fmt.Sprintf("成功 %d 个，失败 %d 个：\n", okCount, failCount)
		for _, fm := range failMsgs {
			summary += "  · " + fm + "\n"
		}
		walk.MsgBox(mw, "部分失败", summary, walk.MsgBoxIconWarning)
	}

	reload() // 刷新列表（增量 diff + 选中恢复）
}
