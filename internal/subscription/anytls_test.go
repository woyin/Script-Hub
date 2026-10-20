package subscription

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAnyTLSNativeEgernAndURIRoundTrip(t *testing.T) {
	for _, query := range []string{"", "?insecure=0&udp=1", "?insecure=1&udp=0"} {
		nodes, err := Parse([]byte("anytls://password@proxy.example:443" + query + "#test"))
		if err != nil {
			t.Fatal(err)
		}
		out, err := Export(nodes, "egern")
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Proxies []map[string]map[string]any `yaml:"proxies"`
		}
		if err := yaml.Unmarshal(out.Body, &doc); err != nil {
			t.Fatal(err)
		}
		n := doc.Proxies[0]["anytls"]
		if n["password"] != "password" || n["skip_tls_verify"] != flag(nodes[0], "skip-cert-verify") {
			t.Fatal("AnyTLS password or certificate validation lost")
		}
		out, err = Export(nodes, "uri")
		if err != nil {
			t.Fatal(err)
		}
		again, err := Parse(out.Body)
		if err != nil || len(again) != 1 || text(again[0]["password"]) != "password" || flag(again[0], "skip-cert-verify") != flag(nodes[0], "skip-cert-verify") || flag(again[0], "udp") != flag(nodes[0], "udp") {
			t.Fatal("AnyTLS URI round trip failed", err)
		}
		loon, err := Export(nodes, "loon")
		if err != nil || !strings.Contains(string(loon.Body), "AnyTLS,") {
			t.Fatal("Loon rejected supported AnyTLS", err)
		}
	}
}

func TestMixedSubscriptionPacketHints(t *testing.T) {
	input := "hysteria2://password@proxy.example:443?insecure=0#hy2\n" +
		"vless://test-id@proxy.example:443?security=reality&type=tcp&packetEncoding=xudp&fp=chrome&flow=xtls-rprx-vision&pbk=testkey&sid=ab#vless\n" +
		"trojan://password@proxy.example:443?type=tcp&alpn=h2,http%2F1.1#trojan\n" +
		"anytls://password@proxy.example:443?security=tls&type=tcp&packetEncoding=xudp&udp=1&packet_encoding=xudp&xudp=true#anytls"
	nodes, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 4 || nodes[3]["packet-encoding"] != "xudp" || !flag(nodes[3], "udp") {
		t.Fatal("mixed nodes or hints lost")
	}
	out, err := Export(nodes, "clash")
	if err != nil || !strings.Contains(string(out.Body), "packet-encoding: xudp") {
		t.Fatal("Clash lost encoding", err)
	}
	out, err = Export(nodes, "uri")
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(out.Body)
	if err != nil || len(again) != 4 || again[3]["packet-encoding"] != "xudp" {
		t.Fatal("mixed URI round trip", err)
	}
	loon, err := Export(nodes, "loon")
	if err != nil {
		t.Fatal("Loon rejected supported mixed subscription", err)
	}
	if strings.Count(string(loon.Body), " = ") != 4 || !strings.Contains(string(loon.Body), "AnyTLS,") || !strings.Contains(string(loon.Body), "flow=xtls-rprx-vision") || !strings.Contains(string(loon.Body), "public-key=testkey") || !strings.Contains(string(loon.Body), "udp=true") {
		t.Fatal("Loon lost connection settings")
	}
	out, err = Export(nodes, "egern")
	if err != nil {
		t.Fatal("Egern native conversion rejected supported mixed subscription", err)
	}
	var doc struct {
		Proxies []map[string]map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(out.Body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Proxies) != 4 {
		t.Fatal("Egern lost nodes")
	}
	v := doc.Proxies[1]["vless"]
	tls := object(object(v["transport"])["tls"])
	if v["udp_relay"] != true {
		t.Fatal("VLESS URI default UDP relay lost")
	}
	if v["flow"] != "xtls-rprx-vision" || v["user_id"] != "test-id" || object(tls["reality"])["public_key"] != "testkey" {
		t.Fatal("Egern lost VLESS connection settings")
	}
	if doc.Proxies[3]["anytls"]["udp_relay"] != true {
		t.Fatal("Egern lost explicit UDP enablement")
	}
	for _, node := range doc.Proxies {
		for _, settings := range node {
			for _, key := range []string{"packetEncoding", "packet-encoding", "fp", "client-fingerprint", "alpn"} {
				if _, ok := settings[key]; ok {
					t.Fatal("source-only hint leaked into native Egern output", key)
				}
			}
		}
	}
	for _, query := range []string{"packetEncoding=xudp&packet_encoding=none", "packetEncoding=xudp&xudp=false", "xudp=invalid", "udp=invalid"} {
		if _, err := Parse([]byte("anytls://password@proxy.example:443?" + query)); err == nil {
			t.Fatal("accepted invalid/conflicting hint", query)
		}
	}
}
