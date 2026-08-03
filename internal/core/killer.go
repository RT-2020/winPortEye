package core

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/shirou/gopsutil/v4/process"
)

var (
	shell32       = syscall.NewLazyDLL("shell32.dll")
	procShellExec = shell32.NewProc("ShellExecuteW")
)

// KillProcess 两级杀进程：
//  1. 先尝试直接杀（用户态进程成功，零 UAC，立即返回）
//  2. 直接杀失败（系统进程/其他用户进程）→ 用 ShellExecute runas 提权 taskkill（弹一次 UAC）
//
// 注意：runas 提权会弹出 UAC 对话框；MCP 模式（无交互桌面）下 runas 会失败，
// 调用方应据此返回"需在桌面会话手动处理"的提示。
func KillProcess(pid int32) KillResult {
	if pid <= 0 {
		return KillResult{Success: false, Message: "无效的 PID", Pid: pid}
	}

	// 第 1 级：直接杀。gopsutil 的 process.Kill 内部调 TerminateProcess。
	if exists, _ := process.PidExists(pid); !exists {
		return KillResult{Success: false, Message: "进程不存在", Pid: pid}
	}
	if p, err := process.NewProcess(pid); err == nil {
		if err := p.Kill(); err == nil {
			return KillResult{Success: true, Message: "已终止（直接）", Pid: pid}
		}
		// 失败则进入第 2 级
	}

	// 第 2 级：提权 taskkill /F /PID（弹 UAC）
	if err := killElevated(pid); err != nil {
		return KillResult{
			Success: false,
			Message: fmt.Sprintf("终止失败（系统进程需提权，已弹 UAC）: %v", err),
			Pid:     pid,
		}
	}
	return KillResult{Success: true, Message: "已发起终止（提权，UAC 已弹）", Pid: pid}
}

// killElevated 用 ShellExecute 的 runas 动词拉起提权的 taskkill 子进程。
// 会触发 UAC 弹窗。返回 nil 表示提权命令已成功发起。
//
// 注意：ShellExecuteW 返回值是 HINSTANCE，<=32 表示失败。
// runas 是异步的——这里只确认"提权命令已发起"，调用方若需确认进程已死，
// 应在调用后轮询端口表/进程列表。
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
	return nil
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
