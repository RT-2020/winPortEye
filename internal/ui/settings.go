package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"win/internal/core"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// mcpConfigJSON 生成当前 exe 路径对应的 MCP 客户端配置 JSON。
func mcpConfigJSON() string {
	exe, err := os.Executable()
	if err != nil {
		exe = `C:\path\to\porteye.exe`
	}
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"porteye": map[string]any{
				"command": exe,
				"args":    []string{"--mcp"},
			},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return string(data)
}

// jsonLineWidgets 把 JSON 文本按行拆成多个 Label 控件。
// 每行一个 Label，等宽字体，绝不自动折行 —— 解决 TextEdit 折行导致缩进错乱的问题。
func jsonLineWidgets(text string) []Widget {
	lines := strings.Split(text, "\n")
	widgets := make([]Widget, 0, len(lines))
	for _, line := range lines {
		// 保留前导空格用于缩进；空行用单空格占位（walk Label 空串会塌缩）
		display := line
		if display == "" {
			display = " "
		}
		widgets = append(widgets, Label{
			Text: display,
			Font: Font{Family: "Consolas", PointSize: 10},
			MinSize: Size{Width: 0, Height: 20},
		})
	}
	return widgets
}

// showSettings 显示设置对话框。
// 四个独立 Tab：MCP配置 / MCP状态 / 通用 / 关于。
func showSettings(owner walk.Form) {
	var dlg *walk.Dialog
	var autoStartCB *walk.CheckBox
	var closeBtn *walk.PushButton
	var statusLabel *walk.Label
	var checkBtn *walk.PushButton

	configText := mcpConfigJSON()
	jsonWidgets := jsonLineWidgets(configText)

	// 当前 exe 路径（自检用）
	exePath, _ := os.Executable()

	if err := (Dialog{
		AssignTo: &dlg,
		Title:    "设置",
		MinSize:  Size{Width: 600, Height: 520},
		Layout:   VBox{Margins: Margins{Left: 12, Top: 12, Right: 12, Bottom: 12}, Spacing: 10},
		Children: []Widget{
			TabWidget{
				Pages: []TabPage{
					// —— Tab 1: MCP 配置 ——
					TabPage{
						Title:  "MCP 配置",
						Layout: VBox{Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}, Spacing: 6},
						Children: append([]Widget{
							Label{Text: "将以下 JSON 合并进 AI 客户端配置后重启客户端即可使用："},
						},
							// JSON 逐行展示（等宽、不折行）
							append(jsonWidgets,
								Composite{
									Layout: HBox{MarginsZero: true, Spacing: 8, Margins: Margins{Left: 0, Top: 8, Right: 0, Bottom: 0}},
									Children: []Widget{
										PushButton{
											Text: "复制配置",
											OnClicked: func() {
												if err := walk.Clipboard().SetText(configText); err == nil {
													walk.MsgBox(dlg, "已复制", "配置已复制到剪贴板。", walk.MsgBoxIconInformation)
												}
											},
										},
										PushButton{
											Text: "接入说明",
											OnClicked: func() {
												walk.MsgBox(dlg, "客户端接入说明",
													"● Claude Desktop：编辑 %APPDATA%\\Claude\\claude_desktop_config.json\n"+
														"● Cursor：设置 → MCP → 添加 Server\n"+
														"● ZCode：编辑 MCP 配置文件\n\n"+
														"把上方 JSON 的 mcpServers 内容合并进配置，重启客户端。",
													walk.MsgBoxIconInformation)
											},
										},
										HSpacer{},
									},
								},
							)...),
					},
					// —— Tab 2: MCP 状态 ——
					TabPage{
						Title:  "MCP 状态",
						Layout: VBox{Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}, Spacing: 10},
						Children: []Widget{
							Label{Text: "点击下方按钮，本地启动一次 MCP server 并验证 5 个工具是否可用。", MinSize: Size{Height: 20}},
							Composite{
								Layout: HBox{MarginsZero: true, Spacing: 8},
								Children: []Widget{
									Label{AssignTo: &statusLabel, Text: "● 未检测", MinSize: Size{Width: 380, Height: 22}},
								},
							},
							PushButton{
								AssignTo: &checkBtn,
								Text:     "开始检测",
								OnClicked: func() {
									statusLabel.SetText("● 检测中...")
									statusLabel.SetTextColor(walk.RGB(120, 120, 120))
									checkBtn.SetEnabled(false)
									// 后台跑自检，避免卡 UI
									go func() {
										result := core.CheckMcpServer(exePath, 15*time.Second)
										dlg.Synchronize(func() {
											checkBtn.SetEnabled(true)
											if result.OK {
												statusLabel.SetText(fmt.Sprintf("● 正常  ·  %s  ·  耗时 %dms  ·  %s",
													result.Message, result.CostMs, strings.Join(result.Tools, " / ")))
												statusLabel.SetTextColor(walk.RGB(0, 140, 0))
											} else {
												statusLabel.SetText(fmt.Sprintf("● 异常  ·  %s  ·  耗时 %dms",
													result.Message, result.CostMs))
												statusLabel.SetTextColor(walk.RGB(200, 0, 0))
											}
										})
									}()
								},
							},
							Label{Text: "说明：本工具的 MCP server 由 AI 客户端按需启动（spawn --mcp 子进程），", MinSize: Size{Height: 18}},
							Label{Text: "因此不存在常驻服务。检测功能是临时启动一次以验证可用性。", MinSize: Size{Height: 18}},
						},
					},
					// —— Tab 3: 通用 ——
					TabPage{
						Title:  "通用",
						Layout: VBox{Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}, Spacing: 8},
						Children: []Widget{
							CheckBox{
								AssignTo: &autoStartCB,
								Text:     "开机自启动（后台运行到托盘）",
								Checked:  isAutoStart(),
								OnClicked: func() {
									if autoStartCB == nil {
										return
									}
									enable := autoStartCB.Checked()
									if err := setAutoStart(enable); err != nil {
										walk.MsgBox(dlg, "错误", "设置开机自启失败: "+err.Error(), walk.MsgBoxIconError)
										autoStartCB.SetChecked(!enable)
									}
								},
							},
							Label{Text: "启动时自动加载到托盘，不显示主窗口。", MinSize: Size{Height: 18}},
							// 清理本机数据区块（与开机自启区块风格一致）
							Label{Text: " ", MinSize: Size{Height: 6}},
							Label{Text: "清理软件运行产生的本机数据：", MinSize: Size{Height: 18}},
							PushButton{
								Text: "清理本机数据…",
								OnClicked: func() {
									// 确认框：列出将删除的内容，强调不删 exe 本体
									msg := "将删除以下内容：\n" +
										"  • %APPDATA%\\PortEye 日志目录\n" +
										"  • 开机自启项\n" +
										"  • 更新下载残留文件\n" +
										"  • 临时更新脚本\n\n" +
										"程序本体不会被删除。\n\n是否继续？"
									if walk.MsgBox(dlg, "清理本机数据", msg,
										walk.MsgBoxYesNo|walk.MsgBoxIconWarning) != walk.DlgCmdYes {
										return
									}
									// 磁盘 IO 较快，同步执行（参照更新按钮同线程模型）
									dir, err := exeDir()
									if err != nil {
										walk.MsgBox(dlg, "清理失败", "无法定位程序目录: "+err.Error(), walk.MsgBoxIconError)
										return
									}
									r := core.CleanupLocalData(dir)
									walk.MsgBox(dlg, "清理完成", formatCleanupResult(r), walk.MsgBoxIconInformation)
								},
							},
							Label{Text: "（不删除程序本体；如需卸载，直接删除软件所在目录即可）", MinSize: Size{Height: 18}},
						},
					},
					// —— Tab 4: 关于 ——
					TabPage{
						Title:  "关于",
						Layout: VBox{Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}, Spacing: 6},
						Children: []Widget{
							Label{Text: fmt.Sprintf("PortEye v1.0   (%s/%s)", runtime.GOOS, runtime.GOARCH), MinSize: Size{Height: 20}},
							Label{Text: "PortEye · Windows 端口监控工具 · 支持 MCP", MinSize: Size{Height: 18}},
							Label{Text: " ", MinSize: Size{Height: 6}},
							Label{Text: "MCP 工具：", MinSize: Size{Height: 18}},
							Label{Text: "  · list_ports   列出端口连接及占用进程", MinSize: Size{Height: 18}},
							Label{Text: "  · find_port    查找指定端口被谁占用", MinSize: Size{Height: 18}},
							Label{Text: "  · get_process  查询进程详情", MinSize: Size{Height: 18}},
							Label{Text: "  · kill_process 终止进程", MinSize: Size{Height: 18}},
							Label{Text: "  · kill_by_port 按端口终止", MinSize: Size{Height: 18}},
						},
					},
				},
			},
			// 底部关闭按钮（所有 Tab 共用）
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{
						AssignTo:  &closeBtn,
						Text:      "关闭",
						OnClicked: func() { dlg.Accept() },
					},
				},
			},
		},
	}.Create(owner)); err != nil {
		walk.MsgBox(owner, "错误", "打开设置失败: "+err.Error(), walk.MsgBoxIconError)
		return
	}

	dlg.Run()
}

