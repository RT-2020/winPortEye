// updater.go 把 core 层的更新逻辑接到 walk UI（更新按钮、下载进度、重启替换）。
//
// 线程模型：检查/下载在后台 goroutine；所有 walk 控件操作（按钮文本/可见性、
// MsgBox）一律经 mw.Synchronize 切回 UI 线程（与 watcher.go:32 同款用法）。
//
// ctx 生命周期：下载用 uc.ctx，进程真实退出（托盘退出 / 立即重启更新）时取消；
// 窗口关闭（=藏托盘）【不】取消——用户最小化到托盘时下载应继续。
package ui

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"win/internal/core"

	"github.com/lxn/walk"
)

// 更新物料文件名（均落在 exe 同目录，加 _update 后缀避免与运行中 exe 同名冲突）。
const (
	updateSidecarName  = "porteye_update.version"          // 内容：一行 tag，标记"已下载就绪"
	updateExeName      = "porteye_update.exe"              // 解压出的待替换主程序
	updateManifestName = "porteye_update.exe.manifest"     // 解压出的待替换 manifest
	updateZipName      = "porteye_update.zip"              // 下载完成的完整 zip
	updatePartName     = "porteye_update.zip.part"         // 下载中的临时分片
	tooltipMaxRunes    = 800                                // changelog 截断长度（walk tooltip 上限 1023 UTF-16）
)

// updaterController 封装更新按钮的全部交互状态。
// 由 Run() 在主窗口创建后构造，trayIcon 字段在 setupTray 返回后赋值。
type updaterController struct {
	mw       *walk.MainWindow
	btn      *walk.PushButton
	trayIcon *walk.NotifyIcon
	version  string

	ctx    context.Context
	cancel context.CancelFunc

	info *core.UpdateInfo // 当前发现的远端更新（仅 UI 线程读写）

	checkClient    *http.Client // 检查用，总超时 10s
	downloadClient *http.Client // 下载用，不设总超时，ResponseHeaderTimeout 15s
}

// newUpdaterController 构造控制器。trayIcon 在 setupTray 返回后由调用方赋值。
func newUpdaterController(mw *walk.MainWindow, btn *walk.PushButton, version string) *updaterController {
	ctx, cancel := context.WithCancel(context.Background())
	return &updaterController{
		mw:      mw,
		btn:     btn,
		version: version,
		ctx:     ctx,
		cancel:  cancel,
		checkClient: &http.Client{
			Timeout: 10 * time.Second, // 检查总超时 ≤10s，慢就静默放弃
		},
		downloadClient: &http.Client{
			Transport: &http.Transport{
				// 国内访问 GitHub 慢：不设总超时，仅兜住"连上但首字节迟迟不来"
				ResponseHeaderTimeout: 15 * time.Second,
			},
		},
	}
}

// start 在后台启动更新检查。先检 sidecar（已下载就绪）再检远端，全程静默失败。
func (uc *updaterController) start() {
	go uc.checkAndUpdate()
}

// checkAndUpdate 实现五步交互契约中的启动期逻辑：
//  1. sidecar + 待装 exe 均在且 tag 比当前新 → 直接弹时机选择窗（跳过下载）
//     - 用户选"立即" → 重启替换
//     - 用户选"稍后" → 继续查远端，若远端比已下载版本更新则丢弃旧物走新流程
//  2. sidecar 不完整/无效 → 清理孤儿残留（不提示）
//  3. 静默检查远端，有更新则显示按钮
func (uc *updaterController) checkAndUpdate() {
	dir, err := exeDir()
	if err != nil {
		return // 无法定位 exe 目录，更新无从谈起，静默
	}
	sidecarPath := filepath.Join(dir, updateSidecarName)
	updateExePath := filepath.Join(dir, updateExeName)

	// 1. 优先处理已就绪的 sidecar 更新（上次下载完成但用户选了"稍后"）
	if tag, ok := readSidecar(sidecarPath); ok {
		if _, statErr := os.Stat(updateExePath); statErr == nil &&
			core.CompareVersions(tag, uc.version) == 1 {
			// 有就绪且比当前新 → 弹时机选择窗，跳过下载
			if uc.promptInstallReady(tag) {
				uc.mw.Synchronize(func() { _ = uc.performRestartUpdate() })
				return // 进程即将退出
			}
			// 用户选稍后：查远端是否比已下载版本更新
			info, _ := core.CheckLatest(uc.checkClient, uc.version)
			if info != nil && core.CompareVersions(info.TagName, tag) == 1 {
				// 远端更新 → 丢弃旧下载物，按新版本走完整流程
				uc.cleanupStaleDownloads(dir)
				uc.applyRemoteUpdate(info)
			}
			// 远端不比 sidecar 新：用户已拒绝该版本，不再打扰
			return
		}
	}

	// 2. sidecar 不完整/无效/已过时 → 清理孤儿残留（绝不提示用户）
	uc.cleanupStaleDownloads(dir)

	// 3. 静默检查远端
	info, err := core.CheckLatest(uc.checkClient, uc.version)
	if err != nil || info == nil {
		return // 无网/限流/已最新/解析失败 一律静默
	}
	uc.applyRemoteUpdate(info)
}

