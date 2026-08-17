package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---- loadCheckState ----

func TestLoadCheckStateOK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "updater.json")
	if err := os.WriteFile(path, []byte(`{"lastCheckUnix":1000,"hadUpdate":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	state, err := loadCheckState(path)
	if err != nil {
		t.Fatalf("应解析成功: %v", err)
	}
	if state.LastCheckUnix != 1000 || !state.HadUpdate {
		t.Errorf("字段不符: %+v", state)
	}
}

func TestLoadCheckStateMissingFile(t *testing.T) {
	if _, err := loadCheckState(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("文件不存在应报错")
	}
}

func TestLoadCheckStateBadJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "updater.json")
	if err := os.WriteFile(path, []byte(`not json{`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCheckState(path); err == nil {
		t.Fatal("损坏 JSON 应报错")
	}
}

// ---- checkState.shouldSkip ----

func TestCheckStateShouldSkip(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	const interval = 24 * time.Hour
	cases := []struct {
		name  string
		state checkState
		now   time.Time
		want  bool
	}{
		{"从未检查(lastCheckUnix=0)不节流", checkState{}, now, false},
		{"hadUpdate=true 不节流", checkState{LastCheckUnix: now.Add(-time.Hour).Unix(), HadUpdate: true}, now, false},
		{"24 小时内节流", checkState{LastCheckUnix: now.Add(-time.Hour).Unix(), HadUpdate: false}, now, true},
		{"恰满 24 小时不节流", checkState{LastCheckUnix: now.Add(-interval).Unix(), HadUpdate: false}, now, false},
		{"超过 24 小时不节流", checkState{LastCheckUnix: now.Add(-48 * time.Hour).Unix(), HadUpdate: false}, now, false},
		{"未来时间戳(时钟回拨)按节流处理", checkState{LastCheckUnix: now.Add(time.Hour).Unix(), HadUpdate: false}, now, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.state.shouldSkip(c.now, interval); got != c.want {
				t.Errorf("shouldSkip(%+v, %v) = %v, want %v", c.state, c.now, got, c.want)
			}
		})
	}
}

// ---- RecordCheck / ShouldSkipRemoteCheck 端到端 ----

func TestRecordCheckAndShouldSkip(t *testing.T) {
	// APPDATA 重定向到临时目录，不污染真实 %APPDATA%\PortEye
	t.Setenv("APPDATA", t.TempDir())

	// 从未记录 → 不节流
	if ShouldSkipRemoteCheck() {
		t.Fatal("从未检查过应不节流")
	}

	// 记录一次"无更新"检查 → 24 小时内应节流
	RecordCheck(false)
	if !ShouldSkipRemoteCheck() {
		t.Fatal("记录无更新后应节流")
	}

	// 记录一次"有更新"检查 → 不节流（hadUpdate 优先）
	RecordCheck(true)
	if ShouldSkipRemoteCheck() {
		t.Fatal("hadUpdate=true 应不节流")
	}
}

func TestShouldSkipRemoteCheckNoAppData(t *testing.T) {
	// APPDATA 为空 → 状态文件路径不可用 → 静默不节流
	t.Setenv("APPDATA", "")
	if ShouldSkipRemoteCheck() {
		t.Fatal("APPDATA 为空应不节流")
	}
}
