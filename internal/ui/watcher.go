package ui

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/lxn/walk"
)

// startWatcher 后台定时在 UI 线程触发刷新回调。
//
// 设计说明：watcher 不直接碰 model，也不做扫描——它只负责"定时 + 切回 UI 线程"，
// 实际的扫描/聚合/选中恢复/子表刷新都在 onTick 回调（即 window.go 的 reload）里完成。
// 这样 watcher 路径和手动点"刷新"按钮走完全相同的逻辑，行为一致。
func startWatcher(mw *walk.MainWindow, onTick func()) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
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
