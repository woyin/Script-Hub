# internal/subscription 文件夹索引

节点订阅解析、处理和目标客户端输出。与现有 rule/rewrite 管线独立。

- `subscription.go`：Clash YAML、URI/Base64 输入、节点验证、名称处理、去重及排序。
- `export.go`：Clash、Shadowrocket/Base64 URI、原始 URI、Loon、Egern 输出及兼容能力；已识别的客户端提示按目标规则处理；未知参数和不支持的传输明确拒绝。
- `anytls_test.go`：AnyTLS 原生 Egern/Loon 输出、证书校验默认值、混合订阅和参数别名回归测试。
- `packet_encoding_test.go`：VLESS packetEncoding 参数解析、往返、无效值及目标限制回归测试。
- `subscription_test.go`：输出结构、转义/IPv6、URI 往返、传输和 TLS 限制、未知字段、并发导出。

输出是节点订阅，不是完整客户端配置；真实客户端导入和连接测试需另行验收。
