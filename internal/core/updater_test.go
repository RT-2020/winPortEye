package core

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- CompareVersions ----

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int
	}{
		{"相等", "1.0.0", "1.0.0", 0},
		{"大于", "1.2.0", "1.1.0", 1},
		{"小于", "1.0.0", "1.0.1", -1},
		{"带v前缀相等", "v1.0.0", "1.0.0", 0},
		{"带V前缀", "V2.0", "1.9.9", 1},
		{"两段vs三段缺段补0", "1.2", "1.2.0", 0},
		{"两段vs三段不等", "1.2", "1.2.1", -1},
		{"主版本号", "2.0.0", "1.9.9", 1},
		{"非数字段-beta不可比较", "1.0-beta", "1.0.0", versionNonComparable},
		{"非数字段rc不可比较", "1.0.0", "1.0-rc1", versionNonComparable},
		{"空串视为0", "", "0.0.0", 0},
		{"大版本号", "0.3.0", "0.2.9", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CompareVersions(c.a, c.b); got != c.want {
				t.Fatalf("CompareVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
			}
		})
	}
}

// ---- CheckLatest ----

// githubReleasesHandler 构造一个返回固定 JSON 的 httptest 服务器。
func newReleasesServer(t *testing.T, tag, body string, assets []githubAsset) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// GitHub 要求 User-Agent，验证调用方确实带了
		if r.Header.Get("User-Agent") == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		resp := githubRelease{TagName: tag, Body: body, Assets: assets}
		_ = json.NewEncoder(w).Encode(resp)
	})
	return httptest.NewServer(mux)
}

func TestCheckLatestHasUpdate(t *testing.T) {
	srv := newReleasesServer(t, "v0.3.0", "修复若干问题", []githubAsset{
		{Name: "PortEye_v0.3.1_win64.zip", BrowserDownloadURL: "http://example/PortEye_v0.3.1_win64.zip", Size: 12345},
		{Name: "porteye.exe", BrowserDownloadURL: "http://example/porteye.exe", Size: 100},
	})
	defer srv.Close()
	old := githubLatestURL
	githubLatestURL = srv.URL
	defer func() { githubLatestURL = old }()

	client := &http.Client{Timeout: 5 * time.Second}
	info, err := CheckLatest(client, "0.2.0")
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if info == nil {
		t.Fatal("应发现更新，got nil")
	}
	// 应优先选 .zip 资产
	if info.AssetName != "PortEye_v0.3.1_win64.zip" {
		t.Errorf("应优先选 PortEye_v0.3.1_win64.zip，got %s", info.AssetName)
	}
	if info.TagName != "v0.3.0" || info.Size != 12345 || info.DownloadURL != "http://example/PortEye_v0.3.1_win64.zip" {
		t.Errorf("info 字段不符: %+v", info)
	}
}

func TestCheckLatestNoUpdate(t *testing.T) {
	// 远端 tag == 当前版本 → (nil, nil)
	srv := newReleasesServer(t, "v0.2.0", "", []githubAsset{
		{Name: "PortEye_v0.3.1_win64.zip", BrowserDownloadURL: "http://example/PortEye_v0.3.1_win64.zip", Size: 1},
	})
	defer srv.Close()
	old := githubLatestURL
	githubLatestURL = srv.URL
	defer func() { githubLatestURL = old }()

	info, err := CheckLatest(&http.Client{Timeout: 5 * time.Second}, "0.2.0")
	if err != nil {
		t.Fatalf("应返回 nil,nil，got err=%v", err)
	}
	if info != nil {
		t.Fatalf("已是最新应返回 nil，got %+v", info)
	}
}

func TestCheckLatestRemoteOlder(t *testing.T) {
	// 远端 tag < 当前版本 → (nil, nil)
	srv := newReleasesServer(t, "v0.1.0", "", []githubAsset{
		{Name: "PortEye_v0.3.1_win64.zip", BrowserDownloadURL: "http://x", Size: 1},
	})
	defer srv.Close()
	old := githubLatestURL
	githubLatestURL = srv.URL
	defer func() { githubLatestURL = old }()

	info, err := CheckLatest(&http.Client{Timeout: 5 * time.Second}, "0.2.0")
	if err != nil || info != nil {
		t.Fatalf("远端更旧应返回 nil,nil，got info=%+v err=%v", info, err)
	}
}

func TestCheckLatestNonNumericTagSilent(t *testing.T) {
	// tag 含预发布标记 → 不可比较 → 按无更新静默 (nil, nil)
	srv := newReleasesServer(t, "v1.0-beta", "", []githubAsset{
		{Name: "PortEye_v0.3.1_win64.zip", BrowserDownloadURL: "http://x", Size: 1},
	})
	defer srv.Close()
	old := githubLatestURL
	githubLatestURL = srv.URL
	defer func() { githubLatestURL = old }()

	info, err := CheckLatest(&http.Client{Timeout: 5 * time.Second}, "0.2.0")
	if err != nil || info != nil {
		t.Fatalf("不可比较 tag 应返回 nil,nil，got info=%+v err=%v", info, err)
	}
}

