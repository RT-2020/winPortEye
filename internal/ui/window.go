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

	model := NewPortModel()
	var mw *walk.MainWindow
	var tv *walk.TableView
	var searchBox *walk.LineEdit
	var detailLabel *walk.Label

	// reload 重新扫描端口并推入 model（model 内部维持过滤+排序）。
	// 所有刷新路径（首次加载/刷新按钮/watcher/kill后）都走这个闭包。
	reload := func() {
		conns, err := core.ListConnections(core.KindAll)
		if err != nil {
			walk.MsgBox(mw, "错误", err.Error(), walk.MsgBoxIconError)
			return
		}
		model.SetRaw(conns)
		updateDetail(detailLabel, tv, model)
	}

	if err := (MainWindow{
		AssignTo: &mw,
		Title:    "PortEye 端口之眼",
		Size:     Size{Width: 960, Height: 600},
		MinSize:  Size{Width: 720, Height: 420},
		Layout:   VBox{MarginsZero: true, SpacingZero: true},
		Children: []Widget{
			// 顶部工具栏：搜索 + 刷新 + 设置
			Composite{
				Layout: HBox{MarginsZero: false, Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 6}, Spacing: 8},
				Children: []Widget{
					Label{Text: "搜索", MinSize: Size{Width: 36, Height: 0}},
					LineEdit{
						AssignTo:  &searchBox,
						CueBanner: "输入 端口 / 进程名 / 状态 过滤...",
						OnTextChanged: func() {
							kw := ""
							if searchBox != nil {
								kw = searchBox.Text()
							}
							// 只改过滤，不影响排序；model 自动重算视图
							model.SetKeyword(kw)
							updateDetail(detailLabel, tv, model)
						},
					},
					PushButton{Text: "刷新", OnClicked: func() { reload() }},
					PushButton{Text: "⚙ 设置", OnClicked: func() { showSettings(mw) }},
				},
			},
			// 端口列表（表头可点击排序，最后一列拉伸填满）
			TableView{
				AssignTo:            &tv,
				MinSize:             Size{Height: 320},
				LastColumnStretched: true,
				HeaderHidden:        false,
				Columns: []TableViewColumn{
					{Title: "协议", Width: 56},
					{Title: "本地地址", Width: 150},
					{Title: "端口", Width: 70},
					{Title: "状态", Width: 110},
					{Title: "进程", Width: 150},
					{Title: "路径", Width: 320},
				},
				Model: model,
				OnCurrentIndexChanged: func() {
					updateDetail(detailLabel, tv, model)
				},
			},
			// 底部：进程详情 + 操作按钮
			Composite{
				Layout: HBox{MarginsZero: false, Margins: Margins{Left: 10, Top: 6, Right: 10, Bottom: 10}, Spacing: 8},
				Children: []Widget{
					Label{AssignTo: &detailLabel, Text: "选中一行查看进程详情 · 点表头可排序"},
					HSpacer{},
					PushButton{
						Text: "终止选中进程",
						OnClicked: func() { killSelected(mw, tv, model, reload) },
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

	// 启动后台轮询（只推原始数据，过滤/排序由 model 维持）
	go startWatcher(mw, model)

	// 显式显示窗口
	mw.Show()

	// 进入消息循环
	mw.Run()
}

// updateDetail 更新底部详情标签为当前选中行。
func updateDetail(label *walk.Label, tv *walk.TableView, model *PortModel) {
	if label == nil || tv == nil {
		return
	}
	row := tv.CurrentIndex()
	c, ok := model.At(row)
	if !ok {
		label.SetText("选中一行查看进程详情 · 点表头可排序")
		return
	}
	label.SetText(fmt.Sprintf("PID %d  %s  %s", c.Pid, c.ProcessName, c.ProcessPath))
}

// killSelected 终止当前选中行对应的进程，弹确认框。
// reload 用于杀完后刷新（model 自动维持过滤+排序）。
func killSelected(mw *walk.MainWindow, tv *walk.TableView, model *PortModel, reload func()) {
	row := tv.CurrentIndex()
	c, ok := model.At(row)
	if !ok {
		walk.MsgBox(mw, "提示", "请先选中一行", walk.MsgBoxIconInformation)
		return
	}
	// 二次确认（系统进程额外警告）
	msg := fmt.Sprintf("确定终止 PID %d (%s) 吗？", c.Pid, c.ProcessName)
	if c.Pid == 4 || c.ProcessName == "" {
		msg += "\n\n警告：这可能是系统进程，终止后可能影响系统稳定性。"
	}
	if walk.MsgBox(mw, "确认", msg, walk.MsgBoxYesNo|walk.MsgBoxIconWarning) != walk.DlgCmdYes {
		return
	}
	result := core.KillProcess(c.Pid)
	if result.Success {
		walk.MsgBox(mw, "成功", result.Message, walk.MsgBoxIconInformation)
		reload() // 刷新列表（排序/过滤自动维持）
	} else {
		walk.MsgBox(mw, "失败", result.Message, walk.MsgBoxIconError)
	}
}
