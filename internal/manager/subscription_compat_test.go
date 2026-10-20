package manager

import (
	"github.com/script-hub-org/script-hub/internal/subscription"
	"gopkg.in/yaml.v3"
	"net/http"
	"strings"
	"testing"
)

func TestSharedMixedURICompatibility(t *testing.T) {
	h := newTestHandler(t, nil)
	content := "hysteria2://password@proxy.example:443?insecure=0#hy2\n" +
		"vless://test-id@proxy.example:443?security=reality&type=tcp&packetEncoding=xudp&fp=chrome&flow=xtls-rprx-vision&pbk=testkey&sid=ab#vless\n" +
		"trojan://password@proxy.example:443?type=tcp&alpn=h2,http%2F1.1#trojan\n" +
		"anytls://password@proxy.example:443?security=tls&type=tcp&packetEncoding=xudp&udp=1&packet_encoding=xudp&xudp=true#anytls"
	id := create(t, h, "subscriptions", Spec{Name: "mixed", Source: "local", Content: content})
	share := issue(t, h, "subscriptions", id)
	for _, target := range []string{"egern", "loon", "shadowrocket"} {
		path := share + "?target=" + target
		for attempt := 0; attempt < 2; attempt++ {
			r := request(t, h, http.MethodGet, path, nil)
			if r.Code != http.StatusOK {
				t.Fatalf("share returned %d", r.Code)
			}
			if target == "loon" {
				if strings.Count(r.Body.String(), " = ") != 4 {
					t.Fatal("Loon share lost nodes")
				}
				continue
			}
			if target == "shadowrocket" {
				nodes, err := subscription.Parse(r.Body.Bytes())
				if err != nil || len(nodes) != 4 {
					t.Fatal("Shadowrocket share lost nodes", err)
				}
				continue
			}
			var doc struct {
				Proxies []map[string]map[string]any `yaml:"proxies"`
			}
			if err := yaml.Unmarshal(r.Body.Bytes(), &doc); err != nil || len(doc.Proxies) != 4 {
				t.Fatal("share lost nodes or returned invalid YAML", err)
			}
			if doc.Proxies[1]["vless"]["udp_relay"] != true || doc.Proxies[3]["anytls"]["udp_relay"] != true {
				t.Fatal("share lost UDP relay")
			}
		}
	}

}
