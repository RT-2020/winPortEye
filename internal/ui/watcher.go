package ui

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"win/internal/core"

	"github.com/lxn/walk"
)

// startWatcher 后台定时轮询端口，在 UI 线程安全地刷新 Model。
// model 内部自动维持「关键字过滤 + 排序」，所以这里只推原始全量数据。
func startWatcher(mw *walk.MainWindow, model *PortModel) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		conns, err := core.ListConnections(core.KindAll)
		if err != nil {
			continue
		}
		// 切回 UI 线程更新（walk 控件非线程安全）
		mw.Synchronize(func() {
			model.SetRaw(conns)
		})
	}
}

// setLogFile 把日志写到 %APPDATA%\PortMonitor\logs\app.log
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
