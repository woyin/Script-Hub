// Package types 定义了 Script Hub 各模块间共享的数据类型。
// 所有解析器/转换器的输出都通过 ResponseWriter 接口统一到 HTTP 响应。
package types

// ResponseData 是所有解析器/转换器返回的统一响应结构。
// 对应 JS 版 $done({ response: { status, headers, body } }) 的输出格式。
type ResponseData struct {
	Status  int               `json:"status"`  // HTTP 状态码
	Headers map[string]string `json:"headers"` // 响应头
	Body    string            `json:"body"`    // 响应体
}

// ResponseWriter 是所有解析器输出必须实现的接口。
// 通过 GetResponse() 将特定格式的输出统一转换为 HTTP 响应。
type ResponseWriter interface {
	GetResponse() ResponseData
}
