// updatecheck.go 实现「检查更新」的远端请求节流。
//
// 动机：每次启动 GUI 都会触发一次 GitHub API 请求；异常网络环境（无网/代理
// 拦截/被限流）下用户反复点「检查更新」会反复打远端。节流后 24 小时内
// 只允许一次真实远端检查，其余直接跳过（不打扰用户）。
//
// 状态文件：%APPDATA%\PortEye\updater.json，内容为 checkState。
package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// CheckInterval 节流窗口：距上次成功检查不足 24 小时则跳过远端请求。
const CheckInterval = 24 * time.Hour

// checkState 是 updater.json 的内容。
// HadUpdate 为 true 表示上次检查发现有更新——此时不节流，
// 让用户能立刻再查（可能想确认新版本/重试下载）。
type checkState struct {
	LastCheckUnix int64 `json:"lastCheckUnix"` // 上次远端检查的 Unix 时间戳（秒）
	HadUpdate     bool  `json:"hadUpdate"`     // 上次检查是否发现更新
}

// updateStatePath 返回节流状态文件路径：%APPDATA%\PortEye\updater.json。
// 调用方必须先确认 APPDATA 非空（见 ShouldSkipRemoteCheck / RecordCheck 的
// 前置检查）——APPDATA 为空时 filepath.Join 会产出相对路径，写操作会落到
// 进程 CWD 下，产生清理不到的数据，故一律在入口静默短路。
func updateStatePath() string {
	return filepath.Join(os.Getenv("APPDATA"), "PortEye", "updater.json")
}

// ShouldSkipRemoteCheck 判断本次「检查更新」是否应跳过远端请求（节流）。
//
// 语义（冻结）：hadUpdate==true 不节流；lastCheckUnix==0 不节流；
// now-lastCheck < 24h 节流（返回 true）；状态文件读/解析失败或
// APPDATA 为空 → 不节流（返回 false，静默）。
func ShouldSkipRemoteCheck() bool {
	if os.Getenv("APPDATA") == "" {
		return false // 无 APPDATA（如服务会话）：无处读状态，静默不节流
	}
	state, err := loadCheckState(updateStatePath())
	if err != nil {
		return false
	}
	return state.shouldSkip(time.Now(), CheckInterval)
}

// RecordCheck 记录一次远端检查结果到状态文件（写入前确保目录存在）。
// 写失败静默：节流是锦上添花，失败不应打扰用户。
func RecordCheck(hadUpdate bool) {
	if os.Getenv("APPDATA") == "" {
		return // 无 APPDATA：无处写状态，静默跳过（避免落到 CWD 相对路径）
	}
	state := checkState{LastCheckUnix: time.Now().Unix(), HadUpdate: hadUpdate}
	dir := filepath.Dir(updateStatePath())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	_ = os.WriteFile(updateStatePath(), data, 0644)
}

// loadCheckState 读取并解析状态文件。文件不存在 / JSON 损坏均返回错误。
// 单独抽出便于单测（显式传 path，不依赖环境变量）。
func loadCheckState(path string) (checkState, error) {
	var state checkState
	data, err := os.ReadFile(path)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	return state, nil
}

// shouldSkip 判断距上次检查是否仍处于节流窗口内。
// hadUpdate 与 lastCheckUnix==0 两个「不节流」分支在此统一表达。
func (s checkState) shouldSkip(now time.Time, interval time.Duration) bool {
	if s.HadUpdate {
		return false
	}
	if s.LastCheckUnix == 0 {
		return false
	}
	return now.Sub(time.Unix(s.LastCheckUnix, 0)) < interval
}
