// Package mcpserver 实现基于 stdio 的 MCP server，暴露 7 个端口监控工具。
// 由 main.go 在 --mcp 模式下调用。
package mcpserver

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"strconv"

	"win/internal/core"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Run 启动 MCP stdio server，阻塞直到客户端断开。
// version 为程序版本号（空串按 "dev"），随 initialize 响应上报给客户端。
// 日志只能写 stderr（log 默认即 stderr），stdout 严格只用于 JSON-RPC。
func Run(version string) {
	if version == "" {
		version = "dev"
	}
	srv := mcp.NewServer(
		&mcp.Implementation{Name: "porteye", Version: version},
		nil, // 用默认 ServerOptions
	)

	// 7 个 tool 注册
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_ports",
		Description: "列出当前所有网络端口连接及其占用进程。可按协议/状态/端口号过滤。返回 Connection 数组的 JSON。",
	}, listPortsHandler)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "find_port",
		Description: "查找占用指定本地端口的进程。返回匹配连接的 JSON 数组（一个端口可能被多个进程占用）。",
	}, findPortHandler)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_process",
		Description: "查询指定 PID 的进程详情（名称、可执行文件路径、命令行、创建时间）。",
	}, getProcessHandler)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "kill_process",
		Description: "终止指定 PID 的进程。用户态进程直接终止（零UAC）；系统进程会弹出 UAC 提权（需要用户在桌面会话点击同意，MCP 无桌面环境下会失败）。",
	}, killProcessHandler)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "kill_by_port",
		Description: "按端口号终止占用进程（先查后杀）。返回每个受影响 PID 的结果。",
	}, killByPortHandler)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "export_ports",
		Description: "把端口连接导出为 CSV 文本（含表头），供存档/分析/贴表格。支持协议/状态过滤。",
	}, exportPortsHandler)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "process_tree",
		Description: "枚举全部进程及其父 PID（含 PID、进程名、可执行文件路径），可据此构建进程树、判断杀进程的连带影响。",
	}, processTreeHandler)

	// 阻塞运行 stdio server
	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("MCP server 运行失败: %v", err)
	}
}

// ---- Tool 输入/输出结构 ----

// ListPorts
type listPortsInput struct {
	Protocol string `json:"protocol,omitempty" jsonschema:"协议过滤：tcp/udp/all（默认 all）"`
	State    string `json:"state,omitempty" jsonschema:"状态过滤（如 LISTEN/ESTABLISHED），默认不过滤"`
	Port     int    `json:"port,omitempty" jsonschema:"只返回该本地端口（0-65535，0 表示不过滤）"`
}
type listPortsOutput struct {
	Connections []core.Connection `json:"connections"`
	Count       int               `json:"count"`
}

func listPortsHandler(ctx context.Context, req *mcp.CallToolRequest, in listPortsInput) (*mcp.CallToolResult, listPortsOutput, error) {
	conns, err := core.ListConnections(kindFromProtocol(in.Protocol))
	if err != nil {
		return nil, listPortsOutput{}, fmt.Errorf("枚举端口失败: %w", err)
	}
	// 过滤状态/端口
	if in.State != "" || in.Port > 0 {
		filtered := conns[:0]
		for _, c := range conns {
			if in.State != "" && c.State != in.State {
				continue
			}
			if in.Port > 0 && int(c.LocalPort) != in.Port {
				continue
			}
			filtered = append(filtered, c)
		}
		conns = filtered
	}
	return nil, listPortsOutput{Connections: conns, Count: len(conns)}, nil
}

// FindPort
type findPortInput struct {
	Port     int    `json:"port" jsonschema:"要查询的本地端口号，1-65535（必填）"`
	Protocol string `json:"protocol,omitempty" jsonschema:"协议过滤：tcp/udp/all（默认 all）"`
}
type findPortOutput struct {
	Connections []core.Connection `json:"connections"`
	Count       int               `json:"count"`
}

func findPortHandler(ctx context.Context, req *mcp.CallToolRequest, in findPortInput) (*mcp.CallToolResult, findPortOutput, error) {
	if in.Port <= 0 {
		return nil, findPortOutput{}, fmt.Errorf("port 参数必填且 > 0")
	}
	conns, err := core.FindPort(uint16(in.Port), kindFromProtocol(in.Protocol))
	if err != nil {
		return nil, findPortOutput{}, err
	}
	return nil, findPortOutput{Connections: conns, Count: len(conns)}, nil
}

// GetProcess
type getProcessInput struct {
	Pid int32 `json:"pid" jsonschema:"进程 ID"`
}
type getProcessOutput struct {
	Process core.ProcessInfo `json:"process"`
}

