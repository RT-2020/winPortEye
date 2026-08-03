package ui

import (
	"fmt"
	"os"
	"syscall"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows/registry"
)

// setupTray 创建系统托盘图标并绑定交互。
// 菜单项：显示窗口 / 开机自启(勾选) / 退出。
func setupTray(mw *walk.MainWindow) (*walk.NotifyIcon, error) {
	ni, err := walk.NewNotifyIcon(mw)
	if err != nil {
		return nil, err
	}

	// 图标：用 shell32.dll 里的网络图标（ID=275）兜底
	icon, err := loadShellIcon(275)
	if err != nil {
		icon = walk.IconInformation() // 退一步用内置信息图标
	}
	ni.SetIcon(icon)
	ni.SetToolTip("PortEye 端口之眼")
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
		}
	})
	menu.Actions().Add(actAutoStart)

	// 分隔线
	menu.Actions().Add(walk.NewSeparatorAction())

	// "退出"
	actQuit := walk.NewAction()
	actQuit.SetText("退出")
	actQuit.Triggered().Attach(func() {
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

// hookCloseButton 拦截关闭按钮：默认行为改为隐藏到托盘而非退出。
// 真正退出只能通过托盘菜单的"退出"。
func hookCloseButton(mw *walk.MainWindow) {
	mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		if reason == walk.CloseReasonUser {
			*canceled = true
			mw.Hide()
		}
	})
}

// ---- 开机自启（注册表 HKCU\...\Run）----

const autoStartKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const autoStartValue = "PortEye"

func setAutoStart(enable bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, autoStartKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if enable {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return k.SetStringValue(autoStartValue, fmt.Sprintf("%q", exe))
	}
	return k.DeleteValue(autoStartValue)
}

func isAutoStart() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, autoStartKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(autoStartValue)
	return err == nil
}

// loadShellIcon 从 shell32.dll 加载系统图标（按资源 ID）。
func loadShellIcon(resID uint32) (*walk.Icon, error) {
	h, err := syscall.LoadLibrary("shell32.dll")
	if err != nil {
		return nil, fmt.Errorf("加载 shell32.dll 失败: %w", err)
	}
	hIcon := win.LoadIcon(win.HINSTANCE(h), win.MAKEINTRESOURCE(uintptr(resID)))
	if hIcon == 0 {
		return nil, fmt.Errorf("LoadIcon 失败 resID=%d", resID)
	}
	return walk.NewIconFromHICON(hIcon)
}
