package core

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// 提权相关 Win32 API。
var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcessToken  = kernel32.NewProc("OpenProcessToken")
	procGetTokenInformation = kernel32.NewProc("GetTokenInformation")
)

const (
	tokenElevation = 20 // TOKEN_ELEVATION 的 TokenInformationClass
)

// IsElevated 检测当前进程是否以管理员权限运行。
// 通过 OpenProcessToken + GetTokenInformation(TokenElevation) 判断。
// 任何 Win32 调用失败都按"未提权"处理（保守，不误判为已提权）。
func IsElevated() bool {
	// GetCurrentProcess 返回伪句柄 -1，直接用
	var token syscall.Token
	// OpenProcessToken(HANDLE ProcessHandle, DWORD DesiredAccess, PHANDLE TokenHandle)
	ret, _, _ := procOpenProcessToken.Call(
		uintptr(0xFFFFFFFF), // GetCurrentProcess() 伪句柄
		uintptr(0x0008),     // TOKEN_QUERY
		uintptr(unsafe.Pointer(&token)),
	)
	if ret == 0 {
		return false
	}
	defer token.Close()

	// 查询 TOKEN_ELEVATION（一个 DWORD）
	var elevated uint32
	var returned uint32
	ret, _, _ = procGetTokenInformation.Call(
		uintptr(token),
		uintptr(tokenElevation),
		uintptr(unsafe.Pointer(&elevated)),
		unsafe.Sizeof(elevated),
		uintptr(unsafe.Pointer(&returned)),
	)
	if ret == 0 {
		return false
	}
	return elevated != 0
}

// RelaunchElevated 以管理员身份重新启动自身 exe。
// 用 ShellExecuteW 的 "runas" 动词拉起，会弹出 UAC 对话框。
// 调用方在成功发起后应自行退出当前进程。
// exePath 为空时自动取当前进程可执行文件路径。
func RelaunchElevated(exePath string) error {
	if exePath == "" {
		// os.Executable 取当前 exe 路径（symlink 等情况由 EvalSymlinks 规整）
		p, err := os.Executable()
		if err != nil {
			return fmt.Errorf("获取当前 exe 路径失败: %w", err)
		}
		exePath = p
	}

	verb := syscall.StringToUTF16Ptr("runas")
	file := syscall.StringToUTF16Ptr(exePath)
	params := syscall.StringToUTF16Ptr("")
	cwd := syscall.StringToUTF16Ptr("")

	const swShownormal = 1
	ret, _, _ := procShellExec.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)),
		uintptr(unsafe.Pointer(cwd)),
		uintptr(swShownormal),
	)
	// HINSTANCE > 32 表示成功
	if ret <= 32 {
		return fmt.Errorf("提权重启失败（可能用户拒绝 UAC），错误码 %d", int(ret))
	}
	return nil
}