func getProcessHandler(ctx context.Context, req *mcp.CallToolRequest, in getProcessInput) (*mcp.CallToolResult, getProcessOutput, error) {
	info, err := core.GetProcessInfo(in.Pid)
	if err != nil {
		return nil, getProcessOutput{}, fmt.Errorf("查询进程失败: %w", err)
	}
	return nil, getProcessOutput{Process: info}, nil
}

// KillProcess
type killProcessInput struct {
	Pid int32 `json:"pid" jsonschema:"要终止的进程 ID"`
}
type killProcessOutput struct {
	Result core.KillResult `json:"result"`
}

func killProcessHandler(ctx context.Context, req *mcp.CallToolRequest, in killProcessInput) (*mcp.CallToolResult, killProcessOutput, error) {
	res := core.KillProcess(in.Pid)
	return nil, killProcessOutput{Result: res}, nil
}

// KillByPort
type killByPortInput struct {
	Port     int    `json:"port" jsonschema:"端口号，1-65535"`
	Protocol string `json:"protocol,omitempty" jsonschema:"协议过滤：tcp/udp/all（默认 all）"`
}
type killByPortOutput struct {
	Results []core.KillResult `json:"results"`
}

func killByPortHandler(ctx context.Context, req *mcp.CallToolRequest, in killByPortInput) (*mcp.CallToolResult, killByPortOutput, error) {
	if in.Port <= 0 {
		return nil, killByPortOutput{}, fmt.Errorf("port 参数必填且 > 0")
	}
	results, err := core.KillByPort(uint16(in.Port), kindFromProtocol(in.Protocol))
	if err != nil {
		return nil, killByPortOutput{}, err
	}
	// 便于日志：把结果序列化后输出到 stderr
	if data, err := json.Marshal(results); err == nil {
		log.Printf("kill_by_port %d 结果: %s", in.Port, string(data))
	}
	return nil, killByPortOutput{Results: results}, nil
}

// kindFromProtocol 把工具入参的协议串映射为 core.FilterKind。
// ""/all → KindAll，tcp → KindTCP，udp → KindUDP；其他值按 all 处理。
func kindFromProtocol(p string) core.FilterKind {
	switch p {
	case "tcp":
		return core.KindTCP
	case "udp":
		return core.KindUDP
	default:
		return core.KindAll
	}
}

// ExportPorts
type exportPortsInput struct {
	Protocol string `json:"protocol,omitempty" jsonschema:"协议过滤：tcp/udp/all（默认 all）"`
	State    string `json:"state,omitempty" jsonschema:"状态过滤（如 LISTEN/ESTABLISHED），默认不过滤"`
}
type exportPortsOutput struct {
	Csv   string `json:"csv"`   // CSV 文本（UTF-8，含表头）
	Count int    `json:"count"` // 数据行数（不含表头）
}

func exportPortsHandler(ctx context.Context, req *mcp.CallToolRequest, in exportPortsInput) (*mcp.CallToolResult, exportPortsOutput, error) {
	conns, err := core.ListConnections(kindFromProtocol(in.Protocol))
	if err != nil {
		return nil, exportPortsOutput{}, fmt.Errorf("枚举端口失败: %w", err)
	}
	// 状态过滤（与 listPortsHandler 同款）
	if in.State != "" {
		filtered := conns[:0]
		for _, c := range conns {
			if c.State != in.State {
				continue
			}
			filtered = append(filtered, c)
		}
		conns = filtered
	}

	// 生成 CSV：列序固定 8 列，含逗号/引号的字段由 csv 自动转义（这也是选 CSV 的原因）。
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.Write([]string{"protocol", "local_addr", "local_port", "remote_addr", "state", "pid", "process_name", "process_path"})
	for _, c := range conns {
		w.Write([]string{
			string(c.Protocol),
			c.LocalAddr,
			strconv.Itoa(int(c.LocalPort)),
			c.RemoteAddr,
			c.State,
			strconv.Itoa(int(c.Pid)),
			c.ProcessName,
			c.ProcessPath,
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, exportPortsOutput{}, fmt.Errorf("生成 CSV 失败: %w", err)
	}
	return nil, exportPortsOutput{Csv: buf.String(), Count: len(conns)}, nil
}

// ProcessTree
type processTreeInput struct {
	// 无参数
}
type processTreeOutput struct {
	Processes []core.ProcessInfo `json:"processes"`
	Count     int                `json:"count"`
}

func processTreeHandler(ctx context.Context, req *mcp.CallToolRequest, in processTreeInput) (*mcp.CallToolResult, processTreeOutput, error) {
	procs, err := core.ListProcesses()
	if err != nil {
		return nil, processTreeOutput{}, fmt.Errorf("枚举进程失败: %w", err)
	}
	return nil, processTreeOutput{Processes: procs, Count: len(procs)}, nil
}
