package core

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ExcludedRange 表示一段被 Windows 内核预留的端口范围（无进程占用，但应用无法 bind）。
//
// 典型来源：Hyper-V / WSL2 / Docker Desktop 的 WinNAT 动态端口预留。
// 这些端口不会出现在 netstat / Get-NetTCPConnection 里（因为没有进程打开 socket），
// 但 TCP/IP 驱动在内核层标记为"不可绑定"，应用 bind() 会被拒绝。
type ExcludedRange struct {
	Protocol string // "tcp" / "udp"
	Family   string // "ipv4" / "ipv6"
	Start    uint16 // 起始端口（含）
	End      uint16 // 结束端口（含）
	Managed  bool   // 是否为"管理的端口排除"（netsh 输出末尾带 *，通常为系统级）
}

// 排除范围查询的可用状态，驱动 UI 的降级提示。
type ExcludedStatus int

const (
	ExcludedStatusUnknown   ExcludedStatus = iota // 未检测（刚启动，预热未完成）
	ExcludedStatusOK                              // 检测成功，有数据
	ExcludedStatusUnavailable                     // netsh 不可用 / 超时 / 解析失败
)

// excludedCache 缓存 netsh 解析结果。排除范围不像端口那样频繁变动，
// 缓存 30 秒避免 watcher 每次刷新都跑 netsh（约 100ms 开销）。
var (
	excludedCache     []ExcludedRange
	excludedStatus    ExcludedStatus
	excludedCacheTime time.Time
	excludedCacheMu   sync.Mutex
)

const excludedCacheTTL = 30 * time.Second

// netsh 调用超时。netsh 正常 1-2 秒返回，给 5 秒余量，超时则放弃本次检测。
const netshTimeout = 5 * time.Second

// ListExcludedPortRanges 返回当前 Windows TCP/UDP 端口排除范围及检测状态。
// 结果带 30 秒缓存，避免频繁调用 netsh。
//
// 注意：缓存未命中时会同步调用 netsh（约 1-2 秒），会阻塞调用方。
// 仅供后台预热或非 UI 线程使用。UI 线程请用 ListExcludedPortRangesNoBlock。
func ListExcludedPortRanges() ([]ExcludedRange, ExcludedStatus, error) {
	excludedCacheMu.Lock()
	defer excludedCacheMu.Unlock()
	if time.Since(excludedCacheTime) < excludedCacheTTL && excludedStatus == ExcludedStatusOK {
		return excludedCache, excludedStatus, nil
	}

	result, err := fetchExcludedPortRanges()
	if err != nil {
		// netsh 不可用/超时/解析失败：标记为不可用，UI 据此降级提示
		excludedStatus = ExcludedStatusUnavailable
		excludedCacheTime = time.Now()
		return nil, excludedStatus, err
	}
	excludedCache = result
	excludedStatus = ExcludedStatusOK
	excludedCacheTime = time.Now()
	return result, excludedStatus, nil
}

// ListExcludedPortRangesNoBlock 只读缓存，绝不调用 netsh。
// 缓存未命中（刚启动、预热未完成）时返回 (nil, ExcludedStatusUnknown)——此时不提示，
// 等预热 goroutine 填充缓存后，下一次搜索自然就能命中。
// 专为 UI 线程设计，保证不卡顿、不弹控制台窗口。
func ListExcludedPortRangesNoBlock() ([]ExcludedRange, ExcludedStatus) {
	excludedCacheMu.Lock()
	defer excludedCacheMu.Unlock()
	return excludedCache, excludedStatus
}

// ClearExcludedCache 清除排除范围缓存（供测试和管理员重启后刷新使用）。
func ClearExcludedCache() {
	excludedCacheMu.Lock()
	excludedCache = nil
	excludedStatus = ExcludedStatusUnknown
	excludedCacheTime = time.Time{}
	excludedCacheMu.Unlock()
}

// fetchExcludedPortRanges 分别查询 TCP 和 UDP 的排除范围并合并。
// ipv4 任一协议查询失败即整体失败（降级为不可用，与现状语义一致）；
// ipv6 尽力查询，失败静默跳过（不降级、不进 error）。
func fetchExcludedPortRanges() ([]ExcludedRange, error) {
	var result []ExcludedRange
	// ipv4：失败即整体失败
	for _, proto := range []string{"tcp", "udp"} {
		ranges, err := queryNetsh("ipv4", proto)
		if err != nil {
			return nil, fmt.Errorf("查询 ipv4 %s 排除范围失败: %w", proto, err)
		}
		result = append(result, ranges...)
	}
	// ipv6：尽力查询，失败静默跳过
	for _, proto := range []string{"tcp", "udp"} {
		ranges, err := queryNetsh("ipv6", proto)
		if err != nil {
			continue
		}
		result = append(result, ranges...)
	}
	return result, nil
}

// queryNetsh 跑 `netsh interface <family> show excludedportrange protocol=<proto>` 并解析输出。
// family ∈ {"ipv4", "ipv6"}。
//
// 健壮性设计：
//   - HideWindow：隐藏 netsh 的控制台窗口（GUI 程序不能闪现黑窗）
//   - 超时保护：5 秒超时，避免极端情况下永久阻塞 UI
//   - 解析容错：parseNetshOutput 对非数据行静默跳过，不报错
func queryNetsh(family, proto string) ([]ExcludedRange, error) {
	ctx, cancel := context.WithTimeout(context.Background(), netshTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "netsh", "interface", family, "show", "excludedportrange", "protocol="+proto)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseNetshOutput(string(out), family, proto), nil
}

// parseNetshOutput 解析 netsh 的输出文本为排除范围列表。
// 单独抽出便于单测（不依赖真实 netsh）。family ∈ {"ipv4", "ipv6"}，透传到结果。
//
// 多语言兼容：解析策略只认"每行前两个字段都是纯数字"的行，
// 不依赖表头文案（中文"开始端口"或英文"Start Port"都不是纯数字，自动跳过）。
// 因此对中文/英文/其他语言的 Windows 输出均兼容。
//
// 输出格式示例（中文）：
//
//	协议 tcp 端口排除范围
//
//	开始端口    结束端口
//	----------    --------
//	        80          80
//	     8250        8349
//	   50000       50059     *
//
// 英文版表头是 "Start Port    End Port"，但同样不是纯数字，解析逻辑一致。
func parseNetshOutput(output, family, proto string) []ExcludedRange {
	var result []ExcludedRange
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// 只认前两个字段都是纯整数的行——表头、分隔线、说明文字都会被跳过
		start, err1 := strconv.Atoi(fields[0])
		end, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		if start <= 0 || end <= 0 || start > 65535 || end > 65535 || start > end {
			continue
		}
		managed := len(fields) >= 3 && fields[len(fields)-1] == "*"
		result = append(result, ExcludedRange{
			Protocol: proto,
			Family:   family,
			Start:    uint16(start),
			End:      uint16(end),
			Managed:  managed,
		})
	}
	return result
}

// FindExcludedPort 在排除范围里查找指定端口，返回命中的范围（可能有多个协议命中）。
// 没命中返回 nil。
func FindExcludedPort(port uint16, ranges []ExcludedRange) []ExcludedRange {
	var matched []ExcludedRange
	for _, r := range ranges {
		if port >= r.Start && port <= r.End {
			matched = append(matched, r)
		}
	}
	return matched
}
