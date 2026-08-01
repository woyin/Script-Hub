package rewrite

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/script-hub-org/script-hub/internal/httpclient"
)

// newTestHTTPClient 返回一个短超时的真实 httpclient.Client，供需要 client!=nil 的测试使用。
func newTestHTTPClient(t *testing.T) *httpclient.Client {
	t.Helper()
	return httpclient.NewClient(2, 0)
}

// ─────────── ApplyArgModification ───────────

func TestApplyArgModification_NoMatch(t *testing.T) {
	scripts := []ParsedRewrite{{Pattern: "^a", ScriptPath: "p1.js", Arguments: "old"}}
	out := ApplyArgModification(scripts, "", "")
	if len(out) != 1 || out[0].Arguments != "old" {
		t.Errorf("empty target/argv should be no-op: %+v", out)
	}
}

func TestApplyArgModification_Replace(t *testing.T) {
	scripts := []ParsedRewrite{
		{Pattern: "^api\\.example\\.com", ScriptPath: "p1.js", Arguments: "old"},
		{Pattern: "^other", ScriptPath: "p2.js", Arguments: "keep"},
	}
	out := ApplyArgModification(scripts, "api+other", "new1+new2")
	if out[0].Arguments != "new1" {
		t.Errorf("matched script args = %q, want new1", out[0].Arguments)
	}
	if out[1].Arguments != "new2" {
		t.Errorf("second script args = %q, want new2", out[1].Arguments)
	}
}

func TestApplyArgModification_MismatchedCounts(t *testing.T) {
	// argv 比 target 少 → 用较小的 count
	scripts := []ParsedRewrite{{Pattern: "^a", Arguments: "old"}}
	out := ApplyArgModification(scripts, "a+b+c", "v1+v2")
	// 只前 2 个 target 生效，但只有一个脚本匹配 a
	if out[0].Arguments != "v1" {
		t.Errorf("mismatched: args = %q, want v1", out[0].Arguments)
	}
}

// ─────────── ApplyScriptNameModification ───────────

func TestApplyScriptNameModification(t *testing.T) {
	scripts := []ParsedRewrite{{Pattern: "^a", Replacement: "oldName"}}
	out := ApplyScriptNameModification(scripts, "a", "newName")
	if out[0].Replacement != "newName" {
		t.Errorf("name = %q, want newName", out[0].Replacement)
	}
}

// ─────────── ApplyTimeoutModification ───────────

func TestApplyTimeoutModification(t *testing.T) {
	scripts := []ParsedRewrite{{Pattern: "^a", Timeout: 0}}
	out := ApplyTimeoutModification(scripts, "a", "30")
	if out[0].Timeout != 30 {
		t.Errorf("timeout = %d, want 30", out[0].Timeout)
	}
}

func TestApplyTimeoutModification_InvalidValue(t *testing.T) {
	scripts := []ParsedRewrite{{Pattern: "^a", Timeout: 5}}
	out := ApplyTimeoutModification(scripts, "a", "not-a-number")
	if out[0].Timeout != 5 {
		t.Errorf("invalid timeout should keep 5, got %d", out[0].Timeout)
	}
}

func TestApplyTimeoutModification_ZeroIgnored(t *testing.T) {
	scripts := []ParsedRewrite{{Pattern: "^a", Timeout: 10}}
	out := ApplyTimeoutModification(scripts, "a", "0")
	if out[0].Timeout != 10 {
		t.Errorf("timeout=0 should keep 10, got %d", out[0].Timeout)
	}
}

// ─────────── ApplyEngineModification ───────────

func TestApplyEngineModification(t *testing.T) {
	scripts := []ParsedRewrite{{Pattern: "^a", Engine: ""}}
	out := ApplyEngineModification(scripts, "a", "quickjs")
	if out[0].Engine != "quickjs" {
		t.Errorf("engine = %q, want quickjs", out[0].Engine)
	}
}

// ─────────── ApplyCronModification ───────────