// applyRemoteUpdate 把发现的远端更新显示到按钮上（切回 UI 线程）。
func (uc *updaterController) applyRemoteUpdate(info *core.UpdateInfo) {
	uc.mw.Synchronize(func() {
		uc.info = info
		uc.btn.SetText("有新版本 " + info.TagName)
		_ = uc.btn.SetToolTipText(truncateTooltip(info.Changelog))
		uc.btn.SetVisible(true)
	})
}

// onButtonClicked 处理更新按钮点击：禁用按钮 → 后台下载 → 进度更新 → 弹时机选择。
// 下载中按钮显示"下载中… NN%"；失败回滚按钮状态并弹错（不影响主功能）。
func (uc *updaterController) onButtonClicked() {
	if uc.info == nil {
		return
	}
	info := uc.info
	dir, err := exeDir()
	if err != nil {
		walk.MsgBox(uc.mw, "更新", "无法定位程序目录："+err.Error(), walk.MsgBoxIconError)
		return
	}

	uc.btn.SetEnabled(false)
	uc.btn.SetText("下载中… 0%")

	go func() {
		partPath := filepath.Join(dir, updatePartName)
		err := core.Download(uc.ctx, uc.downloadClient, info.DownloadURL, partPath, info.Size,
			func(done, total int64) {
				uc.mw.Synchronize(func() {
					if total > 0 {
						uc.btn.SetText(fmt.Sprintf("下载中… %d%%", done*100/total))
					} else {
						uc.btn.SetText(fmt.Sprintf("下载中… %d KB", done/1024))
					}
				})
			})
		if err != nil {
			uc.mw.Synchronize(func() {
				uc.btn.SetEnabled(true)
				uc.btn.SetText("有新版本 " + info.TagName)
				walk.MsgBox(uc.mw, "更新", "下载失败："+err.Error(), walk.MsgBoxIconError)
			})
			return
		}

		// 下载完成：rename .part → .zip
		zipPath := filepath.Join(dir, updateZipName)
		_ = os.Remove(zipPath) // 覆盖可能残留的旧 zip
		if err := os.Rename(partPath, zipPath); err != nil {
			uc.failDownload(info, "整理下载文件失败："+err.Error())
			return
		}
		// 解压校验
		if _, _, err := core.ExtractUpdate(zipPath, dir); err != nil {
			uc.failDownload(info, "解压更新包失败："+err.Error())
			return
		}
		// 最后写 sidecar：只有解压校验通过才标记"就绪"，避免半成品被当可装
		if err := os.WriteFile(filepath.Join(dir, updateSidecarName), []byte(info.TagName), 0644); err != nil {
			uc.failDownload(info, "记录更新就绪状态失败："+err.Error())
			return
		}

		// 弹时机选择窗（在 UI 线程）
		uc.mw.Synchronize(func() {
			uc.promptDownloaded(info)
		})
	}()
}

// failDownload 下载后处理失败：恢复按钮可点 + 弹错。
func (uc *updaterController) failDownload(info *core.UpdateInfo, msg string) {
	uc.mw.Synchronize(func() {
		uc.btn.SetEnabled(true)
		uc.btn.SetText("有新版本 " + info.TagName)
		walk.MsgBox(uc.mw, "更新", msg, walk.MsgBoxIconError)
	})
}

