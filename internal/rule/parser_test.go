package rule

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/script-hub-org/script-hub/internal/config"
)

// parseLocal runs the parser pipeline against a local text blob and returns the
// formatted output for the given target app. Mirrors the Parse() entrypoint but
// skips remote fetching.
func parseLocal(t *testing.T, body, target string, args map[string]string) string {
	t.Helper()
	p := &Parser{}
	input := ParseInput{TargetApp: target, Arguments: args}
	rules := p.parseRules(body, input)
	out := p.formatOutput(rules, target)
	out = strings.ReplaceAll(out, "t&zd;", ",")
	out = strings.ReplaceAll(out, " ;#", " #")
	return out
}

// P0 fix #17: dot shorthand prefix must consume the leading dot.
func TestDotPrefixConsumesDot(t *testing.T) {
	out := parseLocal(t, ".example.com\n*abc.net\n+xyz.org", "surge", nil)
	for _, want := range []string{"DOMAIN-SUFFIX,example.com", "DOMAIN-SUFFIX,abc.net", "DOMAIN-SUFFIX,xyz.org"} {
		if !strings.Contains(out, want) {
			t.Fatalf("want %q in output, got:\n%s", want, out)
		}
	}
	// must NOT contain the buggy form with a leftover dot
	if strings.Contains(out, "DOMAIN-SUFFIX,.") {
		t.Fatalf("leftover dot detected:\n%s", out)
	}
}

// P0 fix #16: nore=true appends no-resolve only to ip-cidr/cidr6, nothing else.
func TestNoResolveOnlyWhenRequested(t *testing.T) {
	// Without nore: no no-resolve anywhere.
	out := parseLocal(t, "IP-CIDR,1.2.3.0/24\nDOMAIN,foo.com", "surge", nil)
	if strings.Contains(out, "no-resolve") {
		t.Fatalf("unexpected no-resolve without nore:\n%s", out)
	}
	// With nore: only ip rules get no-resolve, domains do not.
	out = parseLocal(t, "IP-CIDR,1.2.3.0/24\nIP-CIDR6,::1/128\nDOMAIN,foo.com", "surge",
		map[string]string{"nore": "true"})
	if !strings.Contains(out, "IP-CIDR,1.2.3.0/24,no-resolve") {
		t.Fatalf("ip-cidr missing no-resolve:\n%s", out)
	}
	if !strings.Contains(out, "IP-CIDR6,::1/128,no-resolve") {
		t.Fatalf("ip-cidr6 missing no-resolve:\n%s", out)
	}
	if strings.Contains(out, "DOMAIN,foo.com,no-resolve") {
		t.Fatalf("domain wrongly got no-resolve:\n%s", out)
	}
}

// P0 fix #15: y (include) must uncomment lines that start with #.
func TestYUncomments(t *testing.T) {
	body := "#DOMAIN,keep.me\n#DOMAIN,skip.me\nDOMAIN,normal.com"
	out := parseLocal(t, body, "surge", map[string]string{"y": "keep.me"})
	if !strings.Contains(out, "DOMAIN,keep.me") {
		t.Fatalf("y failed to uncomment keep.me:\n%s", out)
	}
	// skip.me stays commented (dropped), normal survives
	if strings.Contains(out, "DOMAIN,skip.me") {
		t.Fatalf("skip.me should remain excluded:\n%s", out)
	}
	if !strings.Contains(out, "DOMAIN,normal.com") {
		t.Fatalf("normal rule dropped:\n%s", out)
	}
}

// P0 fix: x (exclude) must mark matched rules into the excluded section.
func TestXExcludes(t *testing.T) {
	body := "DOMAIN,evil.com\nDOMAIN,good.com"
	out := parseLocal(t, body, "surge", map[string]string{"x": "evil"})
	if strings.Contains(out, "DOMAIN,evil.com") && !strings.Contains(out, "#已排除") {
		t.Fatalf("evil.com should be in excluded section:\n%s", out)
	}
	if !strings.Contains(out, "DOMAIN,good.com") {
		t.Fatalf("good.com dropped:\n%s", out)
	}
}