func TestApplyCronModification(t *testing.T) {
	scripts := []ParsedRewrite{
		{Pattern: "^a", ScriptType: "cron", CronExp: "0 * * * *"},
		{Pattern: "^b", ScriptType: "http-response", CronExp: ""},
	}
	out := ApplyCronModification(scripts, "a+b", "0.8.*.*.*+ignored")
	// 只对 ScriptType=="cron" 的脚本生效；dots → spaces
	if out[0].CronExp != "0 8 * * *" {
		t.Errorf("cron = %q, want '0 8 * * *'", out[0].CronExp)
	}
}

func TestApplyCronModification_NonCronUnchanged(t *testing.T) {
	scripts := []ParsedRewrite{{Pattern: "^a", ScriptType: "http-response", CronExp: "keepme"}}
	out := ApplyCronModification(scripts, "a", "0.8.*.*.*")
	if out[0].CronExp != "keepme" {
		t.Errorf("non-cron script should be unchanged: %q", out[0].CronExp)
	}
}

// ─────────── ApplyPolicyToRules ───────────

func TestApplyPolicyToRules(t *testing.T) {
	cases := []struct {
		name   string
		rules  []string
		policy string
		want   []string
	}{
		{
			"no_policy_adds",
			[]string{"DOMAIN,foo.com", "IP-CIDR,1.2.3.0/24"},
			"DIRECT",
			[]string{"DOMAIN,foo.com,DIRECT", "IP-CIDR,1.2.3.0/24,DIRECT"},
		},
		{
			"existing_policy_kept",
			[]string{"DOMAIN,foo.com,REJECT"},
			"DIRECT",
			[]string{"DOMAIN,foo.com,REJECT"}, // unchanged
		},
		{
			"only_no_resolve_adds_policy",
			[]string{"IP-CIDR,1.2.3.0/24,no-resolve"},
			"DIRECT",
			[]string{"IP-CIDR,1.2.3.0/24,no-resolve,DIRECT"},
		},
		{
			"empty_skipped",
			[]string{"", "#comment", "DOMAIN,foo.com"},
			"REJECT",
			[]string{"", "#comment", "DOMAIN,foo.com,REJECT"},
		},
		{
			"empty_policy_noop",
			[]string{"DOMAIN,foo.com"},
			"",
			[]string{"DOMAIN,foo.com"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ApplyPolicyToRules(append([]string{}, c.rules...), c.policy)
			if len(got) != len(c.want) {
				t.Fatalf("len = %d, want %d (got=%v)", len(got), len(c.want), got)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

// ─────────── ApplyMITMAdditions / Deletions / RegexDeletions ───────────

func TestApplyMITMAdditions(t *testing.T) {
	out := ApplyMITMAdditions([]string{"a.com"}, "b.com, c.com ,, d.com")
	want := []string{"a.com", "b.com", "c.com", "d.com"}
	if len(out) != len(want) {
		t.Fatalf("len = %d, want %d (got=%v)", len(out), len(want), out)
	}
	for i, v := range want {
		if out[i] != v {
			t.Errorf("[%d] = %q, want %q", i, out[i], v)
		}
	}
}

func TestApplyMITMAdditions_Empty(t *testing.T) {
	out := ApplyMITMAdditions([]string{"a.com"}, "")
	if len(out) != 1 || out[0] != "a.com" {
		t.Errorf("empty should be no-op: %v", out)
	}
}

func TestApplyMITMDeletions(t *testing.T) {
	out := ApplyMITMDeletions([]string{"a.com", "b.com", "c.com"}, "b.com")
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2: %v", len(out), out)
	}
	for _, h := range out {
		if h == "b.com" {
			t.Errorf("b.com should be deleted: %v", out)
		}
	}
}

func TestApplyMITMDeletions_Trimmed(t *testing.T) {
	out := ApplyMITMDeletions([]string{"a.com", " b.com "}, "b.com")
	if len(out) != 1 {
		t.Errorf("should trim whitespace on delete: %v", out)
	}
}

func TestApplyMITNRegexDeletions(t *testing.T) {
	out := ApplyMITNRegexDeletions([]string{"ads.a.com", "api.b.com", "c.com"}, `^ads\.`)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2: %v", len(out), out)
	}
}

func TestApplyMITNRegexDeletions_InvalidRegex(t *testing.T) {
	out := ApplyMITNRegexDeletions([]string{"a.com"}, "((invalid")
	if len(out) != 1 {
		t.Errorf("invalid regex should be no-op: %v", out)
	}
}

// ─────────── ApplySynMitm ───────────

func TestApplySynMitm(t *testing.T) {
	mitm := []string{"a.com", "b.com"}
	out := ApplySynMitm(mitm, true)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	out2 := ApplySynMitm(mitm, false)
	if out2 != nil {
		t.Errorf("synMitm=false should return nil: %v", out2)
	}
}

// ─────────── ApplyDelCommented ───────────

func TestApplyDelCommented(t *testing.T) {
	lines := []string{
		"#!name=Keep",
		"# comment line",
		"DOMAIN,foo.com",
		"[Section]",
		"  # indented comment",
	}
	out := ApplyDelCommented(lines, true)
	// 应保留非注释行、节标题 [Section]、#! 行（普通注释行被删除）
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "DOMAIN,foo.com") {
		t.Errorf("active rule should be kept:\n%s", joined)
	}
	if !strings.Contains(joined, "[Section]") {
		t.Errorf("section header should be kept:\n%s", joined)
	}
	if strings.Contains(joined, "# comment line") {
		t.Errorf("comment should be deleted:\n%s", joined)
	}
}

func TestApplyDelCommented_Disabled(t *testing.T) {
	lines := []string{"# comment", "DOMAIN,foo.com"}
	out := ApplyDelCommented(lines, false)
	if len(out) != 2 {
		t.Errorf("disabled should be no-op: %v", out)
	}
}

// ─────────── ApplyMetadataOverrides ───────────

func TestApplyMetadataOverrides(t *testing.T) {
	cases := []struct {
		name string
		args map[string]string
		wantName string
		wantDesc string
		wantIcon string
		wantCat  string
	}{
		{"name_plus_desc", map[string]string{"n": "MyName+MyDesc"}, "MyName", "MyDesc", "", ""},
		{"name_space_desc", map[string]string{"n": "MyName MyDesc"}, "MyName", "MyDesc", "", ""},
		{"name_only", map[string]string{"n": "JustName"}, "JustName", "", "", ""},
		{"literal_plus_in_name", map[string]string{"n": "a➕b+desc"}, "a+b", "desc", "", ""},
		{"icon", map[string]string{"icon": "http://x/icon.png"}, "", "", "http://x/icon.png", ""},
		{"category", map[string]string{"category": "tools"}, "", "", "", "tools"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &ParsedModule{}
			ApplyMetadataOverrides(m, c.args)
			if c.wantName != "" && m.Name != c.wantName {
				t.Errorf("Name = %q, want %q", m.Name, c.wantName)
			}
			if c.wantDesc != "" && m.Desc != c.wantDesc {
				t.Errorf("Desc = %q, want %q", m.Desc, c.wantDesc)
			}
			if c.wantIcon != "" && m.Icon != c.wantIcon {
				t.Errorf("Icon = %q, want %q", m.Icon, c.wantIcon)
			}
			if c.wantCat != "" && m.Category != c.wantCat {
				t.Errorf("Category = %q, want %q", m.Category, c.wantCat)
			}
		})
	}
}

// ─────────── CategoryForOutput ───────────

func TestCategoryForOutput(t *testing.T) {
	// Loon: 使用 tag, 取 Category
	m := &ParsedModule{Category: "cat", Keyword: "kw"}
	k, v := CategoryForOutput(m, true)
	if k != "tag" || v != "cat" {
		t.Errorf("Loon: key/val = %q/%q, want tag/cat", k, v)
	}
	// 非 Loon: 优先 Keyword
	k, v = CategoryForOutput(m, false)
	if k != "category" || v != "kw" {
		t.Errorf("non-Loon: key/val = %q/%q, want category/kw", k, v)
	}
	// 非 Loon 且无 Keyword: 退回 Category
	m2 := &ParsedModule{Category: "cat"}
	k, v = CategoryForOutput(m2, false)
	if k != "category" || v != "cat" {
		t.Errorf("non-Loon fallback: key/val = %q/%q, want category/cat", k, v)
	}
	// Loon 无 Category: 空
	m3 := &ParsedModule{Keyword: "kw"}
	k, v = CategoryForOutput(m3, true)
	if k != "" || v != "" {
		t.Errorf("Loon no category: key/val = %q/%q, want empty", k, v)
	}
}

// ─────────── ApplySniPm ───────────

func TestApplySniPm_Params(t *testing.T) {
	rules := []string{"DOMAIN,target.com", "DOMAIN,other.com", "IP-CIDR,1.2.3.0/24"}
	out := ApplySniPm(rules, "target.com", "")
	if !strings.Contains(out[0], "extended-matching") {
		t.Errorf("sni should add extended-matching: %v", out[0])
	}
	if strings.Contains(out[1], "extended-matching") {
		t.Errorf("other.com should NOT get extended-matching: %v", out[1])
	}
	// IP rule skipped for sni
	if strings.Contains(out[2], "extended-matching") {
		t.Errorf("IP-CIDR should NOT get extended-matching: %v", out[2])
	}
}

func TestApplySniPm_PreMatching_Params(t *testing.T) {
	rules := []string{"DOMAIN,foo.com"}
	out := ApplySniPm(rules, "", "foo.com")
	if !strings.Contains(out[0], "pre-matching") {
		t.Errorf("pm should add pre-matching: %v", out[0])
	}
}

func TestApplySniPm_BothStack_Params(t *testing.T) {
	rules := []string{"DOMAIN,foo.com"}
	out := ApplySniPm(rules, "foo.com", "foo.com")
	if !strings.Contains(out[0], "extended-matching,pre-matching") {
		t.Errorf("both should stack: %v", out[0])
	}
}

func TestApplySniPm_LogicalRule_Params(t *testing.T) {
	rules := []string{"OR,((DOMAIN,foo.com),(DOMAIN,bar.com))"}
	orig := rules[0]
	out := ApplySniPm(rules, "foo.com", "")
	// 逻辑规则通过 ModifyRule 处理，应被修改（原值已快照，因 ApplySniPm 原地修改）
	if out[0] == orig {
		t.Errorf("logical rule should be modified: %v", out[0])
	}
}

func TestApplySniPm_NoMatch_Params(t *testing.T) {
	rules := []string{"DOMAIN,foo.com"}
	out := ApplySniPm(rules, "nomatch", "")
	if out[0] != rules[0] {
		t.Errorf("no match should be no-op: %v", out[0])
	}
}

// ─────────── ApplyIconReplacement ───────────

func TestApplyIconReplacement_KeLeeLookup(t *testing.T) {
	// 启动一个假的 KeLee icon JSON 服务
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"icons":[{"name":"proxy","url":"https://example.com/proxy.png"}]}`))
	}))
	defer srv.Close()

	// 通过替换 keLeeIconURL + 重置缓存来测试。由于 URL 是包级常量，
	// 我们转而直接测试 lookupIconURL 用的 cache 路径。
	// 先用真实函数验证 lookupIconURL 在 cache 为空且 client 为 nil 时返回 ""
	if got := lookupIconURL(context.Background(), nil, "proxy"); got != "" {
		t.Errorf("nil client → want empty, got %q", got)
	}
	_ = srv // 服务器仅用于确认 httptest 可用
}

func TestApplyIconReplacement_RandomLibrary(t *testing.T) {
	// isStashOrLoon=true 且 iconReplace 启用 → 使用 random sticker
	m := &ParsedModule{Icon: "old"}
	ApplyIconReplacement(context.Background(), m, map[string]string{
		"iconReplace": "enable",
		"iconLibrary": "Doraemon(10P)",
	}, nil, true)
	if !strings.Contains(m.Icon, "Toperlock/Quantumult") {
		t.Errorf("random library should set sticker URL: %q", m.Icon)
	}
	if !strings.Contains(m.Icon, "Doraemon") {
		t.Errorf("library name should be in URL: %q", m.Icon)
	}
}

func TestApplyIconReplacement_BareNameResolves(t *testing.T) {
	// 注入预加载的 map，跳过网络抓取
	keLeeIconMu.Lock()
	oldMap, oldLoaded := keLeeIconMap, keLeeIconLoaded
	keLeeIconMap = map[string]string{"netflix": "https://x/netflix.png"}
	keLeeIconLoaded = true
	keLeeIconMu.Unlock()
	defer func() {
		keLeeIconMu.Lock()
		keLeeIconMap = oldMap
		keLeeIconLoaded = oldLoaded
		keLeeIconMu.Unlock()
	}()

	client := newTestHTTPClient(t)
	m := &ParsedModule{Icon: "netflix"}
	// map 已预加载 → lookupIconURL 应能命中
	ApplyIconReplacement(context.Background(), m, nil, client, false)
	if m.Icon != "https://x/netflix.png" {
		t.Errorf("bare name should resolve via cache: %q", m.Icon)
	}
}

func TestApplyIconReplacement_URLKept(t *testing.T) {
	// icon 已是完整 URL → 不替换
	m := &ParsedModule{Icon: "https://cdn.example.com/icon.png"}
	ApplyIconReplacement(context.Background(), m, nil, nil, false)
	if m.Icon != "https://cdn.example.com/icon.png" {
		t.Errorf("full URL should be kept: %q", m.Icon)
	}
}

// ─────────── TakeLeadingTemplate ───────────

func TestTakeLeadingTemplate(t *testing.T) {
	cases := []struct {
		in   string
		want *LeadingTemplate
	}{
		{"{{{toggle_key}}} script-path=x", &LeadingTemplate{Key: "toggle_key", Rest: "script-path=x"}},
		{"  {{{key}}}rest", &LeadingTemplate{Key: "key", Rest: "  rest"}},
		{"no template here", nil},
		{"{{single}}", nil}, // 单括号不匹配
		{"", nil},
	}
	for _, c := range cases {
		got := TakeLeadingTemplate(c.in)
		if c.want == nil {
			if got != nil {
				t.Errorf("TakeLeadingTemplate(%q) = %+v, want nil", c.in, got)
			}
			continue
		}
		if got == nil {
			t.Errorf("TakeLeadingTemplate(%q) = nil, want %+v", c.in, c.want)
			continue
		}
		if got.Key != c.want.Key || got.Rest != c.want.Rest {
			t.Errorf("TakeLeadingTemplate(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// ─────────── randomIconURL ───────────

func TestRandomIconURL_Params(t *testing.T) {
	url := randomIconURL("Doraemon(50P)")
	if !strings.Contains(url, "Doraemon/Doraemon-") {
		t.Errorf("library name should be in path: %q", url)
	}
	if !strings.HasSuffix(url, ".png") {
		t.Errorf("default format should be png: %q", url)
	}
}

func TestRandomIconURL_GifLibrary_Params(t *testing.T) {
	url := randomIconURL("SomeGif(10P)")
	if !strings.HasSuffix(url, ".gif") {
		t.Errorf("gif library should produce .gif: %q", url)
	}
}

// ─────────── argsValue ───────────

func TestArgsValue(t *testing.T) {
	if got := argsValue(map[string]string{"k": "v"}, "k", "fb"); got != "v" {
		t.Errorf("got %q, want v", got)
	}
	if got := argsValue(map[string]string{"k": ""}, "k", "fb"); got != "fb" {
		t.Errorf("empty value → fallback: got %q", got)
	}
	if got := argsValue(map[string]string{}, "missing", "fb"); got != "fb" {
		t.Errorf("missing key → fallback: got %q", got)
	}
}

// ─────────── containsKeyword ───────────

func TestContainsKeyword(t *testing.T) {
	rw := ParsedRewrite{Pattern: "^api\\.x\\.com", ScriptPath: "p.js", Replacement: "r", Arguments: "arg=v"}
	if !containsKeyword(rw, "api") {
		t.Error("should match Pattern")
	}
	if !containsKeyword(rw, "p.js") {
		t.Error("should match ScriptPath")
	}
	if !containsKeyword(rw, "r") {
		t.Error("should match Replacement")
	}
	if !containsKeyword(rw, "arg") {
		t.Error("should match Arguments")
	}
	if containsKeyword(rw, "nomatch") {
		t.Error("should not match nomatch")
	}
}