func TestCheckLatestNoAssets(t *testing.T) {
	// 无资产 → err
	srv := newReleasesServer(t, "v9.9.9", "", nil)
	defer srv.Close()
	old := githubLatestURL
	githubLatestURL = srv.URL
	defer func() { githubLatestURL = old }()

	info, err := CheckLatest(&http.Client{Timeout: 5 * time.Second}, "0.1.0")
	if err == nil {
		t.Fatalf("无资产应报错，got info=%+v", info)
	}
	if info != nil {
		t.Fatalf("出错时 info 应为 nil")
	}
}

func TestCheckLatestBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json {"))
	}))
	defer srv.Close()
	old := githubLatestURL
	githubLatestURL = srv.URL
	defer func() { githubLatestURL = old }()

	if _, err := CheckLatest(&http.Client{Timeout: 5 * time.Second}, "0.1.0"); err == nil {
		t.Fatal("坏 JSON 应报错")
	}
}

func TestCheckLatestExeOnlyAsset(t *testing.T) {
	// 只有裸 exe 无 zip → 选 .exe
	srv := newReleasesServer(t, "v0.3.0", "", []githubAsset{
		{Name: "porteye.exe", BrowserDownloadURL: "http://x/porteye.exe", Size: 999},
	})
	defer srv.Close()
	old := githubLatestURL
	githubLatestURL = srv.URL
	defer func() { githubLatestURL = old }()

	info, err := CheckLatest(&http.Client{Timeout: 5 * time.Second}, "0.2.0")
	if err != nil || info == nil {
		t.Fatalf("应选 exe 资产，got info=%+v err=%v", info, err)
	}
	if info.AssetName != "porteye.exe" {
		t.Errorf("应选 porteye.exe，got %s", info.AssetName)
	}
}

func TestCheckLatestNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	old := githubLatestURL
	githubLatestURL = srv.URL
	defer func() { githubLatestURL = old }()

	if _, err := CheckLatest(&http.Client{Timeout: 5 * time.Second}, "0.1.0"); err == nil {
		t.Fatal("非 200 应报错")
	}
}

// ---- Download ----

func TestDownloadOK(t *testing.T) {
	body := bytes.Repeat([]byte("A"), 4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "dl.part")
	var lastDone int64
	var reportedTotal int64
	err := Download(context.Background(), srv.Client(), srv.URL, dest, int64(len(body)), func(done, total int64) {
		lastDone = done
		reportedTotal = total
	})
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, body) {
		t.Errorf("下载内容不符: got %d bytes", len(got))
	}
	if reportedTotal != int64(len(body)) {
		t.Errorf("进度 total 应=%d，got %d", len(body), reportedTotal)
	}
	if lastDone != int64(len(body)) {
		t.Errorf("进度最终 done 应=%d，got %d", len(body), lastDone)
	}
}

func TestDownloadSizeMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("short"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "dl.part")
	// 期望 999 字节，实际 5 → 应报错
	err := Download(context.Background(), srv.Client(), srv.URL, dest, 999, nil)
	if err == nil {
		t.Fatal("大小不符应报错")
	}
	if !strings.Contains(err.Error(), "不符") {
		t.Errorf("错误信息应含\"不符\"，got %v", err)
	}
}