// formatCleanupResult 把 CleanupResult 格式化成结果弹窗文案。
// 成功项标 ✓，无残留项标 ·，失败项连同原因列出（✗），结尾给出卸载提示。
func formatCleanupResult(r core.CleanupResult) string {
	var sb strings.Builder
	switch {
	case r.LogDirRemoved:
		sb.WriteString("✓ 日志目录已清理\n")
	case r.LogDirAbsent:
		sb.WriteString("· 无日志需清理\n")
	default:
		sb.WriteString("✗ 日志目录未清理\n")
	}
	if r.AutoStartRemoved {
		sb.WriteString("✓ 开机自启已关闭\n")
	} else {
		sb.WriteString("✗ 开机自启未关闭\n")
	}
	if len(r.UpdateFilesRemoved) > 0 {
		sb.WriteString(fmt.Sprintf("✓ 更新残留已删 %d 个文件\n", len(r.UpdateFilesRemoved)))
	} else {
		sb.WriteString("· 无更新残留需清理\n")
	}
	if len(r.TempBatsRemoved) > 0 {
		sb.WriteString(fmt.Sprintf("✓ 临时脚本已删 %d 个\n", len(r.TempBatsRemoved)))
	} else {
		sb.WriteString("· 无临时脚本需清理\n")
	}
	if len(r.Errors) > 0 {
		sb.WriteString("\n失败项：\n")
		for _, e := range r.Errors {
			sb.WriteString("✗ " + e + "\n")
		}
	}
	sb.WriteString("\n如需卸载，直接删除软件所在目录即可。")
	return sb.String()
}
