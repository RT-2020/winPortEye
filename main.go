// PortEye 入口：单 exe 两模式。
//   - --mcp：MCP stdio server（AI 客户端 spawn）
//   - 无参（默认）：GUI 窗口 + 后续托盘常驻
package main

import (
	"os"

	"win/internal/mcpserver"
	"win/internal/ui"
)

func main() {
	// 注：原先这里设了 debug.SetMemoryLimit(64MB)，但实测会触发频繁 GC 导致
	// UI 刷新时整片重绘闪烁（GC STW 暂停）。已移除，让 Go 用默认 GOGC 策略。
	// 实测常驻内存 ~20MB，远低于任何会引起问题的阈值，无需软上限。

	// 检测 --mcp 参数，分流到 MCP server
	for _, arg := range os.Args[1:] {
		if arg == "--mcp" {
			mcpserver.Run()
			return
		}
	}
	// 默认：启动 GUI
	ui.Run()
}
