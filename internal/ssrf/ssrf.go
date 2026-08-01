// Input: context, errors, fmt, net, net/http, net/netip, time
// Output: func IsBlockedAddr(), func DialContext(), func NewTransport(), var ErrBlocked, var Enabled
// Pos: 工具层-SSRF 防护，在拨号阶段阻止对私有/保留地址的上游请求
//
// 本注释在文件修改时自动更新，同时触发 FOLDER_INDEX 和 PROJECT_INDEX 更新

// Package ssrf 提供 SSRF 防护：当启用时（SSRF_BLOCK_PRIVATE，默认 true），
// 拦截指向私有网段、环回、链路本地及云元数据地址的上游请求。
//
// 防 DNS rebinding：真正的校验发生在拨号阶段（DialContext），
// 解析出的 IP 与实际建立 TCP 连接的 IP 是同一个，消除"先检查再连接"的 TOCTOU 窗口。
package ssrf

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// ErrBlocked 表示目标地址被 SSRF 策略拦截。
var ErrBlocked = errors.New("ssrf: target address blocked")

// IsBlockedAddr 判断一个 IP 是否属于应拦截的范围（公开 API）。
// 供 httpclient 的 DialContext 在拨号阶段复用，确保校验的 IP 就是实际连接的 IP。
//
// 这是纯函数，无 TOCTOU 风险——校验的 IP 由调用方（DialContext）在拨号时
// 实际使用。历史上曾存在 IsBlocked/Check/MaybeCheck 这类"先解析再校验"的
// URL 级 API，但它们存在 DNS rebinding 窗口（解析与连接分离），已删除。
// 生产校验一律走 DialContext。
func IsBlockedAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsMulticast() || addr.IsUnspecified() {
		return true
	}
	// 云元数据端点 169.254.169.254 已被 IsLinkLocalUnicast 覆盖；
	// 显式列出常见的元数据 host 字面量以应对未来变更。
	host := addr.String()
	for _, blocked := range []string{"169.254.169.254", "fd00::"} {
		if host == blocked {
			return true
		}
	}
	return false
}

// Enabled 控制是否启用 SSRF 检查。
// 由 main 在启动时根据 cfg.SSRFBlockPrivate（默认 true）设置。
// 零值为 false 仅为兼容不经过 main 的测试环境——测试构造的 Parser/Server
// 不经 main.go，保持 Enabled=false 使其上游 httptest（监听 loopback）可正常工作。
var Enabled = false

// ── 受控拨号器（防 DNS rebinding） ──

// ipResolver 抽象 DNS 解析，便于测试注入。
// net.Resper 的 LookupNetIP 满足此接口。
type ipResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// resolver 允许测试注入自定义 DNS 解析器。默认使用 net.DefaultResolver。
var resolver ipResolver = net.DefaultResolver

// lookupIPs 解析 host 的所有 IP 地址（注入友好，便于测试）。
func lookupIPs(ctx context.Context, host string) ([]netip.Addr, error) {
	// 先尝试当作 IP 字面量
	if addr, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{addr}, nil
	}
	addrs, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	return addrs, nil
}

// DialContext 是受控的拨号函数，用于替换 http.Transport 的 DialContext。
//
// 它在拨号阶段完成 DNS 解析与 IP 校验，确保校验过的 IP 就是实际建立
// TCP 连接的 IP，消除"先检查再连接"的 TOCTOU 窗口（DNS rebinding）。
//
// 流程：
//  1. 解析 network/host/port（由 http.Transport 传入）
//  2. 对 host 做 DNS 解析（若为 IP 字面量则直接用）
//  3. 校验每个解析结果；任一为私有/保留地址则拒绝整个请求
//  4. 用校验过的第一个 IP 直连（不再让 OS 重新解析）
//
// 未启用（Enabled=false）时调用方不应使用此 dialer。
func DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("ssrf dial: %w", err)
	}

	ips, err := lookupIPs(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("ssrf dial: 解析 %s 失败: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("ssrf dial: %s 无 IP 记录", host)
	}

	// 校验所有候选 IP；任一私有即拒绝（保守策略）。
	for _, ip := range ips {
		if IsBlockedAddr(ip) {
			return nil, fmt.Errorf("%w: %s 解析到私有地址 %s", ErrBlocked, host, ip)
		}
	}

	// 用校验过的第一个 IP 直连，跳过 OS 的二次解析。
	// dialTimeout 设为较短值，避免阻塞在不可达地址上。
	d := &net.Dialer{Timeout: 15 * time.Second}
	return d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
}

// NewTransport 返回一个内置 SSRF 校验的 *http.Transport。
// 仅当 Enabled=true 时使用；校验发生在 DialContext 阶段。
func NewTransport() *http.Transport {
	return &http.Transport{
		DialContext: DialContext,
		// 复用默认的代理与 TLS 配置
		ForceAttemptHTTP2: true,
	}
}
