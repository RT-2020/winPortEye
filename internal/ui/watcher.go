package ui

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"win/internal/core"

	"github.com/lxn/walk"
)

// startWatcher 后台定时在 UI 线程触发刷新回调。
//
// 设计说明：watcher 不直接碰 model，也不做扫描——它只负责"定时 + 切回 UI 线程"，
// 实际的扫描/聚合/选中恢复/子表刷新都在 onTick 回调（即 window.go 的 reload）里完成。
// 这样 watcher 路径和手动点"刷新"按钮走完全相同的逻辑，行为一致。
//
// 额外职责：每 30 秒（10 个 tick）在【后台 goroutine】里异步刷新端口排除范围缓存。
// 这部分不碰 UI 控件，且 netsh 较慢，放在后台 goroutine 避免阻塞 UI。
func startWatcher(mw *walk.MainWindow, onTick func()) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	tick := 0
	for range ticker.C {
		tick++
		// 每 30 秒（10 个 tick）异步刷新排除范围缓存（后台 goroutine，不阻塞 UI）
		if tick%10 == 0 {
			go func() { _, _, _ = core.ListExcludedPortRanges() }()
		}
		mw.Synchronize(onTick)
	}
}

// setLogFile 把日志写到 %APPDATA%\PortEye\logs\app.log
// GUI 模式不写 stderr（避免弹控制台）。
func setLogFile() {
	dir := filepath.Join(os.Getenv("APPDATA"), "PortEye", "logs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "app.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	log.SetOutput(f)
}
