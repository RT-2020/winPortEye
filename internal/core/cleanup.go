// cleanup.go 实现 PortEye 的「清理本机数据」纯逻辑层。
//
// 分层约定：本文件只做磁盘/注册表编排，不 import ui/walk，
// 结果由 ui 包（设置面板）汇总成弹窗文案。
//
// 清理范围（克制设计：不删 exe 本体、不删软件目录其他用户文件）：
//   - %APPDATA%\PortEye 日志目录（运行日志，唯一持续增长项；见 ui/watcher.go:39）
//   - 开机自启注册表值（仅开过自启才有；HKCU\...\Run\PortEye）
//   - exe 同目录更新下载残留五件套（见本文件常量）
//   - %TEMP%\porteye_update_*.bat 更新脚本残留（见 updater.go WriteUpdateScript）
//   - %TEMP%\PortEye 临时工作目录（TempWorkDirPath；整个目录树）
//
// 逐项容错：单个失败（如日志文件正被本进程占用、exe 目录不可写）记入 Errors，
// 不阻断其他项。返回结果如实反映每类删除成功与否——是否算"验收通过"由 UI 层判定。
package core

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// 更新残留物料文件名（与 ui/updater.go 的未导出常量同值，由 ui 层引用本常量消除重复）。
// 发行物约定：解压/下载到 exe 同目录均加 _update 后缀，避免与运行中主 exe 同名触发共享冲突。
const (
	UpdateSidecarName  = "porteye_update.version"        // 内容：一行 tag，标记"已下载就绪"
	UpdateExeName      = "porteye_update.exe"            // 解压出的待替换主程序
	UpdateManifestName = "porteye_update.exe.manifest"   // 解压出的待替换 manifest
	UpdateZipName      = "porteye_update.zip"            // 下载完成的完整 zip
	UpdatePartName     = "porteye_update.zip.part"       // 下载中的临时分片
)

// 开机自启注册表：HKCU\Software\Microsoft\Windows\CurrentVersion\Run 的 PortEye 值。
// 从 ui/tray.go 迁入 core，供托盘菜单 / 设置面板勾选 / 清理功能共用同一份实现。
const (
	AutoStartKey   = `Software\Microsoft\Windows\CurrentVersion\Run`
	AutoStartValue = "PortEye"
)

// TempWorkDirName 是 %TEMP% 下 PortEye 临时工作目录名。
const TempWorkDirName = "PortEye"

// TempWorkDirPath 返回临时工作目录完整路径：%TEMP%\PortEye。
// 供更新/解压等临时文件使用；CleanupLocalData 整体删除该目录。
func TempWorkDirPath() string {
	return filepath.Join(os.TempDir(), TempWorkDirName)
}

// CleanupResult 汇总 CleanupLocalData 的逐项结果。
// 每类删除成功与否单独记录，单个失败不阻断整体（错误进 Errors）。
type CleanupResult struct {
	LogDirRemoved      bool     // 日志目录存在且已删除
	LogDirAbsent       bool     // 日志目录本就不存在（无日志需清理，与其他各类"无残留"口径对齐）
	AutoStartRemoved   bool     // 开机自启注册表值是否已删（本就无值视作 true）
	UpdateFilesRemoved []string // 成功删除的 exe 目录更新残留文件名
	TempBatsRemoved    []string // 成功删除的 TEMP 更新脚本文件名
	TempWorkDirRemoved bool     // 临时工作目录存在且已删除
	TempWorkDirAbsent  bool     // 临时工作目录本就不存在（与 LogDirAbsent 口径一致）
	Errors             []string // 逐项失败原因（含"正在使用中"类共享冲突）
}

// SetAutoStart 写入或删除开机自启注册表值（HKCU\...\Run\PortEye）。
// enable=true 时写入当前 exe 路径（带引号，兼容空格路径）；enable=false 时删除该值。
// 从 ui/tray.go 迁入，行为与原实现完全一致。
func SetAutoStart(enable bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, AutoStartKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if enable {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return k.SetStringValue(AutoStartValue, fmt.Sprintf("%q", exe))
	}
	return k.DeleteValue(AutoStartValue)
}

// IsAutoStart 判断开机自启注册表值是否存在。
// 从 ui/tray.go 迁入，行为与原实现完全一致。
func IsAutoStart() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, AutoStartKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(AutoStartValue)
	return err == nil
}

