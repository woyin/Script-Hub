# internal/subscription 文件夹索引

节点订阅解析、处理和目标客户端输出。与现有 rule/rewrite 管线独立。

- `subscription.go`：Clash YAML、URI/Base64 输入、节点验证、名称处理、去重及排序。
- `export.go`：Clash、Shadowrocket/Base64 URI、原始 URI、Loon、Egern 输出及兼容能力；不可表达的参数明确拒绝。
- `subscription_test.go`：输出结构、转义/IPv6、URI 往返、传输和 TLS 限制、未知字段、并发导出。

输出是节点订阅，不是完整客户端配置；真实客户端导入和连接测试需另行验收。
