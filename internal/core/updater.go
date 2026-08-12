// updater.go 实现 PortEye 的「检查更新 + 下载 + 替换自身」纯逻辑层。
//
// 分层约定：本文件只做网络/磁盘/进程编排，不 import ui/walk，
// 所有 UI 回调由调用方（ui 包）经 walk.Synchronize 切回 UI 线程后执行。
//
// 更新物料约定（与仓库 GitHub Release 发行物一致）：
//   - 发行 zip 内含两个文件：porteye.exe 与 porteye.exe.manifest
//   - 解压到 exe 同目录，命名为 porteye_update.exe / porteye_update.exe.manifest
//     （加 _update 后缀，避免与正在运行的主 exe 同名导致共享冲突）
//   - porteye_update.version 为 sidecar：内容一行 tag，标记「已下载就绪」
package core

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// githubLatestURL 指向仓库最新 Release API。设为变量便于测试注入 httptest 地址。
var githubLatestURL = "https://api.github.com/repos/RT-2020/winPortEye/releases/latest"

// UpdateInfo 描述一个可用的 GitHub Release 更新。
type UpdateInfo struct {
	TagName     string // 版本号，如 v0.3.0
	Changelog   string // Release body（更新日志原文）
	DownloadURL string // 资产 browser_download_url
	AssetName   string // 资产文件名（用于判断 .zip / .exe）
	Size        int64  // 资产字节数
}

