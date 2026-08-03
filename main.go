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
