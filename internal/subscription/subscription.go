// Package subscription converts proxy nodes without running user supplied scripts.
package subscription

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

const MaxNodes = 10000
const MaxInputBytes = 8 << 20

// Node preserves Clash fields so unknown parameters cannot silently disappear.
type Node map[string]any

type Rename struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
}
type Options struct {
	Include     string   `json:"include,omitempty"`
	Exclude     string   `json:"exclude,omitempty"`
	Rename      []Rename `json:"rename,omitempty"`
	Deduplicate bool     `json:"deduplicate,omitempty"`
	Sort        bool     `json:"sort,omitempty"`
}

func (o Options) Validate() error {
	if len(o.Rename) > 32 {
		return errors.New("too many rename operations")
	}
	patterns := []string{o.Include, o.Exclude}
	for _, r := range o.Rename {
		patterns = append(patterns, r.Pattern)
		if len(r.Replacement) > 1024 {
			return errors.New("rename replacement too long")
		}
	}
	for _, p := range patterns {
		if len(p) > 1024 {
			return errors.New("pattern too long")
		}
		if _, err := regexp.Compile(p); err != nil {
			return errors.New("invalid regular expression")
		}
	}
	return nil
}
func Process(nodes []Node, o Options) ([]Node, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	var include, exclude *regexp.Regexp
	if o.Include != "" {
		include = regexp.MustCompile(o.Include)
	}
	if o.Exclude != "" {
		exclude = regexp.MustCompile(o.Exclude)
	}
	renames := make([]*regexp.Regexp, len(o.Rename))
	for i, r := range o.Rename {
		renames[i] = regexp.MustCompile(r.Pattern)
	}
	out := []Node{}
	seen := map[string]bool{}
	for _, node := range nodes {
		name := text(node["name"])
		if include != nil && !include.MatchString(name) || exclude != nil && exclude.MatchString(name) {
			continue
		}
		n := Node{}
		for k, v := range node {
			n[k] = v
		}
		for i, r := range o.Rename {
			name = renames[i].ReplaceAllString(name, r.Replacement)
		}
		n["name"] = name
		if o.Deduplicate {
			identity := Node{}
			for k, v := range n {
				if k != "name" {
					identity[k] = v
				}
			}
			b, _ := json.Marshal(identity)
			if seen[string(b)] {
				continue
			}
			seen[string(b)] = true
		}
		if err := validate(n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if o.Sort {
		sort.SliceStable(out, func(i, j int) bool { return text(out[i]["name"]) < text(out[j]["name"]) })
	}
	if len(out) == 0 {
		return nil, errors.New("no nodes remain after processing")
	}
	return out, nil
}
func text(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func integer(v any) (int, error) {
	switch x := v.(type) {
	case int:
		return x, nil
	case int64:
		return int(x), nil
	case uint64:
		if x > 65535 {
			return 0, errors.New("number out of range")
		}
		return int(x), nil
	case float64:
		if x != float64(int(x)) {
			return 0, errors.New("integer required")
		}
		return int(x), nil
	default:
		return strconv.Atoi(text(v))
	}
}
func number(n Node, key string) int { v, _ := integer(n[key]); return v }
func flag(n Node, key string) bool  { v, _ := n[key].(bool); return v }
func object(v any) map[string]any   { m, _ := v.(map[string]any); return m }
func validate(n Node) error {
	if text(n["name"]) == "" || len(text(n["name"])) > 512 || strings.IndexFunc(text(n["name"]), unicode.IsControl) >= 0 {
		return errors.New("node name must contain 1–512 bytes without control lines")
	}
	for _, k := range []string{"name", "type", "server"} {
		if _, ok := n[k].(string); !ok || text(n[k]) == "" {
			return fmt.Errorf("node requires string field %s", k)
		}
	}
	if strings.ContainsAny(text(n["server"]), "\r\n\t /\x00") {
		return errors.New("invalid node server")
	}
	p, err := integer(n["port"])
	if err != nil || p < 1 || p > 65535 {
		return errors.New("invalid node port")
	}
	n["port"] = p
	n["type"] = strings.ToLower(text(n["type"]))
	for _, k := range []string{"tls", "skip-cert-verify", "udp", "tfo"} {
		if v, ok := n[k]; ok {
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("%s must be boolean", k)
			}
		}
	}
	for _, k := range []string{"password", "uuid", "username", "cipher", "sni", "servername", "network", "flow", "client-fingerprint", "obfs", "obfs-password", "udp-relay-mode", "packet-encoding"} {
		if v, ok := n[k]; ok {
			if _, ok := v.(string); !ok {
				return fmt.Errorf("%s must be string", k)
			}
		}
	}
	switch text(n["type"]) {
	case "ss":
		if text(n["cipher"]) == "" || text(n["password"]) == "" {
			return errors.New("SS requires cipher and password")
		}
	case "trojan", "hysteria2", "anytls":
		if text(n["password"]) == "" {
			return errors.New("node requires password")
		}
	case "vmess", "vless":
		if text(n["uuid"]) == "" {
			return errors.New("node requires uuid")
		}
	case "tuic":
		if text(n["uuid"]) == "" || text(n["password"]) == "" {
			return errors.New("TUIC requires uuid and password")
		}
	}
	return nil
}
func decode64(s string) ([]byte, error) {
	s = strings.Join(strings.Fields(s), "")
	for _, e := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := e.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("invalid base64")
}
func Parse(data []byte) ([]Node, error) {
	if len(data) > MaxInputBytes {
		return nil, errors.New("subscription exceeds 8 MiB")
	}
	data = bytes.TrimSpace(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}))
	if len(data) == 0 {
		return nil, errors.New("subscription is empty")
	}
	if !bytes.Contains(data, []byte("://")) && !bytes.Contains(data, []byte("proxies:")) && !bytes.HasPrefix(data, []byte("{")) && !bytes.HasPrefix(data, []byte("[")) && !bytes.HasPrefix(data, []byte("-")) {
		if b, err := decode64(string(data)); err == nil && bytes.Contains(b, []byte("://")) {
			data = b
		}
	}
	var nodes []Node
	// YAML/JSON subscriptions are identified structurally; URLs may also occur in ws paths.
	var root any
	d := yaml.NewDecoder(bytes.NewReader(data))
	if err := d.Decode(&root); err == nil {
		var sequence []any
		switch v := root.(type) {
		case map[string]any:
			sequence, _ = v["proxies"].([]any)
		case []any:
			sequence = v
		}
		if sequence != nil {
			var extra any
			if err := d.Decode(&extra); err != io.EOF {
				return nil, errors.New("multiple YAML documents are not accepted")
			}
			if len(sequence) > MaxNodes {
				return nil, errors.New("too many nodes")
			}
			for _, item := range sequence {
				m, ok := item.(map[string]any)
				if !ok {
					return nil, errors.New("proxy entries must be mappings")
				}
				n := Node(m)
				if err := validate(n); err != nil {
					return nil, err
				}
				nodes = append(nodes, n)
			}
			if len(nodes) == 0 {
				return nil, errors.New("no nodes in subscription")
			}
			return nodes, nil
		}
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n, err := parseURI(line)
		if err != nil {
			return nil, fmt.Errorf("node %d: %w", len(nodes)+1, err)
		}
		if err := validate(n); err != nil {
			return nil, fmt.Errorf("node %d: %w", len(nodes)+1, err)
		}
		nodes = append(nodes, n)
		if len(nodes) > MaxNodes {
			return nil, errors.New("too many nodes")
		}
	}
	if len(nodes) == 0 {
		return nil, errors.New("no nodes in subscription")
	}
	return nodes, nil
}
func parseURI(raw string) (Node, error) {
	if strings.HasPrefix(raw, "vmess://") {
		return parseVMess(strings.TrimPrefix(raw, "vmess://"))
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("invalid node URI")
	}
	typ := strings.ToLower(u.Scheme)
	if typ == "hy2" {
		typ = "hysteria2"
	}
	if typ == "socks" {
		typ = "socks5"
	}
	if typ == "ss" && !strings.Contains(strings.SplitN(strings.TrimPrefix(raw, "ss://"), "#", 2)[0], "@") {
		parts := strings.SplitN(strings.TrimPrefix(raw, "ss://"), "#", 2)
		body, err := decode64(parts[0])
		if err != nil {
			return nil, err
		}
		raw = "ss://" + string(body)
		if len(parts) == 2 {
			raw += "#" + parts[1]
		}
		u, err = url.Parse(raw)
		if err != nil {
			return nil, errors.New("invalid legacy SS URI")
		}
	}
	switch typ {
	case "ss", "trojan", "vless", "http", "https", "socks5", "hysteria2", "anytls":
	default:
		return nil, errors.New("unsupported node URI scheme")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || u.Hostname() == "" {
		return nil, errors.New("node URI requires host and port")
	}
	n := Node{"name": u.Fragment, "type": typ, "server": u.Hostname(), "port": port}
	if u.Fragment == "" {
		n["name"] = typ + "-" + net.JoinHostPort(u.Hostname(), u.Port())
	}
	user, password := "", ""
	hasPass := false
	if u.User != nil {
		user = u.User.Username()
		password, hasPass = u.User.Password()
	}
	switch typ {
	case "ss":
		if !hasPass {
			b, err := decode64(user)
			if err != nil {
				return nil, err
			}
			parts := strings.SplitN(string(b), ":", 2)
			if len(parts) != 2 {
				return nil, errors.New("SS userinfo requires method and password")
			}
			user, password = parts[0], parts[1]
		}
		n["cipher"], n["password"] = user, password
	case "trojan", "hysteria2", "anytls":
		if hasPass {
			return nil, errors.New("password containing colon must be percent encoded")
		}
		n["password"] = user
		n["tls"] = true
	case "vless":
		n["uuid"] = user
	default:
		if user != "" {
			n["username"] = user
		}
		if hasPass {
			n["password"] = password
		}
		if typ == "https" {
			n["type"] = "http"
			n["tls"] = true
		}
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, errors.New("invalid URI parameters")
	}
	allowed := map[string]bool{}
	keys := ""
	switch typ {
	case "vless":
		keys = "sni security type path host serviceName flow pbk sid fp encryption alpn packetEncoding"
	case "trojan":
		keys = "sni security type path host pbk sid fp alpn"
	case "anytls":
		keys = "sni security type insecure udp packetEncoding packet_encoding xudp alpn fp"
	case "hysteria2":
		keys = "sni insecure obfs obfs-password"
	}
	for _, k := range strings.Fields(keys) {
		allowed[k] = true
	}

	for k, v := range q {
		if !allowed[k] || len(v) != 1 {
			return nil, fmt.Errorf("unsupported URI parameter %s", k)
		}
	}
	if typ == "ss" && len(q) != 0 {
		return nil, errors.New("SS URI plugins/parameters are not yet supported")
	}
	for src, dst := range map[string]string{"sni": "servername", "flow": "flow", "fp": "client-fingerprint", "obfs": "obfs", "obfs-password": "obfs-password"} {
		if q.Get(src) != "" {
			n[dst] = q.Get(src)
		}
	}
	encoding := ""
	for _, key := range []string{"packetEncoding", "packet_encoding"} {
		if value := q.Get(key); value != "" {
			if encoding != "" && encoding != value {
				return nil, errors.New("conflicting packet encoding parameters")
			}
			encoding = value
		}
	}
	if value := q.Get("xudp"); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return nil, errors.New("invalid xudp flag")
		}
		value = "none"
		if enabled {
			value = "xudp"
		}
		if encoding != "" && encoding != value {
			return nil, errors.New("conflicting packet encoding parameters")
		}
		encoding = value
	}
	if encoding != "" {
		switch encoding {
		case "none", "xudp", "packetaddr":
			n["packet-encoding"] = encoding
		default:
			return nil, errors.New("invalid packet encoding")
		}
	}
	if value := q.Get("udp"); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return nil, errors.New("invalid udp flag")
		}
		n["udp"] = enabled
	}
	if q.Get("encryption") != "" && q.Get("encryption") != "none" {
		return nil, errors.New("unsupported VLESS encryption")
	}
	if sec := q.Get("security"); sec != "" {
		switch sec {
		case "none":
			n["tls"] = false
		case "tls", "reality":
			n["tls"] = true
		default:
			return nil, errors.New("unsupported URI security")
		}
	}
	if q.Get("security") == "reality" {
		if q.Get("pbk") == "" {
			return nil, errors.New("Reality requires public key")
		}
		n["reality-opts"] = map[string]any{"public-key": q.Get("pbk"), "short-id": q.Get("sid")}
	} else if q.Get("pbk") != "" || q.Get("sid") != "" {
		return nil, errors.New("Reality fields require security=reality")
	}
	if s := q.Get("insecure"); s != "" {
		v, err := strconv.ParseBool(s)
		if err != nil {
			return nil, errors.New("invalid insecure flag")
		}
		n["skip-cert-verify"] = v
	}
	if q.Get("alpn") != "" {
		values := []any{}
		for _, v := range strings.Split(q.Get("alpn"), ",") {
			values = append(values, v)
		}
		n["alpn"] = values
	}
	network := q.Get("type")
	if network != "" && network != "tcp" {
		n["network"] = network
	}
	if network == "ws" {
		opts := map[string]any{}
		if q.Get("path") != "" {
			opts["path"] = q.Get("path")
		}
		if q.Get("host") != "" {
			opts["headers"] = map[string]any{"Host": q.Get("host")}
		}
		n["ws-opts"] = opts
	} else if q.Get("path") != "" || q.Get("host") != "" {
		return nil, errors.New("path/host require WebSocket transport")
	}
	if network == "grpc" {
		n["grpc-opts"] = map[string]any{"grpc-service-name": q.Get("serviceName")}
	} else if q.Get("serviceName") != "" {
		return nil, errors.New("serviceName requires gRPC")
	}
	// Sub-Store URI parsers enable UDP by default for VLESS and AnyTLS.
	// Keep this explicit so native Egern output does not default to UDP off.
	if typ == "vless" || typ == "anytls" {
		if _, ok := n["udp"]; !ok {
			n["udp"] = true
		}
		if flag(n, "tls") {
			if _, ok := n["skip-cert-verify"]; !ok {
				n["skip-cert-verify"] = false
			}
		}
	}
	return n, nil
}
func parseVMess(raw string) (Node, error) {
	b, err := decode64(raw)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&m); err != nil {
		return nil, errors.New("invalid VMess JSON")
	}
	allowed := strings.Fields("v ps add port id aid scy net type host path tls sni alpn fp")
	for k := range m {
		ok := false
		for _, a := range allowed {
			if a == k {
				ok = true
			}
		}
		if !ok {
			return nil, fmt.Errorf("unsupported VMess field %s", k)
		}
	}
	for _, k := range []string{"ps", "add", "id", "scy", "net", "type", "host", "path", "tls", "sni", "alpn", "fp"} {
		if v, ok := m[k]; ok {
			if _, ok := v.(string); !ok {
				return nil, fmt.Errorf("VMess %s must be string", k)
			}
		}
	}
	if m["v"] != nil && text(m["v"]) != "2" {
		return nil, fmt.Errorf("only VMess URI version 2 is supported")
	}

	if text(m["type"]) != "" && text(m["type"]) != "none" {
		return nil, errors.New("unsupported VMess header type")
	}
	n := Node{"type": "vmess", "name": text(m["ps"]), "server": text(m["add"]), "port": text(m["port"]), "uuid": text(m["id"])}
	if text(n["name"]) == "" {
		n["name"] = "vmess-" + text(m["add"])
	}
	if m["aid"] != nil {
		n["alterId"], err = integer(m["aid"])
		if err != nil {
			return nil, errors.New("invalid VMess alterId")
		}
	}
	if m["scy"] != nil {
		n["cipher"] = text(m["scy"])
	}
	switch text(m["tls"]) {
	case "tls":
		n["tls"] = true
	case "", "none":
	default:
		return nil, errors.New("unsupported VMess TLS mode")
	}
	for src, dst := range map[string]string{"sni": "servername", "fp": "client-fingerprint"} {
		if text(m[src]) != "" {
			n[dst] = text(m[src])
		}
	}
	if text(m["alpn"]) != "" {
		values := []any{}
		for _, v := range strings.Split(text(m["alpn"]), ",") {
			values = append(values, v)
		}
		n["alpn"] = values
	}
	network := text(m["net"])
	if network != "" && network != "tcp" {
		n["network"] = network
	}
	switch network {
	case "ws":
		n["ws-opts"] = map[string]any{"path": text(m["path"]), "headers": map[string]any{"Host": text(m["host"])}}
	case "grpc":
		n["grpc-opts"] = map[string]any{"grpc-service-name": text(m["path"])}
	default:
		if text(m["path"]) != "" || text(m["host"]) != "" {
			return nil, errors.New("unsupported VMess transport fields")
		}
	}
	return n, nil
}
