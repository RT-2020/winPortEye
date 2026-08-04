// PortEye 入口：单 exe 两模式。
//   - --mcp：MCP stdio server（AI 客户端 spawn）
//   - 无参（默认）：GUI 窗口 + 后续托盘常驻
package main

import (
	"os"
	"runtime/debug"

	"win/internal/mcpserver"
	"win/internal/ui"
)

func main() {
	// 设 64MB 内存软上限：让 Go GC 更积极回收，防止常驻内存随扫描累积膨胀。
	// 这是软限制（SoftMemoryLimit），不会硬性 OOM——超过时只是 GC 频率提高。
	// GUI 主程序 + 端口扫描的实际占用远低于此值（实测扫描峰值 <1MB）。
	debug.SetMemoryLimit(64 << 20)

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
