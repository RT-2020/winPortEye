package ui

import (
	"win/internal/core"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

// setupTray 创建系统托盘图标并绑定交互。
// 菜单项：显示窗口 / 开机自启(勾选) / 退出。
// onQuit 在「退出」菜单触发、真正退出进程前调用（用于取消后台下载 ctx 等；
// nil 表示无回调）。窗口关闭=藏托盘不会触发 onQuit。
func setupTray(mw *walk.MainWindow, appIcon *walk.Icon, onQuit func()) (*walk.NotifyIcon, error) {
	ni, err := walk.NewNotifyIcon(mw)
	if err != nil {
		return nil, err
	}

	// 图标：复用主窗口加载的嵌入图标实例（walk SetIcon 不转移所有权，
	// 共享同一 *walk.Icon 安全，避免重复加载泄漏 HICON）；
	// 若未加载到则退回 walk 内置信息图标，保证托盘始终有图标。
	icon := appIcon
	if icon == nil {
		icon = walk.IconInformation()
	}
	ni.SetIcon(icon)
	ni.SetToolTip("PortEye")
	ni.SetVisible(true)

	// 左键单击：切换窗口显示/隐藏
	ni.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			toggleWindow(mw)
		}
	})

	// 右键菜单
	menu := ni.ContextMenu()

	// "显示窗口"
	actShow := walk.NewAction()
	actShow.SetText("显示窗口")
	actShow.Triggered().Attach(func() {
		mw.Show()
		win.SetForegroundWindow(mw.Handle())
	})
	menu.Actions().Add(actShow)

	// "开机自启"（勾选项）
	actAutoStart := walk.NewAction()
	actAutoStart.SetCheckable(true)
	actAutoStart.SetChecked(isAutoStart())
	actAutoStart.SetText("开机自启")
	actAutoStart.Triggered().Attach(func() {
		enable := actAutoStart.Checked()
		if err := setAutoStart(enable); err != nil {
			walk.MsgBox(mw, "错误", err.Error(), walk.MsgBoxIconError)
			// 回滚勾选态到实际生效状态（写注册表失败后勾选态与事实脱节）
			actAutoStart.SetChecked(isAutoStart())
		}
	})
	menu.Actions().Add(actAutoStart)

	// 分隔线
	menu.Actions().Add(walk.NewSeparatorAction())

	// "退出"
	actQuit := walk.NewAction()
	actQuit.SetText("退出")
	actQuit.Triggered().Attach(func() {
		if onQuit != nil {
			onQuit() // 进程真实退出前取消后台任务（如下载 ctx）
		}
		ni.Dispose()       // 先移除托盘图标
		walk.App().Exit(0) // 真正退出
	})
	menu.Actions().Add(actQuit)

	return ni, nil
}

// toggleWindow 切换主窗口显示/隐藏（左键单击托盘触发）。
func toggleWindow(mw *walk.MainWindow) {
	if mw.Visible() {
		mw.Hide()
	} else {
		mw.Show()
		win.SetForegroundWindow(mw.Handle())
	}
}

// realCloseRequested 为 true 时 hookCloseButton 放行，真正退出程序。
// 目前只有「提权重启」路径会置位（旧实例必须退出，不能藏进托盘残留）。
var realCloseRequested bool

// requestRealClose 标记下一次关闭为真正退出，绕过关窗拦截。
func requestRealClose() {
	realCloseRequested = true
}

// hookCloseButton 拦截关闭按钮：默认行为改为隐藏到托盘而非退出。
// 真正退出只能通过托盘菜单的"退出"。
// 注意：当前版本的 walk 库有缺陷——Closing 事件的 reason 恒为 CloseReasonUnknown
// （其 form.go 在 WM_CLOSE 处理里先重置 closeReason 再发布事件，WM_SYSCOMMAND
// SC_CLOSE 置的 CloseReasonUser 会被覆盖），所以这里无法按来源区分，一律拦截；
// 需要真正退出的路径（提权重启）先调 requestRealClose 放行。
func hookCloseButton(mw *walk.MainWindow) {
	mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if realCloseRequested {
			return
		}
		*canceled = true
		mw.Hide()
	})
}

// ---- 开机自启（注册表 HKCU\...\Run）----
// 注册表读写逻辑已抽到 core（见 core/cleanup.go），此处仅转发，保持托盘菜单
// 与设置面板调用签名/行为不变。共用方：托盘菜单勾选项、设置面板勾选项、清理本机数据。

func setAutoStart(enable bool) error { return core.SetAutoStart(enable) }
func isAutoStart() bool              { return core.IsAutoStart() }

// loadAppIcon 加载嵌入 exe 的项目图标（RT_GROUP_ICON 资源 ID=1，
// 由 winres/winres.json 在构建期嵌入）。失败时返回错误，由调用方兜底。
func loadAppIcon() (*walk.Icon, error) {
	return walk.NewIconFromResourceId(1)
}
