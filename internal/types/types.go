// Input: （无外部依赖，纯类型定义）
// Output: type ResponseData, type ResponseWriter, ResponseWriter.GetResponse()
// Pos: 数据层-共享类型，定义所有解析器/转换器的统一响应结构
//
// 本注释在文件修改时自动更新，同时触发 FOLDER_INDEX 和 PROJECT_INDEX 更新

// Package types 定义了 Script Hub 各模块间共享的数据类型。
// 所有解析器/转换器的输出都通过 ResponseWriter 接口统一到 HTTP 响应。
package types

// ResponseData 是所有解析器/转换器返回的统一响应结构。
// 对应 JS 版 $done({ response: { status, headers, body } }) 的输出格式。
// 注意：本结构不参与 JSON 序列化——字段直接用于写 HTTP 响应，
// 故无 json tag。
type ResponseData struct {
	Status  int    // HTTP 状态码
	Headers map[string]string // 响应头
	Body    string // 响应体
}

// ResponseWriter 是所有解析器输出必须实现的接口。
// 通过 GetResponse() 将特定格式的输出统一转换为 HTTP 响应。
type ResponseWriter interface {
	GetResponse() ResponseData
}
