package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Output struct {
	Body        []byte
	ContentType string
}

func Capabilities() map[string]any {
	return map[string]any{
		"inputs":    []string{"clash-yaml", "uri", "base64-uri"},
		"targets":   []string{"clash", "shadowrocket", "uri", "loon", "egern"},
		"protocols": map[string][]string{"clash": {"preserve-clash-fields"}, "shadowrocket": {"ss", "trojan", "vmess", "vless", "http", "socks5", "hysteria2"}, "uri": {"ss", "trojan", "vmess", "vless", "http", "socks5", "hysteria2"}, "loon": {"ss", "trojan", "vmess", "vless", "http", "socks5", "hysteria2"}, "egern": {"ss", "trojan", "vmess", "vless", "http", "socks5", "hysteria2", "tuic"}},
		"strict":    true, "notes": []string{"未知或无法表达的字段会使整个导出失败。", "Clash 输出仅包含 proxies，不是完整客户端配置。", "Shadowrocket 使用标准 URI/Base64，仍需真实客户端导入验收。", "首版 Loon 不输出 gRPC/TUIC；Egern 没有已确认的 client-fingerprint 映射。"},
	}
}
func Export(nodes []Node, target string) (Output, error) {
	if len(nodes) == 0 || len(nodes) > MaxNodes {
		return Output{}, fmt.Errorf("node count must be 1–%d", MaxNodes)
	}
	if target == "" {
		target = "clash"
	}
	// Normalize copies, so callers can export the same immutable nodes concurrently.
	copies := make([]Node, len(nodes))
	for i, node := range nodes {
		copies[i] = Node{}
		for k, v := range node {
			copies[i][k] = v
		}
	}
	nodes = copies
	seen := map[string]bool{}
	for _, n := range nodes {
		if err := validate(n); err != nil {
			return Output{}, err
		}
		name := text(n["name"])
		if seen[name] {
			return Output{}, fmt.Errorf("duplicate node name %q; rename or deduplicate before export", name)
		}
		seen[name] = true
	}
	if target == "clash" {
		b, err := yaml.Marshal(map[string]any{"proxies": nodes})
		return Output{b, "application/yaml; charset=utf-8"}, err
	}
	lines := []string{}
	egern := []map[string]any{}
	for _, n := range nodes {
		var err error
		var line string
		var proxy map[string]any
		switch target {
		case "uri", "shadowrocket":
			line, err = toURI(n)
		case "loon":
			line, err = toLoon(n)
		case "egern":
			proxy, err = toEgern(n)
		default:
			return Output{}, fmt.Errorf("unknown target %q", target)
		}
		if err != nil {
			return Output{}, fmt.Errorf("node %q (%s): %w", text(n["name"]), text(n["type"]), err)
		}
		if target == "egern" {
			egern = append(egern, proxy)
		} else {
			lines = append(lines, line)
		}
	}
	if target == "egern" {
		b, err := yaml.Marshal(map[string]any{"proxies": egern})
		return Output{b, "application/yaml; charset=utf-8"}, err
	}
	body := strings.Join(lines, "\n") + "\n"
	if target == "loon" {
		body = "[Proxy]\n" + body
	}
	if target == "shadowrocket" {
		body = base64.StdEncoding.EncodeToString([]byte(body))
	}
	return Output{[]byte(body), "text/plain; charset=utf-8"}, nil
}
func only(n map[string]any, allowed string) error {
	set := map[string]bool{}
	for _, k := range strings.Fields(allowed) {
		set[k] = true
	}
	keys := []string{}
	for k := range n {
		if !set[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		return fmt.Errorf("unsupported field %s", strings.Join(keys, ", "))
	}
	return nil
}
func fieldsFor(n Node) string {
	base := "name type server port "
	switch text(n["type"]) {
	case "ss":
		return base + "cipher password udp tfo"
	case "trojan":
		return base + "password tls servername sni skip-cert-verify udp tfo network ws-opts reality-opts alpn client-fingerprint"
	case "vmess":
		return base + "uuid cipher alterId tls servername sni skip-cert-verify udp tfo network ws-opts grpc-opts reality-opts alpn client-fingerprint"
	case "vless":
		return base + "uuid flow tls servername sni skip-cert-verify udp tfo network ws-opts grpc-opts reality-opts alpn client-fingerprint"
	case "http":
		return base + "username password tls servername sni skip-cert-verify tfo"
	case "socks5":
		return base + "username password tls servername sni skip-cert-verify udp tfo"
	case "hysteria2":
		return base + "password tls servername sni skip-cert-verify obfs obfs-password up down"
	case "tuic":
		return base + "uuid password tls servername sni skip-cert-verify alpn udp-relay-mode"
	}
	return base
}
func transport(n Node) string {
	v := text(n["network"])
	if v == "" {
		v = "tcp"
	}
	return v
}
func secure(n Node) bool {
	return flag(n, "tls") || text(n["type"]) == "trojan" || text(n["type"]) == "hysteria2" || text(n["type"]) == "tuic"
}
func servername(n Node) (string, error) {
	a, b := text(n["servername"]), text(n["sni"])
	if a != "" && b != "" && a != b {
		return "", fmt.Errorf("conflicting sni and servername")
	}
	if a != "" {
		return a, nil
	}
	return b, nil
}
func alpn(n Node) ([]string, error) {
	if n["alpn"] == nil {
		return nil, nil
	}
	var out []string
	switch a := n["alpn"].(type) {
	case []any:
		for _, v := range a {
			s, ok := v.(string)
			if !ok || s == "" || strings.ContainsAny(s, "\r\n,") {
				return nil, fmt.Errorf("invalid alpn")
			}
			out = append(out, s)
		}
	case []string:
		out = a
	default:
		return nil, fmt.Errorf("alpn must be an array")
	}
	return out, nil
}
func ws(n Node) (string, map[string]any, error) {
	if n["ws-opts"] == nil {
		return "/", map[string]any{}, nil
	}
	m := object(n["ws-opts"])
	if m == nil {
		return "", nil, fmt.Errorf("ws-opts must be an object")
	}
	if err := only(m, "path headers"); err != nil {
		return "", nil, err
	}
	if v, ok := m["path"]; ok {
		if _, ok := v.(string); !ok {
			return "", nil, fmt.Errorf("WS path must be string")
		}
	}
	path := text(m["path"])
	if path == "" {
		path = "/"
	}
	headers := map[string]any{}
	if m["headers"] != nil {
		headers = object(m["headers"])
		if headers == nil {
			return "", nil, fmt.Errorf("WS headers must be an object")
		}
		for k, v := range headers {
			if _, ok := v.(string); !ok || strings.ContainsAny(k+text(v), "\r\n") {
				return "", nil, fmt.Errorf("invalid WS header")
			}
		}
	}
	return path, headers, nil
}
func hostOnly(h map[string]any) (string, error) {
	host := ""
	for k, v := range h {
		if !strings.EqualFold(k, "Host") {
			return "", fmt.Errorf("only WS Host header is supported by target")
		}
		if host != "" && host != text(v) {
			return "", fmt.Errorf("conflicting WS Host headers")
		}
		host = text(v)
	}
	return host, nil
}
func reality(n Node) (map[string]any, error) {
	if n["reality-opts"] == nil {
		return nil, nil
	}
	m := object(n["reality-opts"])
	if m == nil {
		return nil, fmt.Errorf("reality-opts must be an object")
	}
	if err := only(m, "public-key short-id"); err != nil {
		return nil, err
	}
	for _, k := range []string{"public-key", "short-id"} {
		if v, ok := m[k]; ok {
			if _, ok := v.(string); !ok {
				return nil, fmt.Errorf("Reality %s must be string", k)
			}
		}
	}
	if text(m["public-key"]) == "" {
		return nil, fmt.Errorf("Reality requires public-key")
	}
	if !secure(n) || transport(n) != "tcp" && transport(n) != "grpc" {
		return nil, fmt.Errorf("Reality requires TCP/gRPC with TLS")
	}
	if flag(n, "skip-cert-verify") {
		return nil, fmt.Errorf("skip-cert-verify is not meaningful with Reality")
	}
	return m, nil
}
func validateTransport(n Node) error {
	if err := only(n, fieldsFor(n)); err != nil {
		return err
	}
	switch text(n["type"]) {
	case "ss", "trojan", "vmess", "vless", "http", "socks5", "hysteria2", "tuic":
	default:
		return fmt.Errorf("unsupported protocol")
	}
	if _, err := servername(n); err != nil {
		return err
	}
	if _, err := alpn(n); err != nil {
		return err
	}
	network := transport(n)
	if network != "tcp" && network != "ws" && network != "grpc" {
		return fmt.Errorf("unsupported transport %s", network)
	}
	if n["ws-opts"] != nil && network != "ws" {
		return fmt.Errorf("ws-opts require network=ws")
	}
	if n["grpc-opts"] != nil && network != "grpc" {
		return fmt.Errorf("grpc-opts require network=grpc")
	}
	if n["ws-opts"] != nil {
		if _, _, err := ws(n); err != nil {
			return err
		}
	}
	if n["grpc-opts"] != nil {
		opts := object(n["grpc-opts"])
		if opts == nil {
			return fmt.Errorf("grpc-opts must be an object")
		}
		if err := only(opts, "grpc-service-name"); err != nil {
			return err
		}
		if v, ok := opts["grpc-service-name"]; ok {
			if _, ok := v.(string); !ok {
				return fmt.Errorf("grpc-service-name must be string")
			}
		}
	}
	if text(n["type"]) == "hysteria2" && text(n["obfs"]) != "" && text(n["obfs-password"]) == "" {
		return fmt.Errorf("HY2 obfuscation requires obfs-password")
	}
	if _, err := reality(n); err != nil {
		return err
	}
	if flow := text(n["flow"]); flow != "" && (flow != "xtls-rprx-vision" || network != "tcp" || !secure(n)) {
		return fmt.Errorf("flow requires xtls-rprx-vision over TCP+TLS/Reality")
	}
	if !secure(n) {
		for _, k := range []string{"sni", "servername", "skip-cert-verify", "alpn", "client-fingerprint"} {
			if _, ok := n[k]; ok {
				return fmt.Errorf("%s requires TLS", k)
			}
		}
	}
	if text(n["type"]) == "trojan" || text(n["type"]) == "hysteria2" || text(n["type"]) == "tuic" {
		if v, ok := n["tls"]; ok && v == false {
			return fmt.Errorf("protocol requires TLS")
		}
	}
	if n["alterId"] != nil {
		v, err := integer(n["alterId"])
		if err != nil || v < 0 {
			return fmt.Errorf("invalid alterId")
		}
	}
	return nil
}
func toEgern(n Node) (map[string]any, error) {
	if err := validateTransport(n); err != nil {
		return nil, err
	}
	if n["client-fingerprint"] != nil {
		return nil, fmt.Errorf("client-fingerprint has no verified Egern mapping")
	}
	typ := text(n["type"])
	protocol := typ
	p := map[string]any{"name": n["name"], "server": n["server"], "port": n["port"]}
	for a, b := range map[string]string{"udp": "udp_relay", "tfo": "tfo"} {
		if v, ok := n[a]; ok {
			p[b] = v
		}
	}
	sn, _ := servername(n)
	tls := map[string]any{}
	if sn != "" {
		tls["sni"] = sn
	}
	if v, ok := n["skip-cert-verify"]; ok {
		tls["skip_tls_verify"] = v
	}
	if r, err := reality(n); err != nil {
		return nil, err
	} else if r != nil {
		r2 := map[string]any{"public_key": r["public-key"]}
		if r["short-id"] != nil {
			r2["short_id"] = r["short-id"]
		}
		tls["reality"] = r2
	}
	ap, _ := alpn(n)
	switch typ {
	case "ss":
		protocol = "shadowsocks"
		cipher := text(n["cipher"])
		if cipher != "aes-128-gcm" && cipher != "aes-256-gcm" {
			return nil, fmt.Errorf("SS cipher lacks verified Egern mapping")
		}
		p["method"], p["password"] = cipher, n["password"]
	case "vmess", "vless":
		if len(ap) > 0 {
			return nil, fmt.Errorf("alpn has no verified Egern VMess/VLESS mapping")
		}
		p["user_id"] = n["uuid"]
		if typ == "vmess" {
			if number(n, "alterId") != 0 {
				return nil, fmt.Errorf("nonzero alterId cannot be preserved in Egern")
			}
			cipher := text(n["cipher"])
			if cipher == "" {
				cipher = "auto"
			}
			switch cipher {
			case "auto", "aes-128-gcm", "chacha20-poly1305", "none", "zero":
			default:
				return nil, fmt.Errorf("unsupported Egern VMess cipher")
			}
			p["security"], p["legacy"] = cipher, false
		} else if n["flow"] != nil {
			p["flow"] = n["flow"]
		}
		switch transport(n) {
		case "tcp":
			if secure(n) {
				p["transport"] = map[string]any{"tls": tls}
			}
		case "ws":
			path, headers, err := ws(n)
			if err != nil {
				return nil, err
			}
			w := map[string]any{"path": path, "headers": headers}
			key := "ws"
			if secure(n) {
				key = "wss"
				for k, v := range tls {
					w[k] = v
				}
			}
			p["transport"] = map[string]any{key: w}
		case "grpc":
			if !secure(n) {
				return nil, fmt.Errorf("Egern gRPC requires TLS")
			}
			opts := object(n["grpc-opts"])
			if opts == nil {
				return nil, fmt.Errorf("grpc-opts required")
			}
			if v, ok := opts["grpc-service-name"]; ok {
				if _, ok := v.(string); !ok {
					return nil, fmt.Errorf("grpc-service-name must be string")
				}
			}
			if err := only(opts, "grpc-service-name"); err != nil {
				return nil, err
			}
			g := map[string]any{"service_name": text(opts["grpc-service-name"])}
			for k, v := range tls {
				g[k] = v
			}
			p["transport"] = map[string]any{"grpc": g}
		}
	case "trojan":
		p["password"] = n["password"]
		for k, v := range tls {
			p[k] = v
		}
		if len(ap) > 0 {
			return nil, fmt.Errorf("alpn has no verified Egern Trojan mapping")
		}
		if transport(n) == "grpc" {
			return nil, fmt.Errorf("Egern Trojan gRPC is unsupported")
		}
		if transport(n) == "ws" {
			path, headers, err := ws(n)
			if err != nil {
				return nil, err
			}
			host, err := hostOnly(headers)
			if err != nil {
				return nil, err
			}
			w := map[string]any{"path": path}
			if host != "" {
				w["host"] = host
			}
			p["websocket"] = w
		}
	case "http", "socks5":
		if secure(n) {
			if typ == "http" {
				protocol = "https"
			} else {
				protocol = "socks5_tls"
			}
			for k, v := range tls {
				p[k] = v
			}
		}
		for _, k := range []string{"username", "password"} {
			if n[k] != nil {
				p[k] = n[k]
			}
		}
	case "hysteria2":
		p["auth"] = n["password"]
		for k, v := range tls {
			p[k] = v
		}
		if n["down"] != nil {
			return nil, fmt.Errorf("down has no Egern bandwidth mapping")
		}
		if n["up"] != nil {
			v, err := integer(n["up"])
			if err != nil || v <= 0 {
				return nil, fmt.Errorf("up must be integer Mbps")
			}
			p["bandwidth"] = v
		}
		if n["obfs"] != nil {
			if text(n["obfs"]) != "salamander" {
				return nil, fmt.Errorf("unsupported HY2 obfs")
			}
			p["obfs"] = n["obfs"]
		}
		if n["obfs-password"] != nil {
			if n["obfs"] == nil {
				return nil, fmt.Errorf("obfs-password requires obfs")
			}
			p["obfs_password"] = n["obfs-password"]
		}
	case "tuic":
		p["uuid"], p["password"] = n["uuid"], n["password"]
		for k, v := range tls {
			p[k] = v
		}
		if len(ap) > 0 {
			p["alpn"] = ap
		}
		if n["udp-relay-mode"] != nil {
			v := text(n["udp-relay-mode"])
			if v != "native" && v != "quic" {
				return nil, fmt.Errorf("invalid udp-relay-mode")
			}
			p["udp_relay_mode"] = v
		}
	}
	return map[string]any{protocol: p}, nil
}
func quoted(s string) (string, error) {
	if strings.ContainsAny(s, "\r\n\x00\\\"") {
		return "", fmt.Errorf("target text cannot safely represent control, quote or backslash characters")
	}
	if strings.ContainsAny(s, ", =[]#;") || s == "" {
		return `"` + s + `"`, nil
	}
	return s, nil
}
func toLoon(n Node) (string, error) {
	if err := validateTransport(n); err != nil {
		return "", err
	}
	if text(n["type"]) == "tuic" {
		return "", fmt.Errorf("TUIC is not implemented for Loon")
	}
	if transport(n) == "grpc" {
		return "", fmt.Errorf("gRPC is not supported for Loon")
	}
	name := text(n["name"])
	if strings.ContainsAny(name, "=,[]#;") {
		return "", fmt.Errorf("node name contains Loon delimiters")
	}
	host, err := quoted(text(n["server"]))
	if err != nil {
		return "", err
	}
	parts := []string{"", host, strconv.Itoa(number(n, "port"))}
	add := func(k string, v any) error {
		s, err := quoted(text(v))
		if err == nil {
			parts = append(parts, k+"="+s)
		}
		return err
	}
	password := func(k string) error {
		value := text(n[k])
		if _, err := quoted(value); err != nil {
			return err
		}
		parts = append(parts, `"`+value+`"`)
		return nil
	}

	switch text(n["type"]) {
	case "ss":
		parts[0] = "Shadowsocks"
		cipher := text(n["cipher"])
		switch cipher {
		case "aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305":
		default:
			return "", fmt.Errorf("unsupported Loon Shadowsocks cipher")
		}
		parts = append(parts, cipher)
		if err := password("password"); err != nil {
			return "", err
		}
	case "trojan":
		parts[0] = "Trojan"
		if err := password("password"); err != nil {
			return "", err
		}
	case "vmess":
		parts[0] = "VMess"
		cipher := text(n["cipher"])
		if cipher == "chacha20-poly1305" {
			cipher = "chacha20-ietf-poly1305"
		}
		if cipher == "" {
			cipher = "auto"
		}
		switch cipher {
		case "none", "auto", "aes-128-cfb", "aes-128-gcm", "chacha20-ietf-poly1305":
		default:
			return "", fmt.Errorf("unsupported Loon VMess cipher")
		}
		parts = append(parts, cipher)
		if err := password("uuid"); err != nil {
			return "", err
		}
		parts = append(parts, "alterId="+strconv.Itoa(number(n, "alterId")))
	case "vless":
		parts[0] = "VLESS"
		if err := password("uuid"); err != nil {
			return "", err
		}
	case "http", "socks5":
		parts[0] = text(n["type"])
		if parts[0] == "http" && secure(n) {
			parts[0] = "https"
		}
		if n["username"] != nil || n["password"] != nil {
			if err := password("username"); err != nil {
				return "", err
			}
			if err := password("password"); err != nil {
				return "", err
			}
		}
	case "hysteria2":
		parts[0] = "Hysteria2"
		if err := password("password"); err != nil {
			return "", err
		}
		if n["up"] != nil {
			return "", fmt.Errorf("up has no verified Loon mapping")
		}
		if n["down"] != nil {
			v, err := integer(n["down"])
			if err != nil || v <= 0 {
				return "", fmt.Errorf("down must be integer Mbps")
			}
			if err := add("download-bandwidth", v); err != nil {
				return "", err
			}
		}
		if n["obfs"] != nil && text(n["obfs"]) != "salamander" {
			return "", fmt.Errorf("unsupported HY2 obfs")
		}
		if n["obfs-password"] != nil {
			if n["obfs"] == nil {
				return "", fmt.Errorf("obfs-password requires obfs")
			}
			if err := add("salamander-password", n["obfs-password"]); err != nil {
				return "", err
			}
		}
	}
	typ := text(n["type"])
	if typ == "vmess" || typ == "vless" || typ == "trojan" {
		parts = append(parts, "transport="+transport(n))
		if transport(n) == "ws" {
			path, headers, err := ws(n)
			if err != nil {
				return "", err
			}
			host, err := hostOnly(headers)
			if err != nil {
				return "", err
			}
			if err := add("path", path); err != nil {
				return "", err
			}
			if host != "" {
				if err := add("host", host); err != nil {
					return "", err
				}
			}
		}
	}
	if typ == "vmess" || typ == "vless" || typ == "socks5" {
		if _, ok := n["tls"]; ok {
			parts = append(parts, "over-tls="+strconv.FormatBool(secure(n)))
		}
	}
	for _, pair := range [][2]string{{"udp", "udp"}, {"tfo", "fast-open"}, {"skip-cert-verify", "skip-cert-verify"}, {"flow", "flow"}} {
		src, dst := pair[0], pair[1]
		if v, ok := n[src]; ok {
			if err := add(dst, v); err != nil {
				return "", err
			}
		}
	}
	sn, _ := servername(n)
	if sn != "" {
		if err := add("sni", sn); err != nil {
			return "", err
		}
	}
	if fp := text(n["client-fingerprint"]); fp != "" {
		if fp != "chrome" {
			return "", fmt.Errorf("only chrome fingerprint mapping is verified for Loon")
		}
		parts = append(parts, "tls-profile=chrome")
	}
	if ap, err := alpn(n); err != nil {
		return "", err
	} else if len(ap) > 0 {
		if err := add("alpn", strings.Join(ap, ",")); err != nil {
			return "", err
		}
	}
	if r, err := reality(n); err != nil {
		return "", err
	} else if r != nil {
		for _, k := range []string{"public-key", "short-id"} {
			if r[k] != nil {
				if err := add(k, r[k]); err != nil {
					return "", err
				}
			}
		}
	}
	return name + " = " + strings.Join(parts, ","), nil
}
func toURI(n Node) (string, error) {
	if err := validateTransport(n); err != nil {
		return "", err
	}
	typ := text(n["type"])
	// Client-specific flags lack a portable URI representation; never silently discard them.
	for _, k := range []string{"udp", "tfo"} {
		if _, ok := n[k]; ok {
			return "", fmt.Errorf("%s has no portable URI mapping", k)
		}
	}
	if typ == "tuic" {
		return "", fmt.Errorf("TUIC URI is not implemented")
	}
	if typ != "hysteria2" && flag(n, "skip-cert-verify") {
		return "", fmt.Errorf("insecure TLS has no verified portable URI mapping")
	}
	host := net.JoinHostPort(text(n["server"]), strconv.Itoa(number(n, "port")))
	if typ == "ss" {
		auth := base64.RawURLEncoding.EncodeToString([]byte(text(n["cipher"]) + ":" + text(n["password"])))
		return "ss://" + auth + "@" + host + "#" + url.PathEscape(text(n["name"])), nil
	}
	sn, _ := servername(n)
	ap, _ := alpn(n)
	if (typ == "http" || typ == "socks5") && (sn != "" || n["skip-cert-verify"] != nil) {
		return "", fmt.Errorf("HTTP/SOCKS TLS options have no portable URI mapping")
	}
	if typ == "vmess" {
		if n["reality-opts"] != nil || n["client-fingerprint"] != nil {
			return "", fmt.Errorf("VMess Reality/fingerprint URI is not implemented")
		}
		m := map[string]any{"v": "2", "ps": n["name"], "add": n["server"], "port": strconv.Itoa(number(n, "port")), "id": n["uuid"], "aid": strconv.Itoa(number(n, "alterId")), "scy": text(n["cipher"]), "net": transport(n), "type": "none", "tls": ""}
		if m["scy"] == "" {
			m["scy"] = "auto"
		}
		if secure(n) {
			m["tls"] = "tls"
		}
		if sn != "" {
			m["sni"] = sn
		}
		if len(ap) > 0 {
			m["alpn"] = strings.Join(ap, ",")
		}
		if transport(n) == "ws" {
			path, h, err := ws(n)
			if err != nil {
				return "", err
			}
			host, err := hostOnly(h)
			if err != nil {
				return "", err
			}
			m["path"], m["host"] = path, host
		} else if transport(n) == "grpc" {
			opts := object(n["grpc-opts"])
			if opts == nil {
				return "", fmt.Errorf("grpc-opts required")
			}
			if err := only(opts, "grpc-service-name"); err != nil {
				return "", err
			}
			m["path"] = text(opts["grpc-service-name"])
		}
		b, _ := json.Marshal(m)
		return "vmess://" + base64.StdEncoding.EncodeToString(b), nil
	}
	u := url.URL{Scheme: typ, Host: host, Fragment: text(n["name"])}
	q := url.Values{}
	switch typ {
	case "trojan", "hysteria2":
		u.User = url.User(text(n["password"]))
	case "vless":
		u.User = url.User(text(n["uuid"]))
		q.Set("encryption", "none")
	case "http", "socks5":
		if secure(n) {
			if typ == "socks5" {
				return "", fmt.Errorf("SOCKS5 TLS URI is not implemented")
			}
			u.Scheme = "https"
		}
		if n["username"] != nil || n["password"] != nil {
			u.User = url.UserPassword(text(n["username"]), text(n["password"]))
		}
	}
	if typ == "trojan" || typ == "vless" {
		if secure(n) {
			q.Set("security", "tls")
		}
		if n["flow"] != nil {
			q.Set("flow", text(n["flow"]))
		}
		if n["client-fingerprint"] != nil {
			q.Set("fp", text(n["client-fingerprint"]))
		}
		if transport(n) != "tcp" {
			q.Set("type", transport(n))
		}
		if transport(n) == "ws" {
			path, h, err := ws(n)
			if err != nil {
				return "", err
			}
			host, err := hostOnly(h)
			if err != nil {
				return "", err
			}
			q.Set("path", path)
			if host != "" {
				q.Set("host", host)
			}
		} else if transport(n) == "grpc" {
			if typ == "trojan" {
				return "", fmt.Errorf("Trojan gRPC URI is not implemented")
			}
			opts := object(n["grpc-opts"])
			if opts == nil {
				return "", fmt.Errorf("grpc-opts required")
			}
			if err := only(opts, "grpc-service-name"); err != nil {
				return "", err
			}
			q.Set("serviceName", text(opts["grpc-service-name"]))
		}
		if r, err := reality(n); err != nil {
			return "", err
		} else if r != nil {
			q.Set("security", "reality")
			q.Set("pbk", text(r["public-key"]))
			if r["short-id"] != nil {
				q.Set("sid", text(r["short-id"]))
			}
		}
	}
	if sn != "" {
		q.Set("sni", sn)
	}
	if len(ap) > 0 {
		q.Set("alpn", strings.Join(ap, ","))
	}
	if typ == "hysteria2" {
		if n["up"] != nil || n["down"] != nil {
			return "", fmt.Errorf("bandwidth has no portable HY2 URI mapping")
		}
		if v, ok := n["skip-cert-verify"]; ok && v == true {
			q.Set("insecure", "1")
		}
		if n["obfs"] != nil {
			if text(n["obfs"]) != "salamander" {
				return "", fmt.Errorf("unsupported HY2 obfs")
			}
			q.Set("obfs", text(n["obfs"]))
		}
		if n["obfs-password"] != nil {
			if n["obfs"] == nil {
				return "", fmt.Errorf("obfs-password requires obfs")
			}
			q.Set("obfs-password", text(n["obfs-password"]))
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