// githubRelease / githubAsset 仅用于解析 GitHub Releases API 的 JSON 响应。
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Body    string        `json:"body"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// versionNonComparable 是 CompareVersions 的哨兵返回值，表示版本号含非数字段
// （如 1.0-beta）无法比较。CheckLatest 据此按「无更新」静默处理。
const versionNonComparable = -2

// CompareVersions 比较两个点分数字版本号 a、b，返回：
//   -1  a < b
//    0  a == b
//    1  a > b
//   -2  不可比较（任一段非纯数字，如 1.0-beta / rc）
//
// 规则：去掉可选的 v/V 前缀，按 "." 切分，逐段按整数比较；缺段视为 0
// （故 "1.2" 与 "1.2.0" 相等）。任何一段非纯数字 → 返回 -2。
// 预发布标记（-beta 等）一律按不可比较处理，避免误判升级方向。
//
// 实现要点：先把两个版本号整体解析成 int 切片（任一段失败即不可比较），
// 再逐段比较——否则在前导段就已分出大小时会跳过对后续段的数字校验，
// 导致 "1.0-beta" 因首段 1>0 被误判为"大于"而非"不可比较"。
func CompareVersions(a, b string) int {
	na, ok := parseVersionSegments(trimVersionPrefix(a))
	if !ok {
		return versionNonComparable
	}
	nb, ok := parseVersionSegments(trimVersionPrefix(b))
	if !ok {
		return versionNonComparable
	}
	n := len(na)
	if len(nb) > n {
		n = len(nb)
	}
	for i := 0; i < n; i++ {
		va, vb := 0, 0
		if i < len(na) {
			va = na[i]
		}
		if i < len(nb) {
			vb = nb[i]
		}
		if va < vb {
			return -1
		}
		if va > vb {
			return 1
		}
	}
	return 0
}

// trimVersionPrefix 去掉版本号开头的 v/V 前缀（"v0.3.0" → "0.3.0"）。
func trimVersionPrefix(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 0 && (s[0] == 'v' || s[0] == 'V') {
		return s[1:]
	}
	return s
}

// parseVersionSegments 把 "1.2.0" 解析为 [1,2,0]；空段视为 0；
// 任一段非纯数字（如 "0-beta"）返回 ok=false（不可比较）。
func parseVersionSegments(s string) ([]int, bool) {
	segs := strings.Split(s, ".")
	out := make([]int, len(segs))
	for i, seg := range segs {
		if seg == "" {
			out[i] = 0 // "1..2" / 末尾空段按 0 容错
			continue
		}
		n, err := strconv.Atoi(seg)
		if err != nil {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

// pickAsset 从 Release 资产里挑选下载目标：
//   - 优先名字以 .zip 结尾的（win.zip 是标准发行物）
//   - 其次裸 .exe
//   - 都无匹配返回 ok=false（调用方按无更新静默）
func pickAsset(assets []githubAsset) (githubAsset, bool) {
	for _, a := range assets {
		if strings.EqualFold(filepath.Ext(a.Name), ".zip") {
			return a, true
		}
	}
	for _, a := range assets {
		if strings.EqualFold(filepath.Ext(a.Name), ".exe") {
			return a, true
		}
	}
	return githubAsset{}, false
}

// CheckLatest 请求 GitHub 最新 Release，判断是否比 currentVersion 新。
//
// 语义（严格）：有新版本返回 (*UpdateInfo, nil)；无更新返回 (nil, nil)；
// 网络/HTTP 非 200/解析失败/无匹配资产返回 err。
// 版本不可比较（含预发布标记）按无更新 (nil, nil) 静默，绝不打扰用户。
//
// client 总超时应由调用方限制在 ≤10s。
func CheckLatest(client *http.Client, currentVersion string) (*UpdateInfo, error) {
	req, err := http.NewRequest(http.MethodGet, githubLatestURL, nil)
	if err != nil {
		return nil, err
	}
	// GitHub API 强制要求 User-Agent，否则 403 拒绝
	req.Header.Set("User-Agent", "PortEye-Updater")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api 返回状态 %d", resp.StatusCode)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("解析 release json 失败: %w", err)
	}

	asset, ok := pickAsset(rel.Assets)
	if !ok {
		return nil, fmt.Errorf("release 无匹配资产（.zip/.exe）")
	}

	// 版本比较：远端严格大于当前才算有更新。
	// 不可比较（-2）或远端不大于当前，一律按无更新静默。
	if CompareVersions(rel.TagName, currentVersion) != 1 {
		return nil, nil
	}

	return &UpdateInfo{
		TagName:     rel.TagName,
		Changelog:   rel.Body,
		DownloadURL: asset.BrowserDownloadURL,
		AssetName:   asset.Name,
		Size:        asset.Size,
	}, nil
}

// Download 把 url 内容下载到 destPart 文件，并周期性回调进度。
//
// 超时策略（针对国内访问 GitHub 慢）：
//   - 不设总超时（大文件 + 慢链路不应被误杀）
//   - 用 ctx 做取消（调用方在进程退出/用户放弃时取消）
//   - Transport.ResponseHeaderTimeout（调用方在 client 上配 ≤15s）兜住「连上但首字节不来」
//
// 写完后若 expectedSize>0，校验落盘字节数一致，不符报错（防止半截下载被当成功）。
func Download(ctx context.Context, client *http.Client, url, destPart string, expectedSize int64, onProgress func(done, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "PortEye-Updater")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载返回状态 %d", resp.StatusCode)
	}

	f, err := os.Create(destPart)
	if err != nil {
		return fmt.Errorf("创建下载文件失败: %w", err)
	}

	total := resp.ContentLength
	if total <= 0 {
		total = expectedSize // 服务器未返回 Content-Length 时退回预期值
	}
	written := int64(0)
	buf := make([]byte, 32*1024)
	for {
		// ctx 取消时 Read 通常会返回 ctx.Err()
		if err := ctx.Err(); err != nil {
			f.Close()
			return err
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return fmt.Errorf("下载写盘失败: %w", werr)
			}
			written += int64(n)
			if onProgress != nil {
				onProgress(written, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return fmt.Errorf("下载读取失败: %w", rerr)
		}
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭下载文件失败: %w", err)
	}

	// 大小校验：防止截断/重定向到错误页面被当成功
	if expectedSize > 0 {
		fi, err := os.Stat(destPart)
		if err != nil {
			return fmt.Errorf("校验下载文件失败: %w", err)
		}
		if fi.Size() != expectedSize {
			return fmt.Errorf("下载大小不符：期望 %d 字节，实际 %d 字节", expectedSize, fi.Size())
		}
	}
	return nil
}

// ExtractUpdate 从 zipPath 解出 porteye.exe 与 porteye.exe.manifest 到 destDir。
//
// 输出文件名固定加 _update 后缀（porteye_update.exe / porteye_update.exe.manifest），
// 避免与正在运行的主 exe 同名触发共享冲突。
// zip 内缺少任一文件报错；zip.OpenReader 本身校验中央目录完整性，损坏 zip 直接报错。
func ExtractUpdate(zipPath, destDir string) (newExe, newManifest string, err error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", "", fmt.Errorf("打开 zip 失败: %w", err)
	}
	defer r.Close()

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", "", fmt.Errorf("创建解压目录失败: %w", err)
	}
	newExe = filepath.Join(destDir, "porteye_update.exe")
	newManifest = filepath.Join(destDir, "porteye_update.exe.manifest")
	gotExe, gotManifest := false, false

	for _, f := range r.File {
		// 只看文件名，忽略 zip 内目录结构（发行物约定为平铺两文件）
		base := filepath.Base(f.Name)
		switch strings.ToLower(base) {
		case "porteye.exe":
			if err := extractZipFile(f, newExe); err != nil {
				return "", "", fmt.Errorf("解压 exe 失败: %w", err)
			}
			gotExe = true
		case "porteye.exe.manifest":
			if err := extractZipFile(f, newManifest); err != nil {
				return "", "", fmt.Errorf("解压 manifest 失败: %w", err)
			}
			gotManifest = true
		}
	}
	if !gotExe {
		return "", "", fmt.Errorf("zip 缺少 porteye.exe")
	}
	if !gotManifest {
		return "", "", fmt.Errorf("zip 缺少 porteye.exe.manifest")
	}
	return newExe, newManifest, nil
}

// extractZipFile 把单个 zip 条目解压到 dest（覆盖）。
func extractZipFile(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

// WriteUpdateScript 生成并写入自更新批处理脚本到 %TEMP%\porteye_update_<pid>.bat。
//
// 为什么放 TEMP：exe 所在目录（如 Program Files）可能不可写，而 TEMP 总可写。
//
// 脚本职责（按顺序）：
//  1. chcp 65001 切 UTF-8 代码页，防中文路径/中文回显乱码（bat 本身无 BOM）
//  2. 轮询等待目标 PID 退出（旧进程不退出，move 会共享冲突）
//  3. move /y 覆盖主 exe，失败重试至多 10 次、每次间隔 1 秒
//     （防 MCP stdio 实例占用同一 exe；重试耗尽则拉起旧版不让程序消失）
//  4. manifest 同样 move /y（独立重试容错，失败仅警告不阻断）
//  5. start "" 启动新版本（必须带空标题引号，否则带空格路径被当窗口标题）
//  6. del "%~f0" 自删 bat
//
// 所有路径在 bat 内加双引号以兼容空格。
func WriteUpdateScript(pid int, oldExe, newExe, newManifest string) (string, error) {
	batPath := filepath.Join(os.TempDir(), fmt.Sprintf("porteye_update_%d.bat", pid))
	oldManifest := oldExe + ".manifest"

	// 注意：fmt 中 % 需写成 %%（如 del "%%~f0"），%d/%s 为实参占位。
	content := fmt.Sprintf(`@echo off