func TestDownloadContextCancel(t *testing.T) {
	// 慢响应 + ctx 取消 → 应返回 ctx 错误
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 每次写 1 字节后 sleep，确保 ctx 取消在传输中生效
		for i := 0; i < 100; i++ {
			_, _ = w.Write([]byte("x"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	dest := filepath.Join(t.TempDir(), "dl.part")
	err := Download(ctx, srv.Client(), srv.URL, dest, 0, nil)
	if err == nil {
		t.Fatal("ctx 取消应导致下载报错")
	}
}

// ---- ExtractUpdate ----

// buildZip 在 buf 里构造一个 zip，含 name→content 的若干平铺文件。
func buildZip(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("创建 zip 条目 %s 失败: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("写 zip 条目 %s 失败: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 zip 失败: %v", err)
	}
	path := filepath.Join(t.TempDir(), "update.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("写 zip 文件失败: %v", err)
	}
	return path
}

func TestExtractUpdateOK(t *testing.T) {
	zipPath := buildZip(t, map[string]string{
		"porteye.exe":           "EXE BODY",
		"porteye.exe.manifest":  "MANIFEST BODY",
	})
	dest := t.TempDir()
	newExe, newManifest, err := ExtractUpdate(zipPath, dest)
	if err != nil {
		t.Fatalf("解压失败: %v", err)
	}
	if got, _ := os.ReadFile(newExe); string(got) != "EXE BODY" {
		t.Errorf("exe 内容不符: %q", got)
	}
	if got, _ := os.ReadFile(newManifest); string(got) != "MANIFEST BODY" {
		t.Errorf("manifest 内容不符: %q", got)
	}
	if filepath.Base(newExe) != "porteye_update.exe" {
		t.Errorf("exe 命名应为 porteye_update.exe，got %s", filepath.Base(newExe))
	}
}

func TestExtractUpdateMissingExe(t *testing.T) {
	zipPath := buildZip(t, map[string]string{
		"porteye.exe.manifest": "ONLY MANIFEST",
	})
	if _, _, err := ExtractUpdate(zipPath, t.TempDir()); err == nil {
		t.Fatal("缺 exe 应报错")
	}
}

func TestExtractUpdateMissingManifest(t *testing.T) {
	zipPath := buildZip(t, map[string]string{
		"porteye.exe": "ONLY EXE",
	})
	if _, _, err := ExtractUpdate(zipPath, t.TempDir()); err == nil {
		t.Fatal("缺 manifest 应报错")
	}
}

func TestExtractUpdateNestedPath(t *testing.T) {
	// zip 内带子目录路径时，只看 basename 仍应识别
	zipPath := buildZip(t, map[string]string{
		"sub/porteye.exe":          "NESTED EXE",
		"deep/porteye.exe.manifest": "NESTED MANIFEST",
	})
	dest := t.TempDir()
	newExe, newManifest, err := ExtractUpdate(zipPath, dest)
	if err != nil {
		t.Fatalf("嵌套路径解压失败: %v", err)
	}
	if got, _ := os.ReadFile(newExe); string(got) != "NESTED EXE" {
		t.Errorf("嵌套 exe 内容不符: %q", got)
	}
	_ = newManifest
}

func TestExtractUpdateCorruptZip(t *testing.T) {
	// 写一段非 zip 字节，OpenReader 应报错
	path := filepath.Join(t.TempDir(), "bad.zip")
	_ = os.WriteFile(path, []byte("this is not a zip"), 0644)
	if _, _, err := ExtractUpdate(path, t.TempDir()); err == nil {
		t.Fatal("损坏 zip 应报错")
	}
}

// ---- WriteUpdateScript ----

func TestWriteUpdateScriptContent(t *testing.T) {
	pid := 12345
	oldExe := `C:\Program Files\PortEye\porteye.exe`
	newExe := `D:\Temp\porteye_update.exe`
	newManifest := `D:\Temp\porteye_update.exe.manifest`

	batPath, err := WriteUpdateScript(pid, oldExe, newExe, newManifest)
	if err != nil {
		t.Fatalf("WriteUpdateScript 失败: %v", err)
	}
	// 路径应落在 %TEMP%\porteye_update_<pid>.bat
	if filepath.Base(batPath) != fmt.Sprintf("porteye_update_%d.bat", pid) {
		t.Errorf("bat 命名不符: %s", batPath)
	}
	content, err := os.ReadFile(batPath)
	if err != nil {
		t.Fatalf("读 bat 失败: %v", err)
	}
	s := string(content)
	// 无 BOM
	if len(content) >= 3 && content[0] == 0xEF && content[1] == 0xBB && content[2] == 0xBF {
		t.Error("bat 不应有 UTF-8 BOM")
	}
	// 行尾必须 CRLF：cmd.exe 在代码页 936 下解析 LF-only 的 .bat 会失败。
	// 防回归：每个 \n 都必须紧跟在一个 \r 之后（即裸 LF 计数为 0）。
	if lfCount, crlfCount := strings.Count(s, "\n"), strings.Count(s, "\r\n"); lfCount != crlfCount {
		t.Errorf("bat 行尾应为 CRLF，存在裸 LF：LF=%d CRLF=%d", lfCount, crlfCount)
	}

	mustContain := []string{
		"chcp 65001",            // 中文防乱码
		`start ""`,              // 带空标题引号
		":copyexe",              // exe 替换重试段
		":copymanifest",         // manifest 替换段
		"move /y",               // 覆盖替换
		"lss 10",                // 重试上限
		"timeout /t 1",          // 重试间隔
		fmt.Sprintf(`"%d"`, pid), // PID 等值判断
		`del "%~f0"`,            // 自删
		"pause",                 // 失败暂停
		"enabledelayedexpansion", // 延迟变量扩展
		// 带空格路径必须被引号包裹
		`"C:\Program Files\PortEye\porteye.exe"`,
		`"D:\Temp\porteye_update.exe"`,
	}
	for _, sub := range mustContain {
		if !strings.Contains(s, sub) {
			t.Errorf("bat 应包含 %q，实际内容:\n%s", sub, s)
		}
	}
}

func TestWriteUpdateScriptManifestPathDerived(t *testing.T) {
	// oldExe.manifest 应由 oldExe + ".manifest" 派生
	batPath, err := WriteUpdateScript(1, `C:\app\porteye.exe`, `C:\new.exe`, `C:\new.exe.manifest`)
	if err != nil {
		t.Fatal(err)
	}
	s := string(mustReadFile(t, batPath))
	if !strings.Contains(s, `"C:\app\porteye.exe.manifest"`) {
		t.Errorf("bat 应含 oldExe.manifest 派生路径，实际:\n%s", s)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s 失败: %v", path, err)
	}
	return b
}