// P0 fix #18: commas inside regex quantifiers {N,M} must survive parsing.
func TestCommaGuard(t *testing.T) {
	out := parseLocal(t, "URL-REGEX,^foo{1,2}$", "surge", nil)
	if !strings.Contains(out, "URL-REGEX,^foo{1,2}$") {
		t.Fatalf("quantifier comma lost:\n%s", out)
	}
}

// Logical rules OR/AND/NOT: Surge emits verbatim, Stash/Loon mark unsupported.
func TestLogicalRules(t *testing.T) {
	body := "OR,((DOMAIN,a.com),(DOMAIN,b.com))\nAND,((DOMAIN,c.com))"
	out := parseLocal(t, body, "surge", nil)
	if !strings.Contains(out, "OR,((DOMAIN,a.com),(DOMAIN,b.com))") {
		t.Fatalf("surge should emit OR verbatim:\n%s", out)
	}
	outStash := parseLocal(t, body, "stash", nil)
	if !strings.Contains(outStash, "#不支持的规则") {
		t.Fatalf("stash should mark OR unsupported:\n%s", outStash)
	}
}

// Smoke test for the full Parse entrypoint with local text.
func TestParseLocalText(t *testing.T) {
	p := NewParser(&config.Config{HTTPTimeout: 5})
	out, err := p.Parse(context.Background(), ParseInput{
		URLs:      []string{"http://local.text"},
		TargetApp: "surge",
		Arguments: map[string]string{"localtext": "DOMAIN,test.com\nIP-CIDR,1.2.3.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "DOMAIN,test.com") {
		t.Fatalf("localtext not parsed:\n%s", out.Content)
	}
}

// Egern and LanceX are Surge-compatible clients: their rule-set output must be
// identical to the Surge rule-set output (DOMAIN rules rendered verbatim, no
// Stash payload: prefix, no Loon rewriting).
func TestEgernLanceXRuleSetMatchesSurge(t *testing.T) {
	body := "DOMAIN,test.com\nDOMAIN-SUFFIX,example.com\nIP-CIDR,1.2.3.0/24,no-resolve"
	surgeOut := parseLocal(t, body, "surge-rule-set", nil)
	for _, target := range []string{"egern-rule-set", "lancex-rule-set"} {
		got := parseLocal(t, body, target, nil)
		if got != surgeOut {
			t.Fatalf("target %s did not match surge rule-set output.\nsurge:\n%s\ngot:\n%s", target, surgeOut, got)
		}
		if strings.Contains(got, "payload:") {
			t.Fatalf("target %s rendered as Stash payload:\n%s", target, got)
		}
	}
}

// ─────────── 域名集输出格式 ───────────

func TestDomainSet_NonStash(t *testing.T) {
	// Surge domain-set: DOMAIN,/DOMAIN-SUFFIX 前缀被剥离，DOMAIN-SUFFIX 变成 .
	body := "DOMAIN,foo.com\nDOMAIN-SUFFIX,bar.com\nIP-CIDR,1.2.3.0/24"
	out := parseLocal(t, body, "surge-domain-set", nil)
	if !strings.Contains(out, "foo.com") {
		t.Errorf("DOMAIN stripped → missing foo.com:\n%s", out)
	}
	if !strings.Contains(out, ".bar.com") {
		t.Errorf("DOMAIN-SUFFIX → missing .bar.com:\n%s", out)
	}
	if strings.Contains(out, "IP-CIDR") {
		t.Errorf("non-domain rule should be excluded from domain set:\n%s", out)
	}
	if !strings.Contains(out, "#域名规则数量:2") {
		t.Errorf("missing count header:\n%s", out)
	}
}

func TestDomainSet_Stash(t *testing.T) {
	body := "DOMAIN,foo.com\nDOMAIN-SUFFIX,bar.com,direct\nIP-CIDR,1.2.3.0/24"
	out := parseLocal(t, body, "stash-domain-set", nil)
	if !strings.Contains(out, "foo.com") {
		t.Errorf("missing foo.com:\n%s", out)
	}
	// stash 应剥离策略部分
	if strings.Contains(out, "bar.com,direct") {
		t.Errorf("policy should be stripped for stash:\n%s", out)
	}
	if !strings.Contains(out, ".bar.com") {
		t.Errorf("stash domain-set should convert DOMAIN-SUFFIX to .bar:\n%s", out)
	}
}

func TestDomainSet_EmptyDomainRules(t *testing.T) {
	// 全是非域名规则 → 返回空（domainRules 为空）
	out := parseLocal(t, "IP-CIDR,1.2.3.0/24\nIP-CIDR6,::1/128", "surge-domain-set", nil)
	if out != "" {
		t.Errorf("no domain rules → want empty, got:\n%s", out)
	}
}

func TestDomainSet2(t *testing.T) {
	body := "DOMAIN,foo.com\nIP-CIDR,1.2.3.0/24\nDST-PORT,8080"
	out := parseLocal(t, body, "surge-domain-set2", nil)
	if !strings.Contains(out, "IP-CIDR,1.2.3.0/24") {
		t.Errorf("non-domain rule missing:\n%s", out)
	}
	if strings.Contains(out, "DOMAIN,foo.com") {
		t.Errorf("domain rule should be excluded from domain-set2:\n%s", out)
	}
	if !strings.Contains(out, "#非域名规则数量:") {
		t.Errorf("missing count header:\n%s", out)
	}
}

func TestDomainSet2_StashPayload(t *testing.T) {
	out := parseLocal(t, "IP-CIDR,1.2.3.0/24", "stash-domain-set2", nil)
	if !strings.Contains(out, "payload:") {
		t.Errorf("stash domain-set2 should have payload: prefix:\n%s", out)
	}
}

func TestDomainSet2_Empty(t *testing.T) {
	out := parseLocal(t, "DOMAIN,foo.com\nDOMAIN-SUFFIX,bar.com", "surge-domain-set2", nil)
	if out != "" {
		t.Errorf("no non-domain rules → want empty, got:\n%s", out)
	}
}

// ─────────── formatLoonRule 路径 ───────────

func TestLoon_UnsupportedRuleTypesDropped(t *testing.T) {
	// DEST-PORT / PROTOCOL / PROCESS-NAME / OR / AND / NOT 都不应出现在 Loon 输出
	body := "DST-PORT,8080\nPROCESS-NAME,binary\nPROTOCOL,udp\nDOMAIN,keep.com"
	out := parseLocal(t, body, "loon", nil)
	// 这些类型应被移至 "不支持的规则" 区块，不应出现在活动规则区
	activeStart := strings.Index(out, "#-----------------以下为解析后的规则")
	active := out[activeStart:]
	if strings.Contains(active, "DST-PORT") {
		t.Errorf("DST-PORT should be dropped for Loon:\n%s", out)
	}
	if strings.Contains(active, "PROCESS-NAME") {
		t.Errorf("PROCESS-NAME should be dropped for Loon:\n%s", out)
	}
	if strings.Contains(active, "PROTOCOL") {
		t.Errorf("PROTOCOL should be dropped for Loon:\n%s", out)
	}
	if !strings.Contains(out, "DOMAIN,keep.com") {
		t.Errorf("DOMAIN rule should be kept:\n%s", out)
	}
	// 不支持的应进入 otherStr
	if !strings.Contains(out, "#不支持的规则") {
		t.Errorf("unsupported rules should be reported:\n%s", out)
	}
}

// ─────────── Shadowrocket DST-PORT vs Surge DEST-PORT ───────────

func TestShadowrocket_KeepsDstPort(t *testing.T) {
	out := parseLocal(t, "DST-PORT,443", "shadowrocket-rule-set", nil)
	if !strings.Contains(out, "DST-PORT,443") {
		t.Errorf("Shadowrocket should keep DST-PORT:\n%s", out)
	}
}

func TestSurge_ConvertsDstPortToDestPort(t *testing.T) {
	out := parseLocal(t, "DST-PORT,443", "surge-rule-set", nil)
	if !strings.Contains(out, "DEST-PORT,443") {
		t.Errorf("Surge should convert DST-PORT → DEST-PORT:\n%s", out)
	}
}

func TestSurge_ProcessPathToProcessName(t *testing.T) {
	out := parseLocal(t, "PROCESS-PATH,/usr/bin/ssh", "surge-rule-set", nil)
	if !strings.Contains(out, "PROCESS-NAME,/usr/bin/ssh") {
		t.Errorf("PROCESS-PATH → PROCESS-NAME for Surge:\n%s", out)
	}
}

// ─────────── Stash PROCESS 路径推断 ───────────

func TestStash_ProcessPathInference(t *testing.T) {
	// PROCESS 类型，value 含 / → PROCESS-PATH，否则 PROCESS-NAME
	out := parseLocal(t, "PROCESS,/usr/bin/x\nPROCESS,binary", "stash", nil)
	if !strings.Contains(out, "PROCESS-PATH,/usr/bin/x") {
		t.Errorf("stash should infer PROCESS-PATH from /:\n%s", out)
	}
	if !strings.Contains(out, "PROCESS-NAME,binary") {
		t.Errorf("stash should infer PROCESS-NAME without /:\n%s", out)
	}
}

// ─────────── excluded 行（;# 前缀）───────────

func TestExcludedLine_EmittedVerbatim(t *testing.T) {
	// 排除路径通过 x 参数（关键字匹配）触发，会在预处理时为行加上 ;# 前缀
	body := "DOMAIN,excluded.com\nDOMAIN,keep.com"
	out := parseLocal(t, body, "surge", map[string]string{"x": "excluded"})
	activeStart := strings.Index(out, "#-----------------以下为解析后的规则")
	active := out[activeStart:]
	if strings.Contains(active, "excluded.com") {
		t.Errorf("excluded line should NOT appear as active rule:\n%s", out)
	}
	if !strings.Contains(out, "#已排除规则") {
		t.Errorf("excluded header missing:\n%s", out)
	}
	if !strings.Contains(out, "excluded.com") {
		t.Errorf("excluded rule should appear verbatim in excluded section:\n%s", out)
	}
}

// ─────────── 逻辑规则 OR/AND/NOT ───────────

func TestLogicalRules_SurgeVerbatim(t *testing.T) {
	body := "OR,((DOMAIN,a.com),(DOMAIN,b.com))"
	out := parseLocal(t, body, "surge", nil)
	if !strings.Contains(out, "OR,((DOMAIN,a.com),(DOMAIN,b.com))") {
		t.Errorf("logical rule should be verbatim for Surge:\n%s", out)
	}
}

func TestLogicalRules_LoonDroppedToOther(t *testing.T) {
	body := "OR,((DOMAIN,a.com))\nDOMAIN,keep.com"
	out := parseLocal(t, body, "loon", nil)
	// 逻辑规则应被移至 "不支持的规则" 区块，不应出现在活动规则区
	activeStart := strings.Index(out, "#-----------------以下为解析后的规则")
	active := out[activeStart:]
	if strings.Contains(active, "OR,((DOMAIN,a.com))") {
		t.Errorf("logical rule should be dropped from active set for Loon:\n%s", out)
	}
	if !strings.Contains(out, "#不支持的规则") {
		t.Errorf("logical rule should be reported as unsupported:\n%s", out)
	}
}

// ─────────── 参数：sni / pm / policy / nore ───────────

func TestSNI_AddsExtendedMatching(t *testing.T) {
	body := "DOMAIN,target.com\nDOMAIN,other.com"
	out := parseLocal(t, body, "surge", map[string]string{"sni": "target.com"})
	if !strings.Contains(out, "DOMAIN,target.com,extended-matching") {
		t.Errorf("sni should add extended-matching:\n%s", out)
	}
	if strings.Contains(out, "other.com,extended-matching") {
		t.Errorf("sni should NOT match other.com:\n%s", out)
	}
}

func TestSNI_SkipsIPRules(t *testing.T) {
	body := "IP-CIDR,1.2.3.0/24"
	out := parseLocal(t, body, "surge", map[string]string{"sni": "1.2.3"})
	if strings.Contains(out, "extended-matching") {
		t.Errorf("IP rules should not get extended-matching:\n%s", out)
	}
}

func TestPreMatching_Added(t *testing.T) {
	body := "DOMAIN,a.com"
	out := parseLocal(t, body, "surge", map[string]string{"sni": "a.com", "pm": "a.com"})
	if !strings.Contains(out, "extended-matching,pre-matching") {
		t.Errorf("both sni + pm should stack:\n%s", out)
	}
}

func TestPreMatching_OnlyPM(t *testing.T) {
	body := "DOMAIN,a.com"
	out := parseLocal(t, body, "surge", map[string]string{"pm": "a.com"})
	if !strings.Contains(out, "DOMAIN,a.com,pre-matching") {
		t.Errorf("pm alone should add pre-matching:\n%s", out)
	}
}

func TestPolicy_Appended(t *testing.T) {
	body := "DOMAIN,foo.com"
	out := parseLocal(t, body, "surge", map[string]string{"policy": "DIRECT"})
	if !strings.Contains(out, "DOMAIN,foo.com,DIRECT") {
		t.Errorf("policy should be appended:\n%s", out)
	}
}

func TestPolicy_NotOverwrittenWhenPresent(t *testing.T) {
	// 当行本身已带策略时，policy 参数不应覆盖
	body := "DOMAIN,foo.com,REJECT"
	out := parseLocal(t, body, "surge", map[string]string{"policy": "DIRECT"})
	if strings.Contains(out, "DIRECT") {
		t.Errorf("policy arg should not override existing REJECT:\n%s", out)
	}
}

// ─────────── 规则类型规范化 ───────────

func TestNormalizeRuleType_IP6CIDR(t *testing.T) {
	out := parseLocal(t, "IP6-CIDR,2001:db8::/32", "surge", nil)
	if !strings.Contains(out, "IP-CIDR6,2001:db8::/32") {
		t.Errorf("IP6-CIDR → IP-CIDR6:\n%s", out)
	}
}

func TestNormalizeRuleType_HOSTToDOMAIN(t *testing.T) {
	out := parseLocal(t, "HOST,foo.com", "surge", nil)
	if !strings.Contains(out, "DOMAIN,foo.com") {
		t.Errorf("HOST → DOMAIN:\n%s", out)
	}
}

// ─────────── no-resolve 标志 ───────────

func TestNoResolve_FromLine(t *testing.T) {
	out := parseLocal(t, "IP-CIDR,1.2.3.0/24,no-resolve", "surge", nil)
	if !strings.Contains(out, "IP-CIDR,1.2.3.0/24,no-resolve") {
		t.Errorf("no-resolve should be preserved:\n%s", out)
	}
}

// ─────────── Parse() 全流程（含 GetResponse / 404 / local.text）───────────

func TestParse_GetResponseRoundtrip(t *testing.T) {
	p := NewParser(config.LoadConfig())
	out, err := p.Parse(context.Background(), ParseInput{
		URLs:      []string{"http://local.text"},
		TargetApp: "surge",
		Arguments: map[string]string{"localtext": "DOMAIN,foo.com"},
	})
	if err != nil {
		t.Fatalf("Parse err: %v", err)
	}
	rd := out.GetResponse()
	if rd.Status != 200 {
		t.Errorf("status = %d", rd.Status)
	}
	if !strings.Contains(rd.Body, "DOMAIN,foo.com") {
		t.Errorf("body missing rule:\n%s", rd.Body)
	}
	if rd.Headers["Access-Control-Allow-Origin"] != "*" {
		t.Errorf("CORS header missing: %v", rd.Headers)
	}
}

func TestParse_URLEncodingDecodedByDecodeURL(t *testing.T) {
	// decodeURL 把空格替换为 %20 — 通过 httptest 验证
	srv := startBodyServer(t, 200, "DOMAIN,from-server.com")
	p := NewParser(config.LoadConfig())
	// 给一个带空格的 URL（decodeURL 会把它转 %20）
	out, err := p.Parse(context.Background(), ParseInput{
		URLs:      []string{srv.URL + "/x y"},
		TargetApp: "surge",
	})
	// 不应 panic；服务端会收到 %20 编码后的路径
	if err != nil {
		t.Errorf("Parse with space URL err: %v", err)
	}
	_ = out
}

func TestParse_404Handled(t *testing.T) {
	srv := startBodyServer(t, 404, "not found body")
	p := NewParser(config.LoadConfig())
	out, err := p.Parse(context.Background(), ParseInput{
		URLs:      []string{srv.URL},
		TargetApp: "surge",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// 404 注入 #!error=404 注释
	if !strings.Contains(out.Content, "404") {
		t.Errorf("404 marker missing:\n%s", out.Content)
	}
}

func TestParse_EmptyBodyReturns200(t *testing.T) {
	p := NewParser(config.LoadConfig())
	out, err := p.Parse(context.Background(), ParseInput{
		URLs:      []string{},
		TargetApp: "surge",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.Status != 200 || out.Content != "" {
		t.Errorf("empty input → status=%d content=%q", out.Status, out.Content)
	}
}

// ─────────── decodeURL 单元测试 ───────────

func TestDecodeURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"plain", "plain"},
		{"with space", "with%20space"},
		{"already%20encoded", "already%20encoded"},
		{"http://x.com/a b/c", "http://x.com/a%20b/c"},
	}
	for _, c := range cases {
		got, err := decodeURL(c.in)
		if err != nil {
			t.Errorf("decodeURL(%q) err: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("decodeURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ─────────── normalizeRuleType / hoStToHost 直接单元测试 ───────────

func TestNormalizeRuleType_Direct(t *testing.T) {
	cases := map[string]string{
		"  ip6-cidr":   "IP-CIDR6",
		"DEST-PORT":    "DST-PORT",
		"HOST-WILDCARD": "HO-ST-WILDCARD",
		"HOST":         "DOMAIN",
		"DOMAIN-SUFFIX": "DOMAIN-SUFFIX",
		"  ":           "",
	}
	for in, want := range cases {
		if got := normalizeRuleType(in); got != want {
			t.Errorf("normalizeRuleType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHoStToHost(t *testing.T) {
	cases := map[string]string{
		"HO-ST-WILDCARD,*.com": "HOST-WILDCARD,*.com",
		"ho-st-something":      "HOST-something",
		"DOMAIN,foo":           "DOMAIN,foo", // 不变
	}
	for in, want := range cases {
		if got := hoStToHost(in); got != want {
			t.Errorf("hoStToHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsUnsupportedForTarget(t *testing.T) {
	// Stash: HO-ST, U..., PROTOCOL, OR, AND, NOT
	if !isUnsupportedForTarget("HO-ST-WILDCARD", true, false, false) {
		t.Error("stash should reject HO-ST")
	}
	if !isUnsupportedForTarget("USER-AGENT", true, false, false) {
		t.Error("stash should reject U-prefix (USER-AGENT)")
	}
	if !isUnsupportedForTarget("OR", true, false, false) {
		t.Error("stash should reject OR")
	}
	if isUnsupportedForTarget("DOMAIN", true, false, false) {
		t.Error("stash should accept DOMAIN")
	}
	// Loon: HO-ST, DST-PORT, PROTOCOL, PROCESS-NAME, OR, AND, NOT
	if !isUnsupportedForTarget("DST-PORT", false, true, false) {
		t.Error("loon should reject DST-PORT")
	}
	if !isUnsupportedForTarget("PROCESS-NAME", false, true, false) {
		t.Error("loon should reject PROCESS-NAME")
	}
	if isUnsupportedForTarget("DOMAIN", false, true, false) {
		t.Error("loon should accept DOMAIN")
	}
	// Surge/Shadowrocket: only HO-ST
	if !isUnsupportedForTarget("HO-ST-X", false, false, true) {
		t.Error("surge should reject HO-ST")
	}
	if isUnsupportedForTarget("DST-PORT", false, false, true) {
		t.Error("surge should accept DST-PORT (converts to DEST-PORT)")
	}
}

// ─────────── Egern/LanceX 走 Surge 路径 ───────────

func TestEgernLanceX_TreatedAsSurge(t *testing.T) {
	for _, target := range []string{"egern-rule-set", "lancex-rule-set"} {
		out := parseLocal(t, "DST-PORT,443", target, nil)
		if !strings.Contains(out, "DEST-PORT,443") {
			t.Errorf("%s should behave like Surge (DEST-PORT):\n%s", target, out)
		}
	}
}

// ─────────── helpers ───────────

// startBodyServer 启动一个返回固定 status/body 的 httptest server。
func startBodyServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

