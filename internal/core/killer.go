package core

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

var (
	shell32       = syscall.NewLazyDLL("shell32.dll")
	procShellExec = shell32.NewProc("ShellExecuteW")
)

// KillProcess 两级杀进程：
//  1. 先尝试直接杀（用户态进程成功，零 UAC，立即返回）
//  2. 直接杀失败（系统进程/其他用户进程）→ 用 ShellExecute runas 提权 taskkill（弹一次 UAC），
//     并轮询进程退出确认结果
//
// 注意：runas 提权会弹出 UAC 对话框；MCP 模式（无交互桌面）下 runas 会失败，
// 调用方应据此返回"需在桌面会话手动处理"的提示。
//
// 阻塞语义：第 2 级提权路径最长阻塞约 8 秒（轮询确认进程退出），
// 调用方不得在 UI 线程同步调用提权路径（GUI 下应放 goroutine，MCP 下由 handler 天然承担）。
//
// 第 1 级用原生 TerminateProcess（替换 gopsutil process.Kill，底层同一 API）。
func KillProcess(pid int32) KillResult {
	if pid <= 0 {
		return KillResult{Success: false, Message: "无效的 PID", Pid: pid}
	}

	// 第 1 级：直接杀（原生 TerminateProcess）。
	if !processExists(pid) {
		return KillResult{Success: false, Message: "进程不存在", Pid: pid}
	}
	if err := terminateProcess(pid); err == nil {
		return KillResult{Success: true, Message: "已终止（直接）", Pid: pid}
	}
	// 失败则进入第 2 级

	// 第 2 级：提权 taskkill /F /PID（弹 UAC）+ 轮询确认进程已退出。
	// err==nil 表示进程已确认消失；err!=nil 表示用户拒绝 UAC / 无桌面 / 超时仍存活。
	if err := killElevated(pid); err != nil {
		return KillResult{Success: false, Message: err.Error(), Pid: pid}
	}
	return KillResult{Success: true, Message: "已终止（提权）", Pid: pid}
}

// killElevated 用 ShellExecute 的 runas 动词拉起提权的 taskkill 子进程。
// 会触发 UAC 弹窗。
//
// ShellExecuteW 返回值是 HINSTANCE：<=32 表示失败（用户拒绝 UAC / 无桌面），
// 立即返回错误，不轮询。
//
// 返回 >32（提权命令已成功拉起）后轮询 processExists 确认结果：
// 每 250ms 一次，最多 8 秒；进程消失返回 nil（提权终止成功）；
// 超时仍存活返回错误（进程可能受保护，taskkill 也无权终止）。
//
// 注意：本函数最长阻塞约 8 秒，调用方不得在 UI 线程同步调用。
func killElevated(pid int32) error {
	verb := syscall.StringToUTF16Ptr("runas")
	file := syscall.StringToUTF16Ptr("taskkill.exe")
	params := syscall.StringToUTF16Ptr(fmt.Sprintf("/F /PID %d", pid))
	cwd := syscall.StringToUTF16Ptr("")

	const swHide = 0 // 隐藏 taskkill 的控制台窗口
	ret, _, _ := procShellExec.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)),
		uintptr(unsafe.Pointer(cwd)),
		uintptr(swHide),
	)
	// HINSTANCE > 32 表示成功
	if ret <= 32 {
		return fmt.Errorf("ShellExecute runas 失败（可能用户拒绝 UAC 或无交互桌面），错误码 %d", int(ret))
	}

	// 轮询确认进程已退出：每 250ms 一次，最多 8 秒
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if !processExists(pid) {
			return nil // 进程已消失，提权终止成功
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("已发起提权请求但进程仍存活（进程可能受保护，请刷新列表确认）")
}

// KillByPort 先查占用端口的进程，再逐一杀（去重 PID）。
// 返回每个受影响 PID 的结果。
func KillByPort(port uint16, kind FilterKind) ([]KillResult, error) {
	conns, err := FindPort(port, kind)
	if err != nil {
		return nil, err
	}
	if len(conns) == 0 {
		return []KillResult{{Success: false, Message: "没有进程占用该端口"}}, nil
	}
	seen := make(map[int32]bool, len(conns))
	results := make([]KillResult, 0, len(conns))
	for _, c := range conns {
		if c.Pid <= 0 || seen[c.Pid] {
			continue
		}
		seen[c.Pid] = true
		results = append(results, KillProcess(c.Pid))
	}
	return results, nil
}
