package ssrf

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"testing"
	"time"
)

func TestIsBlocked_PrivateIPs(t *testing.T) {
	cases := []string{
		"http://127.0.0.1/x",
		"http://localhost/x",
		"http://10.0.0.1/x",
		"http://192.168.1.1/x",
		"http://172.16.0.1/x",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/x",
		"http://0.0.0.0/x",
	}
	for _, c := range cases {
		if !IsBlocked(c) {
			t.Errorf("expected blocked: %s", c)
		}
	}
}

func TestIsBlocked_PublicIPs(t *testing.T) {
	// 公网 IP 不拦截
	if IsBlocked("http://8.8.8.8/x") {
		t.Error("8.8.8.8 should not be blocked")
	}
	// 公网域名（解析失败时不拦截，交由 fetch 处理）
	// 这里用一个肯定能解析的公网域名
	if IsBlocked("http://example.com/") {
		t.Log("note: example.com resolved to private? (depends on env) — acceptable")
	}
}

func TestMaybeCheck_DisabledByDefault(t *testing.T) {
	old := Enabled
	Enabled = false
	defer func() { Enabled = old }()
	if err := MaybeCheck("http://127.0.0.1/x"); err != nil {
		t.Errorf("disabled ssrf should not block: %v", err)
	}
}

func TestMaybeCheck_Enabled(t *testing.T) {
	old := Enabled
	Enabled = true
	defer func() { Enabled = old }()
	if err := MaybeCheck("http://127.0.0.1/x"); err != ErrBlocked {
		t.Errorf("enabled ssrf should block loopback, got %v", err)
	}
	if err := MaybeCheck("http://8.8.8.8/x"); err != nil {
		t.Errorf("enabled ssrf should not block public IP, got %v", err)
	}
}

// ── DialContext 防 DNS rebinding 测试 ──

// fakeResolver 允许测试注入预设的 DNS 解析结果，
// 模拟 DNS rebinding：同一域名在不同时刻返回不同 IP。
type fakeResolver struct {
	ips map[string][]netip.Addr
}

func (f *fakeResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	if ips, ok := f.ips[host]; ok {
		return ips, nil
	}
	return nil, fmt.Errorf("fakeResolver: no record for %s", host)
}

// TestDialContext_BlocksPrivateAddrAtDial 验证拨号阶段拦截私有 IP。
// 即使域名解析返回私有地址（模拟 rebinding），DialContext 也拒绝连接。
func TestDialContext_BlocksPrivateAddrAtDial(t *testing.T) {
	oldResolver := resolver
	defer func() { resolver = oldResolver }()
	resolver = &fakeResolver{
		ips: map[string][]netip.Addr{
			"evil.example.com": {netip.MustParseAddr("127.0.0.1")},
		},
	}

	ctx := context.Background()
	conn, err := DialContext(ctx, "tcp", "evil.example.com:80")
	if err == nil {
		if conn != nil {
			conn.Close()
		}
		t.Fatal("DialContext should reject connection to private IP (DNS rebinding)")
	}
	if !errors.Is(err, ErrBlocked) {
		t.Errorf("error should wrap ErrBlocked, got: %v", err)
	}
}

// TestDialContext_BlocksAnyPrivateCandidate 验证：即使候选 IP 列表混合公网与私有，
// 只要有一个私有就拒绝（保守策略）。
func TestDialContext_BlocksAnyPrivateCandidate(t *testing.T) {
	oldResolver := resolver
	defer func() { resolver = oldResolver }()
	resolver = &fakeResolver{
		ips: map[string][]netip.Addr{
			"mixed.example.com": {
				netip.MustParseAddr("8.8.8.8"),   // 公网
				netip.MustParseAddr("10.0.0.1"),  // 私有
			},
		},
	}

	ctx := context.Background()
	_, err := DialContext(ctx, "tcp", "mixed.example.com:80")
	if err == nil {
		t.Fatal("should reject when any candidate is private")
	}
	if !errors.Is(err, ErrBlocked) {
		t.Errorf("error should wrap ErrBlocked, got: %v", err)
	}
}

// TestDialContext_IPLiteralBlocked 验证 IP 字面量直接拦截。
func TestDialContext_IPLiteralBlocked(t *testing.T) {
	ctx := context.Background()
	_, err := DialContext(ctx, "tcp", "192.168.1.1:80")
	if err == nil {
		t.Fatal("should block private IP literal at dial")
	}
	if !errors.Is(err, ErrBlocked) {
		t.Errorf("error should wrap ErrBlocked, got: %v", err)
	}
}

// TestDialContext_PassesAllPublicCandidates 验证全部公网候选 IP 不拦截。
// 注意：不实际建立连接（目标不可达时 dial 会失败），只校验不返回 ErrBlocked。
func TestDialContext_PassesAllPublicCandidates(t *testing.T) {
	oldResolver := resolver
	defer func() { resolver = oldResolver }()
	resolver = &fakeResolver{
		ips: map[string][]netip.Addr{
			"ok.example.com": {
				netip.MustParseAddr("8.8.8.8"),
				netip.MustParseAddr("1.1.1.1"),
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := DialContext(ctx, "tcp", "ok.example.com:80")
	// 期望：校验通过（非 ErrBlocked）；连接可能成功或超时，但不应是 ErrBlocked。
	if err != nil && errors.Is(err, ErrBlocked) {
		t.Errorf("public candidates should not be blocked, got: %v", err)
	}
}

// TestIsBlockedAddr_Coverage 验证公开的 IsBlockedAddr 覆盖各类地址。
func TestIsBlockedAddr_Coverage(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "::1", "10.0.0.1", "192.168.1.1",
		"172.16.0.1", "169.254.169.254", "0.0.0.0",
	}
	for _, s := range blocked {
		if !IsBlockedAddr(netip.MustParseAddr(s)) {
			t.Errorf("IsBlockedAddr(%s) = false, want true", s)
		}
	}
	public := []string{"8.8.8.8", "1.1.1.1", "104.16.132.229"}
	for _, s := range public {
		if IsBlockedAddr(netip.MustParseAddr(s)) {
			t.Errorf("IsBlockedAddr(%s) = true, want false", s)
		}
	}
}

// TestNewTransport_ReturnsDialContext 验证 NewTransport 配置了受控 DialContext。
func TestNewTransport_ReturnsDialContext(t *testing.T) {
	tr := NewTransport()
	if tr == nil {
		t.Fatal("NewTransport returned nil")
	}
	if tr.DialContext == nil {
		t.Fatal("Transport.DialContext should be set to ssrf.DialContext")
	}
	if tr.ForceAttemptHTTP2 != true {
		t.Error("ForceAttemptHTTP2 should be true")
	}
}

// TestNewTransport_IntegrationBlocksPrivate 验证 NewTransport 返回的 Transport
// 在实际 HTTP 客户端中拦截私有地址（端到端，防 rebinding）。
func TestNewTransport_IntegrationBlocksPrivate(t *testing.T) {
	tr := NewTransport()
	hc := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	// 127.0.0.1 应在 DialContext 阶段被拦截
	_, err := hc.Get("http://127.0.0.1:9/never-reached")
	if err == nil {
		t.Fatal("request to loopback should be blocked by DialContext")
	}
	if !errors.Is(err, ErrBlocked) {
		t.Errorf("error should wrap ErrBlocked, got: %v", err)
	}
}