chcp 65001 >nul
setlocal enabledelayedexpansion

REM 等待目标 PID 退出（旧进程不退出，文件被占用无法替换）
:waitexit
tasklist /FI "PID eq %d" 2>nul | find "%d" >nul
if !errorlevel! equ 0 (
    timeout /t 1 /nobreak >nul
    goto waitexit
)

REM 替换主程序 exe（重试至多 10 次，每次间隔 1 秒，防共享冲突）
set /a retry=0
:copyexe
move /y "%s" "%s" >nul 2>&1
if !errorlevel! neq 0 (
    set /a retry+=1
    if !retry! lss 10 (
        timeout /t 1 /nobreak >nul
        goto copyexe
    )
    echo 更新失败：无法替换主程序（重试 10 次仍被占用），将启动旧版本。
    pause
    start "" "%s"
    del "%%~f0"
    exit /b 1
)

REM 替换 manifest（独立容错，失败仅警告不阻断主程序启动）
set /a mretry=0
:copymanifest
move /y "%s" "%s" >nul 2>&1
if !errorlevel! neq 0 (
    set /a mretry+=1
    if !mretry! lss 10 (
        timeout /t 1 /nobreak >nul
        goto copymanifest
    )
    echo 警告：manifest 替换失败，但主程序已更新（不影响运行）。
)

REM 启动新版本并自删本脚本
start "" "%s"
del "%%~f0"
`, pid, pid, newExe, oldExe, oldExe, newManifest, oldManifest, oldExe)

	// 行尾强制 CRLF：cmd.exe 在部分代码页（如 936 GBK）下解析 LF-only 的 .bat 会失败，
	// 更新根本不会执行。Go 原始字面量天然是 LF，这里统一转 CRLF，避免依赖构建机的
	// core.autocrlf（Linux 交叉编译 GOOS=windows 时不会自动转换，必踩雷）。
	content = strings.ReplaceAll(content, "\n", "\r\n")

	// 无 BOM：os.WriteFile 直接写字节，chcp 65001 负责解释 UTF-8。
	if err := os.WriteFile(batPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("写入更新脚本失败: %w", err)
	}
	return batPath, nil
}

// LaunchUpdateScript 用 ShellExecuteW 的 "open" 动词拉起更新 bat。
//
// 用 SW_SHOWNORMAL（可见窗口）：成功路径 cmd 一闪而过；失败路径 bat 有 pause，
// 必须可见，否则用户看不到失败原因、cmd 静默挂在 pause 上。
// 返回 nil 表示已成功发起（ShellExecute 是异步的，仅确认"已拉起"）。
func LaunchUpdateScript(batPath string) error {
	verb := syscall.StringToUTF16Ptr("open")
	file := syscall.StringToUTF16Ptr(batPath)
	params := syscall.StringToUTF16Ptr("")
	cwd := syscall.StringToUTF16Ptr(filepath.Dir(batPath))

	const swShowNormal = 1
	ret, _, _ := procShellExec.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)),
		uintptr(unsafe.Pointer(cwd)),
		uintptr(swShowNormal),
	)
	// HINSTANCE > 32 表示成功（与 elevate.go / killer.go 同款判断）
	if ret <= 32 {
		return fmt.Errorf("启动更新脚本失败，错误码 %d", int(ret))
	}
	return nil
}
