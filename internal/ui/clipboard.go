package ui

import (
	"fmt"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"github.com/lxn/win"
)

// 剪贴板写入直接调 Win32 API（OpenClipboard → EmptyClipboard → SetClipboardData）。
// 不沿用 walk.Clipboard().SetText：walk 的实现 OpenClipboard 后不 EmptyClipboard，
// 旧格式（如 CF_TEXT）残留，目标程序读到 ANSI 旧内容时表现为「复制了但粘贴出旧数据」。
//
// lxn/win 未封装 WideCharToMultiByte，这里单独声明（UTF-16 → 系统 ANSI 代码页）。
var procWideCharToMultiByte = syscall.NewLazyDLL("kernel32.dll").NewProc("WideCharToMultiByte")

const (
	clipCPACP       = 0           // CP_ACP（系统 ANSI 代码页）
	clipW2MNullTerm = ^uintptr(0) // cchWideChar 传 -1：宽字符串含结尾 NUL
)

// copyConfigToClipboard 原子地把文本写入系统剪贴板。
// 先 EmptyClipboard 清掉全部旧格式，再同时写入 UTF-16 与 ANSI 两种文本格式，
// 兼容只读 ANSI 格式的旧程序；ANSI 格式写入失败不视为复制失败。
func copyConfigToClipboard(text string) error {
	if !win.OpenClipboard(0) { // hwnd 传 NULL：与当前任务关联
		return fmt.Errorf("OpenClipboard 失败")
	}
	defer win.CloseClipboard()
	if !win.EmptyClipboard() {
		return fmt.Errorf("EmptyClipboard 失败")
	}

	// UTF-16 格式（现代程序优先读取）
	u16 := utf16.Encode([]rune(text + "\x00"))
	hMem := win.GlobalAlloc(win.GMEM_MOVEABLE, uintptr(len(u16)*2))
	if hMem == 0 {
		return fmt.Errorf("GlobalAlloc(UTF-16) 失败")
	}
	p := win.GlobalLock(hMem)
	if p == nil {
		win.GlobalFree(hMem)
		return fmt.Errorf("GlobalLock(UTF-16) 失败")
	}
	win.MoveMemory(p, unsafe.Pointer(&u16[0]), uintptr(len(u16)*2))
	win.GlobalUnlock(hMem)
	if win.SetClipboardData(win.CF_UNICODETEXT, win.HANDLE(hMem)) == 0 {
		win.GlobalFree(hMem)
		return fmt.Errorf("SetClipboardData(UTF-16) 失败")
	}

	// ANSI 格式（兼容只读 CF_TEXT 的旧程序；系统代码页外的字符会替换为 ?）
	n, _, _ := procWideCharToMultiByte.Call(clipCPACP, 0,
		uintptr(unsafe.Pointer(&u16[0])), clipW2MNullTerm, 0, 0, 0, 0)
	if n > 0 {
		if hAnsi := win.GlobalAlloc(win.GMEM_MOVEABLE, n); hAnsi != 0 {
			if pAnsi := win.GlobalLock(hAnsi); pAnsi != nil {
				procWideCharToMultiByte.Call(clipCPACP, 0,
					uintptr(unsafe.Pointer(&u16[0])), clipW2MNullTerm, uintptr(pAnsi), n, 0, 0)
				win.GlobalUnlock(hAnsi)
				if win.SetClipboardData(win.CF_TEXT, win.HANDLE(hAnsi)) == 0 {
					win.GlobalFree(hAnsi)
				}
			} else {
				win.GlobalFree(hAnsi)
			}
		}
	}
	return nil
}
