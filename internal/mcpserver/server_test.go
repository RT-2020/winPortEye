package mcpserver

import (
	"context"
	"encoding/csv"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// 表头列序（与 exportPortsHandler 的 Write 保持一致）。
const exportCSVHeader = "protocol,local_addr,local_port,remote_addr,state,pid,process_name,process_path"

// TestExportPortsCSV 验证 export_ports 输出合法 CSV：
// 首行为表头、数据行数 = Count、每行 8 列（7 个逗号）。
// 注意：handler 内部自己枚举连接，无法注入带逗号/引号的样本，
// 这里以「每行列数一致」间接验证转义没有破坏列结构。
func TestExportPortsCSV(t *testing.T) {
	_, out, err := exportPortsHandler(context.Background(), &mcp.CallToolRequest{}, exportPortsInput{})
	if err != nil {
		t.Fatalf("exportPortsHandler 失败: %v", err)
	}
	if out.Count <= 0 {
		t.Fatal("端口连接数应 > 0")
	}
	if out.Csv == "" {
		t.Fatal("Csv 不应为空")
	}

	records, err := csv.NewReader(strings.NewReader(out.Csv)).ReadAll()
	if err != nil {
		t.Fatalf("输出不是合法 CSV: %v", err)
	}
	if len(records) != out.Count+1 {
		t.Errorf("CSV 行数应为 Count+1=%d（含表头），实为 %d", out.Count+1, len(records))
	}
	for i, rec := range records {
		if len(rec) != 8 {
			t.Fatalf("第 %d 行列数应为 8（7 个逗号），实为 %d: %v", i, len(rec), rec)
		}
	}
	if strings.Join(records[0], ",") != exportCSVHeader {
		t.Errorf("首行应为表头 %q，实为 %q", exportCSVHeader, strings.Join(records[0], ","))
	}
}

// TestExportPortsFilterTCP 验证 Protocol="tcp" 时输出全部为 tcp 行。
func TestExportPortsFilterTCP(t *testing.T) {
	_, out, err := exportPortsHandler(context.Background(), &mcp.CallToolRequest{}, exportPortsInput{Protocol: "tcp"})
	if err != nil {
		t.Fatalf("exportPortsHandler 失败: %v", err)
	}
	if out.Count <= 0 {
		t.Fatal("TCP 连接数应 > 0")
	}
	records, err := csv.NewReader(strings.NewReader(out.Csv)).ReadAll()
	if err != nil {
		t.Fatalf("输出不是合法 CSV: %v", err)
	}
	for i, rec := range records[1:] { // 跳过表头行
		if rec[0] != "tcp" {
			t.Errorf("第 %d 行协议列应为 tcp，实为 %q", i+1, rec[0])
		}
	}
}

// TestProcessTree 验证 process_tree 枚举全部进程：
// Count 与列表长度一致、含当前测试进程、其 ParentPid>0 且 Name 非空、PID 无重复。
func TestProcessTree(t *testing.T) {
	_, out, err := processTreeHandler(context.Background(), &mcp.CallToolRequest{}, processTreeInput{})
	if err != nil {
		t.Fatalf("processTreeHandler 失败: %v", err)
	}
	if out.Count <= 0 {
		t.Fatal("进程数应 > 0")
	}
	if len(out.Processes) != out.Count {
		t.Errorf("Processes 长度应与 Count 一致: %d vs %d", len(out.Processes), out.Count)
	}

	self := int32(os.Getpid())
	seen := make(map[int32]bool, len(out.Processes))
	foundSelf := false
	for _, p := range out.Processes {
		if seen[p.Pid] {
			t.Errorf("PID %d 重复出现", p.Pid)
		}
		seen[p.Pid] = true
		if p.Pid == self {
			foundSelf = true
			if p.ParentPid <= 0 {
				t.Errorf("当前进程 ParentPid 应 > 0，实为 %d", p.ParentPid)
			}
			if p.Name == "" {
				t.Error("当前进程 Name 不应为空")
			}
		}
	}
	if !foundSelf {
		t.Errorf("进程列表未包含当前测试进程 PID %d", self)
	}
}