// promptDownloaded 在下载完成后弹时机选择窗。
// 是 = 立即重启更新；否 = 稍后（隐藏按钮，保留文件，下次启动再提示）。
func (uc *updaterController) promptDownloaded(info *core.UpdateInfo) {
	summary := truncateTooltip(strings.TrimSpace(info.Changelog))
	if summary == "" {
		summary = "（作者未提供更新日志）"
	}
	msg := fmt.Sprintf("新版本 %s 已下载完成，可以立即安装。\n\n"+
		"更新内容：\n%s\n\n"+
		"【是】= 立即重启更新（退出 → 替换 → 自动启动新版）\n"+
		"【否】= 稍后（保留下载，下次启动时再次提示）",
		info.TagName, summary)
	if walk.MsgBox(uc.mw, "更新就绪", msg, walk.MsgBoxYesNo|walk.MsgBoxIconInformation) == walk.DlgCmdYes {
		_ = uc.performRestartUpdate()
		return
	}
	// 稍后：隐藏按钮，下次启动经 sidecar 再提示
	uc.btn.SetVisible(false)
}

// promptInstallReady 弹「已下载就绪」时机选择窗（用于 sidecar 命中场景）。
// 返回 true = 立即重启更新。在 goroutine 中调用，内部用 Synchronize 切 UI 线程。
func (uc *updaterController) promptInstallReady(tag string) bool {
	var yes bool
	uc.mw.Synchronize(func() {
		msg := fmt.Sprintf("检测到新版本 %s 已下载就绪，处于待安装状态。\n\n"+
			"是否立即重启并完成更新？\n"+
			"（选【否】= 稍后，下次启动会再次提示安装）", tag)
		yes = walk.MsgBox(uc.mw, "更新就绪", msg,
			walk.MsgBoxYesNo|walk.MsgBoxIconInformation) == walk.DlgCmdYes
	})
	return yes
}

// performRestartUpdate 写出更新 bat → 拉起 → 退出当前进程。
// 必须在 UI 线程调用（触碰 walk 控件 + 关闭窗口）。
// 退出序列与「以管理员重启」按钮一致（window.go:312）：requestRealClose + 摘托盘 + mw.Close，
// 否则旧实例会被 hookCloseButton 藏进托盘不死，bat 永远等不到 PID 退出。
func (uc *updaterController) performRestartUpdate() error {
	dir, err := exeDir()
	if err != nil {
		walk.MsgBox(uc.mw, "更新失败", "无法定位程序目录："+err.Error(), walk.MsgBoxIconError)
		return err
	}
	oldExe, err := os.Executable()
	if err != nil {
		walk.MsgBox(uc.mw, "更新失败", "无法获取当前程序路径："+err.Error(), walk.MsgBoxIconError)
		return err
	}
	newExe := filepath.Join(dir, updateExeName)
	newManifest := filepath.Join(dir, updateManifestName)

	batPath, err := core.WriteUpdateScript(os.Getpid(), oldExe, newExe, newManifest)
	if err != nil {
		walk.MsgBox(uc.mw, "更新失败", "生成更新脚本失败："+err.Error(), walk.MsgBoxIconError)
		return err
	}
	// 拉起 bat（ShellExecute open，异步）。提权会话中发起的更新，新进程继承管理员权限
	// ——与更新前权限一致，此处接受该行为。
	if err := core.LaunchUpdateScript(batPath); err != nil {
		walk.MsgBox(uc.mw, "更新失败", err.Error(), walk.MsgBoxIconError)
		return err
	}

	// 退出当前进程：放行关窗拦截 + 摘除托盘图标（防幽灵）+ 取消下载 ctx
	requestRealClose()
	if uc.trayIcon != nil {
		uc.trayIcon.Dispose()
	}
	uc.cancel()
	uc.mw.Close()
	return nil
}

// cleanupStaleDownloads 删除 exe 同目录的全部更新残留（孤儿 .exe/.manifest/.version/.zip/.part）。
// 用于 sidecar 不完整或更新已完成的清理，绝不提示用户。
func (uc *updaterController) cleanupStaleDownloads(dir string) {
	for _, name := range []string{
		updateExeName, updateManifestName, updateSidecarName, updateZipName, updatePartName,
	} {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// ---- 辅助函数 ----

// exeDir 返回当前可执行文件所在目录（更新物料落盘位置）。
func exeDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

// readSidecar 读取 sidecar 文件并返回 trim 后的 tag。文件不存在/空视为无就绪。
func readSidecar(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	tag := strings.TrimSpace(string(b))
	if tag == "" {
		return "", false
	}
	return tag, true
}

// truncateTooltip 按 rune 截断 changelog 到约 800 字符并加 "…"。
// walk tooltip 硬上限 1023 UTF-16 单元，800 rune 给中文/常用字符留足余量。
func truncateTooltip(s string) string {
	runes := []rune(s)
	if len(runes) <= tooltipMaxRunes {
		return s
	}
	return string(runes[:tooltipMaxRunes]) + "…"
}
