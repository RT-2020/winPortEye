package core

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// snapshotAutoStart 快照 HKCU\...\Run\PortEye 的原值与存在性，
// 供测试 t.Cleanup 逐字节恢复注册表，避免污染真实环境。
func snapshotAutoStart() (value string, exists bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, AutoStartKey, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(AutoStartValue)
	if err != nil {
		return "", false
	}
	return v, true
}

// restoreAutoStart 把 HKCU\...\Run\PortEye 恢复到指定状态。
// exists=true 写回原值；exists=false 删除该值。任何失败静默（尽力恢复）。
func restoreAutoStart(value string, exists bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, AutoStartKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	if exists {
		_ = k.SetStringValue(AutoStartValue, value)
	} else {
		_ = k.DeleteValue(AutoStartValue)
	}
}

// TestCleanupLocalData 端到端验证 CleanupLocalData 的四类清理。
//
// 测试环境隔离：
//   - APPDATA / TMP / TEMP 用 t.Setenv 重定向到临时目录（t 结束自动恢复）
//   - exeDir 用 t.TempDir() 模拟
//   - 注册表用 snapshotAutoStart 快照原值，t.Cleanup 逐字节恢复（不污染真实 HKCU）
//
// 注册表闭环：先 SetAutoStart(true) 写 test.exe 路径 → Cleanup 应删除。
// CI 环境若禁写注册表，SetAutoStart(true) 报错时跳过注册表断言。
func TestCleanupLocalData(t *testing.T) {
	origVal, origExists := snapshotAutoStart()
	t.Cleanup(func() { restoreAutoStart(origVal, origExists) })

	// --- 准备临时 APPDATA + 日志文件 ---
	tmpAppData := t.TempDir()
	t.Setenv("APPDATA", tmpAppData)
	logDir := filepath.Join(tmpAppData, "PortEye", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(logDir, "app.log")
	if err := os.WriteFile(logFile, []byte("fake log"), 0644); err != nil {
		t.Fatal(err)
	}

	// --- 准备临时 exe 目录 + 五个更新残留文件 ---
	exeDir := t.TempDir()
	wantUpdateFiles := []string{
		UpdateSidecarName, UpdateManifestName, UpdateExeName,
		UpdateZipName, UpdatePartName,
	}
	for _, name := range wantUpdateFiles {
		if err := os.WriteFile(filepath.Join(exeDir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// --- 准备临时 TEMP + porteye_update_*.bat ---
	tmpTemp := t.TempDir()
	t.Setenv("TMP", tmpTemp) // os.TempDir() 在 Windows 经 GetTempPathW 读 TMP
	t.Setenv("TEMP", tmpTemp)
	batName := "porteye_update_99999.bat"
	batPath := filepath.Join(tmpTemp, batName)
	if err := os.WriteFile(batPath, []byte("echo hi"), 0644); err != nil {
		t.Fatal(err)
	}

	// --- 注册表闭环：先开自启，Cleanup 应关掉 ---
	wantRegCheck := true
	if err := SetAutoStart(true); err != nil {
		t.Logf("SetAutoStart(true) 失败，跳过注册表断言: %v", err)
		wantRegCheck = false
	} else if !IsAutoStart() {
		t.Fatal("SetAutoStart(true) 后 IsAutoStart 应为 true")
	}

	// --- 执行清理 ---
	r := CleanupLocalData(exeDir)

	// 断言 1：日志目录存在且已删（LogDirRemoved=true，LogDirAbsent=false）
	if !r.LogDirRemoved || r.LogDirAbsent {
		t.Errorf("LogDirRemoved 应为 true（LogDirAbsent 应为 false），errors=%v", r.Errors)
	}
	if _, err := os.Stat(logDir); !os.IsNotExist(err) {
		t.Errorf("日志目录应已删除，stat err=%v", err)
	}

	// 断言 2：五个更新残留文件全部进 UpdateFilesRemoved
	if !reflect.DeepEqual(r.UpdateFilesRemoved, wantUpdateFiles) {
		t.Errorf("UpdateFilesRemoved 不符：got %v，want %v", r.UpdateFilesRemoved, wantUpdateFiles)
	}
	for _, name := range wantUpdateFiles {
		if _, err := os.Stat(filepath.Join(exeDir, name)); !os.IsNotExist(err) {
			t.Errorf("残留文件 %s 应已删除，stat err=%v", name, err)
		}
	}

	// 断言 3：TEMP bat 已删
	if len(r.TempBatsRemoved) != 1 || r.TempBatsRemoved[0] != batName {
		t.Errorf("TempBatsRemoved 不符：got %v，want [%s]", r.TempBatsRemoved, batName)
	}
	if _, err := os.Stat(batPath); !os.IsNotExist(err) {
		t.Errorf("临时脚本 %s 应已删除，stat err=%v", batPath, err)
	}

	// 断言 4：注册表（仅在成功开自启时校验）
	if wantRegCheck {
		if !r.AutoStartRemoved {
			t.Error("AutoStartRemoved 应为 true")
		}
		if IsAutoStart() {
			t.Error("Cleanup 后 IsAutoStart 应为 false")
		}
	}
}

// TestCleanupLocalData_NoSidecarFiles 验证 exe 目录无残留文件、APPDATA 无日志目录时，
// UpdateFilesRemoved 为空、LogDirAbsent=true，且不报错（os.IsNotExist 静默跳过）。
// 注册表用 snapshotAutoStart 快照恢复，不污染真实环境（即便本机开过 PortEye 自启）。
func TestCleanupLocalData_NoSidecarFiles(t *testing.T) {
	origVal, origExists := snapshotAutoStart()
	t.Cleanup(func() { restoreAutoStart(origVal, origExists) })

	t.Setenv("APPDATA", t.TempDir()) // 空 APPDATA → 无 PortEye 日志目录
	t.Setenv("TMP", t.TempDir())
	t.Setenv("TEMP", t.TempDir())

	r := CleanupLocalData(t.TempDir()) // 空 exe 目录

	if len(r.UpdateFilesRemoved) != 0 {
		t.Errorf("空目录 UpdateFilesRemoved 应为空，got %v", r.UpdateFilesRemoved)
	}
	if !r.LogDirAbsent || r.LogDirRemoved {
		t.Errorf("空 APPDATA 应 LogDirAbsent=true，got removed=%v absent=%v", r.LogDirRemoved, r.LogDirAbsent)
	}
	// 不应因"文件/目录不存在"产生错误
	for _, e := range r.Errors {
		if strings.Contains(e, "不存在") {
			t.Errorf("不应因不存在报错: %s", e)
		}
	}
}
