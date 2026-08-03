package ui

import (
	"fmt"
	"log"

	"win/internal/core"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// Run 启动 GUI 主窗口。阻塞直到窗口关闭。
func Run() {
	// 日志写到文件，避免 stderr 弹控制台
	setLogFile()

	groupModel := NewProcessGroupModel() // 主表：进程聚合
	detailModel := NewPortModel()        // 子表：选中进程的端口明细
	var mw *walk.MainWindow
	var masterTV *walk.TableView
	var detailTV *walk.TableView
	var searchBox *walk.LineEdit
	var detailLabel *walk.Label

	// refreshDetail 根据主表当前选中行，把对应进程的端口灌入子表，并更新详情标签。
	// 主表选中变化 / reload / kill 后都走这里。
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

	// reload 重新扫描端口并推入主表 model（聚合在 model 内完成）。
	// 所有刷新路径（首次加载/刷新按钮/watcher/kill后）都走这个闭包。
	reload := func() {
		conns, err := core.ListConnections(core.KindAll)
		if err != nil {
			walk.MsgBox(mw, "错误", err.Error(), walk.MsgBoxIconError)
			return
		}
		// 记录刷新前选中 PID，刷新后若仍存在则恢复选中，避免列表跳动
		var prevPid int32
		if g, ok := groupModel.At(masterTV.CurrentIndex()); ok {
			prevPid = g.Pid
		}
		groupModel.SetRaw(conns)
		// 恢复选中
		if prevPid > 0 {
			if idx := groupModel.IndexOfPid(prevPid); idx >= 0 {
				masterTV.SetCurrentIndex(idx)
			}
		}
		refreshDetail()
	}

	if err := (MainWindow{
		AssignTo: &mw,
		Title:    "PortEye 端口之眼",
		Size:     Size{Width: 960, Height: 660},
		MinSize:  Size{Width: 720, Height: 480},
		Layout:   VBox{MarginsZero: true, SpacingZero: true},
		Children: []Widget{
			// 顶部工具栏：搜索 + 刷新 + 设置
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
						},
					},
					PushButton{Text: "刷新", OnClicked: func() { reload() }},
					PushButton{Text: "⚙ 设置", OnClicked: func() { showSettings(mw) }},
				},
			},
			// 主表（master）：进程聚合，一行一个 PID
			TableView{
				AssignTo:            &masterTV,
				MinSize:             Size{Height: 200},
				LastColumnStretched: true,
				HeaderHidden:        false,
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
						Text:      "终止选中进程",
						OnClicked: func() { killSelected(mw, masterTV, groupModel, reload) },
					},
				},
			},
		},
	}.Create()); err != nil {
		log.Fatalf("创建主窗口失败: %v", err)
	}

	// 首次加载数据
	reload()

	// 创建托盘图标 + 拦截关闭按钮
	if _, err := setupTray(mw); err != nil {
		log.Printf("托盘创建失败（不影响主功能）: %v", err)
	}
	hookCloseButton(mw)

	// 启动后台轮询（定时在 UI 线程触发 reload，扫描/聚合/选中恢复统一在 reload 内完成）
	go startWatcher(mw, reload)

	// 显式显示窗口
	mw.Show()

	// 进入消息循环
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
	label.SetText(fmt.Sprintf("PID %d  %s  %s  ·  占用 %d 个端口", g.Pid, g.ProcessName, g.ProcessPath, g.PortCount))
}

// killSelected 终止当前主表选中行对应的进程，弹确认框。
// reload 用于杀完后刷新（主表自动维持过滤+排序）。
func killSelected(mw *walk.MainWindow, tv *walk.TableView, model *ProcessGroupModel, reload func()) {
	row := tv.CurrentIndex()
	g, ok := model.At(row)
	if !ok {
		walk.MsgBox(mw, "提示", "请先在主表选中一个进程", walk.MsgBoxIconInformation)
		return
	}
	// 二次确认（系统进程额外警告）
	msg := fmt.Sprintf("确定终止 PID %d (%s) 吗？\n该进程占用的 %d 个端口将一并释放。", g.Pid, g.ProcessName, g.PortCount)
	if g.Pid == 4 || g.ProcessName == "" {
		msg += "\n\n警告：这可能是系统进程，终止后可能影响系统稳定性。"
	}
	if walk.MsgBox(mw, "确认", msg, walk.MsgBoxYesNo|walk.MsgBoxIconWarning) != walk.DlgCmdYes {
		return
	}
	result := core.KillProcess(g.Pid)
	if result.Success {
		walk.MsgBox(mw, "成功", result.Message, walk.MsgBoxIconInformation)
		reload() // 刷新列表（聚合/排序/过滤自动维持）
	} else {
		walk.MsgBox(mw, "失败", result.Message, walk.MsgBoxIconError)
	}
}