// CleanupLocalData 一键清理 PortEye 在本机的全部残留数据。
//
// exeDir 为当前 exe 所在目录（由调用方用 os.Executable 取，见 ui/updater.go:291），
// 用于定位更新残留五件套。其余清理目标（APPDATA 日志、注册表、TEMP bat）从环境/系统取。
//
// 逐项容错，返回 CleanupResult 如实反映每类结果。不 panic。
func CleanupLocalData(exeDir string) CleanupResult {
	var r CleanupResult

	// 1. 日志目录：%APPDATA%\PortEye（整个目录树，含 logs\app.log）
	//    先 Stat 判断存在性：不存在 → LogDirAbsent（无日志需清理，与其他三类口径对齐），
	//    不把 RemoveAll 对不存在路径返回的 nil 当作"已清理"。
	//    存在时 app.log 正被本进程 log.SetOutput 持有，删除会失败——记入 Errors，
	//    注明"正在使用中，重启后再删"。
	if appData := os.Getenv("APPDATA"); appData != "" {
		logDir := filepath.Join(appData, "PortEye")
		switch _, err := os.Stat(logDir); {
		case os.IsNotExist(err):
			r.LogDirAbsent = true
		case err != nil:
			r.Errors = append(r.Errors, "日志目录检查失败: "+err.Error())
		default:
			if err := os.RemoveAll(logDir); err != nil {
				r.Errors = append(r.Errors, "日志目录清理失败（可能正在使用中，重启后再删）: "+err.Error())
			} else {
				r.LogDirRemoved = true
			}
		}
	} else {
		r.Errors = append(r.Errors, "未设置 APPDATA 环境变量，跳过日志目录清理")
	}

	// 2. 开机自启：先判断再删，避免对不存在的值调用 DeleteValue 报错。
	//    本就未开自启视作已清理（AutoStartRemoved=true）。
	if IsAutoStart() {
		if err := SetAutoStart(false); err != nil {
			r.Errors = append(r.Errors, "关闭开机自启失败: "+err.Error())
		} else {
			r.AutoStartRemoved = true
		}
	} else {
		r.AutoStartRemoved = true
	}

	// 3. exe 同目录更新残留五件套：逐个删，本就不存在不算失败，
	//    exe 目录不可写等情况记入 Errors（可能正在使用中）。
	for _, name := range []string{
		UpdateSidecarName, UpdateManifestName, UpdateExeName, UpdateZipName, UpdatePartName,
	} {
		p := filepath.Join(exeDir, name)
		if err := os.Remove(p); err != nil {
			if os.IsNotExist(err) {
				continue // 本就不存在，不算失败也不计入已删
			}
			r.Errors = append(r.Errors, "更新残留删除失败 "+name+"（可能正在使用中）: "+err.Error())
			continue
		}
		r.UpdateFilesRemoved = append(r.UpdateFilesRemoved, name)
	}

	// 4. TEMP 下的 porteye_update_*.bat（更新脚本残留，WriteUpdateScript 写入）
	pattern := filepath.Join(os.TempDir(), "porteye_update_*.bat")
	bats, _ := filepath.Glob(pattern)
	for _, b := range bats {
		if err := os.Remove(b); err != nil {
			r.Errors = append(r.Errors, "临时脚本删除失败 "+filepath.Base(b)+": "+err.Error())
			continue
		}
		r.TempBatsRemoved = append(r.TempBatsRemoved, filepath.Base(b))
	}

	// 5. 临时工作目录：%TEMP%\PortEye（整个目录树，口径与日志目录一致：
	//    不存在 → TempWorkDirAbsent，不把 RemoveAll 对不存在路径的 nil 当作"已清理"）
	switch _, err := os.Stat(TempWorkDirPath()); {
	case os.IsNotExist(err):
		r.TempWorkDirAbsent = true
	case err != nil:
		r.Errors = append(r.Errors, "临时工作目录检查失败: "+err.Error())
	default:
		if err := os.RemoveAll(TempWorkDirPath()); err != nil {
			r.Errors = append(r.Errors, "临时工作目录清理失败（可能正在使用中）: "+err.Error())
		} else {
			r.TempWorkDirRemoved = true
		}
	}

	return r
}
