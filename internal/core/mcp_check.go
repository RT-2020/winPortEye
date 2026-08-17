package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// McpCheckResult 表示一次 MCP 自检的结果。
type McpCheckResult struct {
	OK        bool     `json:"ok"`        // MCP server 是否正常响应
	Message   string   `json:"message"`   // 结果说明
	Tools     []string `json:"tools"`     // 探测到的 tool 名称列表
	CostMs    int64    `json:"costMs"`    // 耗时（毫秒）
}

// CheckMcpServer 自检 MCP server 是否可用。
//
// 实现方式：以子进程方式 spawn 当前 exe 的 --mcp 模式，
// 通过 stdin 发送 initialize + tools/list，检查 stdout 返回。
// 整个过程不依赖任何外部 AI 客户端，纯本地验证。
//
// version 为当前程序版本号（空串按 "dev"），写入 initialize 的 clientInfo，
// 与真实客户端（主程序 --version 上报同一版本）保持口径一致。
//
// 自检是同步阻塞的，调用方应在 goroutine 中调用，避免卡 UI。
func CheckMcpServer(exePath string, timeout time.Duration, version string) McpCheckResult {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, exePath, "--mcp")

	// 用 io.Pipe 作 stdin，这样可以在"发完请求 → 等 server 处理 → 再关闭 stdin"
	// 之间留出时间，避免 stdin EOF 触发 server 提前退出导致 stdout 未刷新。
	stdinR, stdinW := io.Pipe()
	cmd.Stdin = stdinR

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// stderr 丢弃：MCP server 在 stdin 关闭时正常退出会 log.Fatal，属预期行为

	if err := cmd.Start(); err != nil {
		return McpCheckResult{
			OK:      false,
			Message: fmt.Sprintf("无法启动 MCP server 进程: %v", err),
			CostMs:  time.Since(start).Milliseconds(),
		}
	}

	// 发送 3 条 JSON-RPC 请求（clientInfo 版本与主程序 --version 口径一致）
	if version == "" {
		version = "dev"
	}
	clientInfo := fmt.Sprintf(`"clientInfo":{"name":"selfcheck","version":%q}`, version)
	requests := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},` + clientInfo + `}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n"
	io.WriteString(stdinW, requests)
	// 给 server 留出处理 + 刷 stdout 的时间，再关 stdin 触发其退出
	// 300ms 对本地 stdio 足够；超过则由外层 ctx timeout 兜底
	done := make(chan struct{})
	go func() {
		time.Sleep(300 * time.Millisecond)
		stdinW.Close()
		close(done)
	}()

	_ = cmd.Wait()
	<-done

	// 解析 stdout，逐行找 tools/list 的响应（id=2）
	tools := []string{}
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var msg struct {
			ID     any `json:"id"`
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(line), &msg) != nil {
			continue
		}
		// tools/list 响应的特征：id==2 且有 tools 字段
		if fmt.Sprint(msg.ID) == "2" && len(msg.Result.Tools) > 0 {
			for _, t := range msg.Result.Tools {
				tools = append(tools, t.Name)
			}
			break
		}
	}

	if len(tools) == 0 {
		return McpCheckResult{
			OK:      false,
			Message: "MCP server 启动但未返回 tool 列表（响应异常）",
			CostMs:  time.Since(start).Milliseconds(),
		}
	}

	return McpCheckResult{
		OK:      true,
		Message: fmt.Sprintf("MCP 服务正常，探测到 %d 个工具", len(tools)),
		Tools:   tools,
		CostMs:  time.Since(start).Milliseconds(),
	}
}
