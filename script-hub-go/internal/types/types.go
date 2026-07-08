package types

// ResponseData is the common response structure from all parsers/converters.
type ResponseData struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// ResponseWriter is the interface that all parser outputs must implement.
type ResponseWriter interface {
	GetResponse() ResponseData
}
