// PortEye 入口：单 exe 两模式。
//   - --mcp：MCP stdio server（AI 客户端 spawn）
//   - --version / --help：控制台输出后退出（发行 exe 是 windowsgui 子系统，
//     无自带控制台，输出前先 attachConsole 挂到父进程控制台）
//   - 无参（默认）：GUI 窗口 + 后续托盘常驻
package main

import (
	"fmt"
	"os"
	"syscall"

	"win/internal/mcpserver"
	"win/internal/ui"
)

// version 当前程序版本号，供「检查更新」与版本比较。
// 默认 "dev"（go run / 未注入时）；正式构建用 -ldflags "-X main.version=0.3.0" 注入。
// MCP 模式（--mcp）天然不进入更新流程，此变量仅 GUI 路径使用。
var version = "dev"

func main() {
	// 注：原先这里设了 debug.SetMemoryLimit(64MB)，但实测会触发频繁 GC 导致
	// UI 刷新时整片重绘闪烁（GC STW 暂停）。已移除，让 Go 用默认 GOGC 策略。
	// 实测常驻内存 ~20MB，远低于任何会引起问题的阈值，无需软上限。

	// 参数解析：--mcp 绝对优先（无论出现在参数表哪个位置，AI 客户端 spawn 时
	// 只关心 MCP 模式；先独立扫描避免 --help --mcp 这类组合被 --help 抢先）。
	for _, arg := range os.Args[1:] {
		if arg == "--mcp" {
			mcpserver.Run(version)
			return
		}
	}
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--version":
			attachConsole()
			fmt.Println("porteye", version)
			return
		case "--help":
			attachConsole()
			fmt.Println("用法: porteye [选项]")
			fmt.Println("  --mcp      启动 MCP server（stdio，供 AI 客户端调用）")
			fmt.Println("  --version  显示版本号")
			fmt.Println("  --help     显示本帮助")
			fmt.Println("  无参数     启动图形界面")
			return
		}
	}
	// 默认：启动 GUI
	ui.Run(version)
}

// attachConsole 把 windowsgui 子系统 exe 的输出附加到父进程控制台。
//
// 发行 exe 是 -H windowsgui 编译（无控制台），--version/--help 的打印默认不可见；
// 这里 AttachConsole(ATTACH_PARENT_PROCESS) 挂到父进程控制台后，
// 再用 GetStdHandle 重取标准输出句柄重设 os.Stdout。
// 双击运行时无父控制台，AttachConsole 失败 → 静默返回（打印自然丢弃）。
// 绝不能用于 --mcp 路径：stdio 是 JSON-RPC 协议通道，输出必须原样走管道。
func attachConsole() {
	const (
		attachParentProcess = ^uintptr(0)  // ATTACH_PARENT_PROCESS (DWORD)-1
		stdOutputHandle     = ^uintptr(10) // STD_OUTPUT_HANDLE (DWORD)-11
		invalidHandle       = ^uintptr(0)  // INVALID_HANDLE_VALUE
	)
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	procAttachConsole := kernel32.NewProc("AttachConsole")
	procGetStdHandle := kernel32.NewProc("GetStdHandle")

	ret, _, _ := procAttachConsole.Call(attachParentProcess)
	if ret == 0 {
		return // 无父控制台（如双击运行），静默
	}
	h, _, _ := procGetStdHandle.Call(stdOutputHandle)
	if h == 0 || h == invalidHandle {
		return
	}
	os.Stdout = os.NewFile(h, "/dev/stdout")
}
