package subscription

import (
	"encoding/base64"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

const fixture = `proxies:
  - name: 日本 SS
    type: ss
    server: 2001:db8::1
    port: 443
    cipher: aes-128-gcm
    password: 'a:@/测试'
  - name: Trojan
    type: trojan
    server: proxy.example
    port: 443
    password: secret
    sni: tls.example
  - name: VLESS
    type: vless
    server: proxy.example
    port: 443
    uuid: 11111111-1111-1111-1111-111111111111
    tls: true
    servername: tls.example
    network: ws
    ws-opts:
      path: /socket?ed=1
      headers:
        Host: cdn.example
`

func TestClientOutputsAndURIRoundTrip(t *testing.T) {
	nodes, err := Parse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"clash", "egern", "loon", "uri", "shadowrocket"} {
		t.Run(target, func(t *testing.T) {
			out, err := Export(nodes, target)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Body) == 0 {
				t.Fatal("empty output")
			}
			switch target {
			case "clash", "egern":
				var doc struct {
					Proxies []map[string]any `yaml:"proxies"`
				}
				if err := yaml.Unmarshal(out.Body, &doc); err != nil {
					t.Fatal(err)
				}
				if len(doc.Proxies) != 3 {
					t.Fatal("lost nodes")
				}
				if target == "egern" {
					ss := object(doc.Proxies[0]["shadowsocks"])
					if ss["server"] != "2001:db8::1" || ss["method"] != "aes-128-gcm" {
						t.Fatalf("invalid Egern SS: %s", out.Body)
					}
					v := object(doc.Proxies[2]["vless"])
					tr := object(v["transport"])
					wss := object(tr["wss"])
					if wss["sni"] != "tls.example" || wss["path"] != "/socket?ed=1" {
						t.Fatalf("invalid Egern WSS: %s", out.Body)
					}
				}
			case "loon":
				if !strings.HasPrefix(string(out.Body), "[Proxy]\n") || !strings.Contains(string(out.Body), "transport=ws") || !strings.Contains(string(out.Body), "over-tls=true") {
					t.Fatalf("invalid Loon: %s", out.Body)
				}
			case "uri", "shadowrocket":
				again, err := Parse(out.Body)
				if err != nil {
					t.Fatal(err)
				}
				if len(again) != len(nodes) {
					t.Fatal("URI roundtrip lost nodes")
				}
				if again[0]["password"] != nodes[0]["password"] || again[0]["server"] != nodes[0]["server"] {
					t.Fatalf("URI escaping lost data: %#v", again[0])
				}
				if text(again[2]["servername"]) != "tls.example" || !flag(again[2], "tls") {
					t.Fatal("TLS lost")
				}
			}
		})
	}
}
func TestStrictCompatibility(t *testing.T) {
	base := func() Node {
		return Node{"name": "node", "type": "vless", "server": "example.com", "port": 443, "uuid": "id", "tls": true}
	}
	cases := []struct {
		name, target string
		node         Node
	}{
		{"unknown field", "egern", Node{"name": "n", "type": "ss", "server": "x", "port": 443, "cipher": "aes-128-gcm", "password": "p", "plugin": "obfs"}},
		{"unknown fingerprint", "egern", func() Node { n := base(); n["client-fingerprint"] = "chrome"; return n }()},
		{"nonzero alterId", "egern", Node{"name": "n", "type": "vmess", "server": "x", "port": 443, "uuid": "id", "alterId": 4}},
		{"grpc Loon", "loon", func() Node {
			n := base()
			n["network"] = "grpc"
			n["grpc-opts"] = map[string]any{"grpc-service-name": "s"}
			return n
		}()},
		{"plaintext grpc Egern", "egern", func() Node {
			n := base()
			n["tls"] = false
			n["network"] = "grpc"
			n["grpc-opts"] = map[string]any{"grpc-service-name": "s"}
			return n
		}()},
		{"WS custom header Loon", "loon", func() Node {
			n := base()
			n["network"] = "ws"
			n["ws-opts"] = map[string]any{"headers": map[string]any{"X-Token": "secret"}}
			return n
		}()},
		{"WS early data", "egern", func() Node {
			n := base()
			n["network"] = "ws"
			n["ws-opts"] = map[string]any{"max-early-data": 2048}
			return n
		}()},
		{"unsupported protocol", "loon", Node{"name": "n", "type": "tuic", "server": "x", "port": 443, "uuid": "id", "password": "p"}},
		{"SS URI flags", "shadowrocket", Node{"name": "n", "type": "ss", "server": "x", "port": 443, "cipher": "aes-128-gcm", "password": "p", "udp": true}},
		{"missing TLS reality", "egern", func() Node {
			n := base()
			n["tls"] = false
			n["reality-opts"] = map[string]any{"public-key": "key"}
			return n
		}()},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Export([]Node{tt.node}, tt.target); err == nil {
				t.Fatal("unrepresentable configuration was silently accepted")
			}
		})
	}
	// Unsupported options are preserved when exporting Clash itself.
	out, err := Export([]Node{cases[0].node}, "clash")
	if err != nil || !strings.Contains(string(out.Body), "plugin: obfs") {
		t.Fatalf("Clash field preservation: %s %v", out.Body, err)
	}
}
func TestParseVMessBase64AndProcessing(t *testing.T) {
	uri := "vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"JP node","add":"x","port":"443","id":"id","aid":"0","net":"ws","type":"none","host":"cdn.example","path":"/ws","tls":"tls"}`))
	nodes, err := Parse([]byte(base64.RawStdEncoding.EncodeToString([]byte(uri))))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"uri", "loon", "egern"} {
		if _, err := Export(nodes, target); err != nil {
			t.Fatalf("%s: %v", target, err)
		}
	}
	copyNode := Node{}
	for k, v := range nodes[0] {
		copyNode[k] = v
	}
	copyNode["name"] = "another"
	out, err := Process([]Node{nodes[0], copyNode}, Options{Deduplicate: true, Rename: []Rename{{"JP", "Japan"}}, Sort: true})
	if err != nil || len(out) != 1 || out[0]["name"] != "Japan node" {
		t.Fatalf("process: %#v %v", out, err)
	}
	if nodes[0]["name"] != "JP node" {
		t.Fatal("mutated original node")
	}
}
func TestMalformedAndUnsafeInputs(t *testing.T) {
	for _, input := range []string{"", "not-a-subscription", "proxies:\n - name: a\n   type: ss\n   port: -1", "proxies: []", "proxies: []\n---\nproxies: []", "ss://a@host:443?plugin=obfs", "vless://id@x:443?unknown=secret"} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Fatalf("accepted invalid input %q", input)
		}
	}
	nodes, _ := Parse([]byte(fixture))
	nodes[0]["cipher"] = "aes-128-gcm\nInjected = http,bad,80"
	if _, err := Export(nodes[:1], "loon"); err == nil {
		t.Fatal("Loon cipher injection accepted")
	}
}
func TestConcurrentExportDoesNotMutateNodes(t *testing.T) {
	nodes, _ := Parse([]byte(fixture))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Export(nodes, "egern"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestNativeProtocolSpecificMappings(t *testing.T) {
	check := func(n Node, protocol string) map[string]any {
		t.Helper()
		out, err := Export([]Node{n}, "egern")
		if err != nil {
			t.Fatal(err)
		}
		var d struct {
			Proxies []map[string]any `yaml:"proxies"`
		}
		if err := yaml.Unmarshal(out.Body, &d); err != nil {
			t.Fatal(err)
		}
		p := object(d.Proxies[0][protocol])
		if p == nil {
			t.Fatalf("missing %s: %s", protocol, out.Body)
		}
		return p
	}
	h2 := Node{"name": "HY2", "type": "hysteria2", "server": "x", "port": 443, "password": "pass", "sni": "tls.example", "up": 50, "obfs": "salamander", "obfs-password": "obfs"}
	p := check(h2, "hysteria2")
	if p["auth"] != "pass" || p["bandwidth"] != 50 || p["obfs_password"] != "obfs" {
		t.Fatalf("HY2 directions/credentials: %#v", p)
	}
	delete(h2, "up")
	h2["down"] = 60
	out, err := Export([]Node{h2}, "loon")
	if err != nil || !strings.Contains(string(out.Body), "download-bandwidth=60") {
		t.Fatalf("HY2 Loon download mapping: %s %v", out.Body, err)
	}
	if _, err := Export([]Node{h2}, "egern"); err == nil {
		t.Fatal("HY2 down silently interpreted as upload")
	}
	delete(h2, "down")
	h2["skip-cert-verify"] = true
	out, err = Export([]Node{h2}, "uri")
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(out.Body)
	if err != nil || !flag(again[0], "skip-cert-verify") || again[0]["obfs-password"] != "obfs" {
		t.Fatalf("HY2 URI round trip: %#v %v", again, err)
	}
	tuic := Node{"name": "TUIC", "type": "tuic", "server": "x", "port": 443, "uuid": "id", "password": "pass", "alpn": []any{"h3"}, "udp-relay-mode": "native"}
	p = check(tuic, "tuic")
	if p["uuid"] != "id" || p["udp_relay_mode"] != "native" {
		t.Fatalf("TUIC: %#v", p)
	}
	tuic["congestion-controller"] = "bbr"
	if _, err := Export([]Node{tuic}, "egern"); err == nil {
		t.Fatal("unmapped TUIC option dropped")
	}
	grpc := Node{"name": "grpc", "type": "vless", "server": "x", "port": 443, "uuid": "id", "tls": true, "servername": "tls.example", "network": "grpc", "grpc-opts": map[string]any{"grpc-service-name": "service"}}
	p = check(grpc, "vless")
	g := object(object(p["transport"])["grpc"])
	if g["service_name"] != "service" || g["sni"] != "tls.example" {
		t.Fatalf("gRPC TLS mapping: %#v", p)
	}
	grpc["network"] = "tcp"
	delete(grpc, "grpc-opts")
	grpc["reality-opts"] = map[string]any{"public-key": "public-key", "short-id": "abc123"}
	grpc["flow"] = "xtls-rprx-vision"
	p = check(grpc, "vless")
	tls := object(object(p["transport"])["tls"])
	if object(tls["reality"])["public_key"] != "public-key" || p["flow"] != "xtls-rprx-vision" {
		t.Fatal("Reality dropped")
	}
	grpc["client-fingerprint"] = "chrome"
	out, err = Export([]Node{grpc}, "loon")
	if err != nil || !strings.Contains(string(out.Body), "tls-profile=chrome") {
		t.Fatalf("Loon Reality: %s %v", out.Body, err)
	}
	trojan := Node{"name": "trojan", "type": "trojan", "server": "x", "port": 443, "password": "pass", "network": "ws", "ws-opts": map[string]any{"path": "/ws", "headers": map[string]any{"Host": "cdn.example"}}}
	p = check(trojan, "trojan")
	if object(p["websocket"])["host"] != "cdn.example" || p["transport"] != nil {
		t.Fatalf("Trojan incorrectly used VMess transport: %#v", p)
	}
	for _, typ := range []string{"http", "socks5"} {
		n := Node{"name": typ, "type": typ, "server": "x", "port": 443, "username": "u", "password": "p", "tls": true, "sni": "tls.example"}
		key := "https"
		if typ == "socks5" {
			key = "socks5_tls"
		}
		p = check(n, key)
		if p["username"] != "u" || p["sni"] != "tls.example" {
			t.Fatalf("%s TLS: %#v", typ, p)
		}
	}
}
func TestLoonDeterministicOutput(t *testing.T) {
	n := Node{"name": "stable", "type": "trojan", "server": "x", "port": 443, "password": "p", "udp": true, "tfo": false, "skip-cert-verify": false}
	first, err := Export([]Node{n}, "loon")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		out, err := Export([]Node{n}, "loon")
		if err != nil || string(out.Body) != string(first.Body) {
			t.Fatal("nondeterministic output")
		}
	}
}
