// Package converter 实现脚本转换引擎。
// 将 QX 脚本转换为目标平台（Surge、Loon、Stash、Shadowrocket）脚本格式，
// 支持 subconverter 代理模式和兼容性模式。
// 对应 JS 版 script-converter.js 的完整功能。
package converter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/script-hub-org/script-hub/internal/config"
	"github.com/script-hub-org/script-hub/internal/eval"
	"github.com/script-hub-org/script-hub/internal/httpclient"
	"github.com/script-hub-org/script-hub/internal/types"
	"github.com/script-hub-org/script-hub/internal/util"
)

// ConvertInput contains the input parameters for script conversion.
type ConvertInput struct {
	URL            string
	LocalText      string
	SourceType     string
	TargetApp      string
	Arguments      map[string]string
	KeepHeader     bool
	JsDelivr       string
}

// ConvertOutput contains the converted script output.
type ConvertOutput struct {
	Content     string
	Headers     map[string]string
	Status      int
	MITM        []string
	ContentType string
}

// GetResponse implements the server.ResponseWriter interface.
func (o ConvertOutput) GetResponse() types.ResponseData {
	return types.ResponseData{
		Status:  o.Status,
		Headers: o.Headers,
		Body:    o.Content,
	}
}

// Converter handles QX script to Surge/Loon script conversion.
type Converter struct {
	cfg    *config.Config
	client *httpclient.Client
}

// NewConverter creates a new script converter.
func NewConverter(cfg *config.Config) *Converter {
	return &Converter{
		cfg:    cfg,
		client: httpclient.NewClient(cfg.HTTPTimeout),
	}
}

// Convert fetches and converts a QX script to the target app format.
func (c *Converter) Convert(ctx context.Context, input ConvertInput) (ConvertOutput, error) {
	var scriptContent string

	sourceType := input.SourceType
	isScriptConversion := strings.HasSuffix(sourceType, "-script")
	compatibilityOnly := util.IsTrue(input.Arguments["compatibilityOnly"])
	keepHeader := input.KeepHeader
	setHeader := input.Arguments["header"]
	setContentType := input.Arguments["contentType"]
	prepend := input.Arguments["prepend"]
	wrapResponse := util.IsTrue(input.Arguments["wrap_response"])
	subconverterURL := input.Arguments["subconverter"]
	targetApp := strings.ToLower(input.TargetApp)

	// subconverter mode: proxy the request through an external subconverter API
	if subconverterURL != "" {
		subURL := buildSubconverterURL(subconverterURL, input.URL, input.LocalText, input.Arguments)
		reqHeaders := httpclient.ParseCustomHeaders(input.Arguments["headers"])
		body, _, gerr := c.client.GetWithHeaders(ctx, subURL, reqHeaders)
		if gerr != nil {
			return ConvertOutput{
				Content: gerr.Error(),
				Headers: map[string]string{"Content-Type": "text/plain; charset=UTF-8"},
				Status:  500,
			}, gerr
		}
		return ConvertOutput{
			Content: body,
			Headers: c.corsHeaders(),
			Status:  200,
		}, nil
	}

	// mock type without keepHeader: 302 redirect to the source URL
	if sourceType == "mock" && !keepHeader && !strings.HasPrefix(input.URL, "http://local.text") {
		loc := input.URL
		if input.JsDelivr != "" {
			loc = jsDelivrConvert(loc)
		}
		// Loon quirk: empty body on 3xx triggers issues; JS sends body='loon'
		body := ""
		if strings.HasPrefix(targetApp, "loon") {
			body = "loon"
		}
		return ConvertOutput{
			Content: body,
			Headers: map[string]string{"Location": loc},
			Status:  302,
		}, nil
	}

	// mock type with keepHeader: fetch as binary and build mock response
	// matching JS script-converter.js mock+keepHeader branch (L338-340)
	if sourceType == "mock" && keepHeader && input.URL != "" && !strings.HasPrefix(input.URL, "http://local.text") {
		return c.handleMockKeepHeader(ctx, input, targetApp)
	}

	// Fetch script content
	var upstreamHeaders map[string]string
	if input.URL != "" && !strings.HasPrefix(input.URL, "http://local.text") {
		reqHeaders := httpclient.ParseCustomHeaders(input.Arguments["headers"])
		rawBytes, _, respHeaders, ferr := c.client.GetBytesWithHeaders(ctx, input.URL, reqHeaders)
		if ferr != nil {
			return ConvertOutput{
				Content: "Script fetch error: " + ferr.Error(),
				Headers: map[string]string{"Content-Type": "text/plain; charset=UTF-8"},
				Status:  500,
			}, ferr
		}
		scriptContent = string(rawBytes)
		upstreamHeaders = respHeaders
	} else {
		scriptContent = input.LocalText
	}

	if scriptContent == "" {
		return ConvertOutput{
			Content: "",
			Headers: map[string]string{"Content-Type": "text/plain; charset=UTF-8"},
			Status:  200,
		}, nil
	}

	// Apply eval operations on original content (before conversion)
	evalParams := eval.EvalParamsFromArgs(input.Arguments)
	scriptContent = eval.ApplyBeforeConversion(ctx, scriptContent, evalParams, c.client)

	// Convert based on target app
	var converted string

	switch {
	case strings.Contains(targetApp, "surge") || strings.Contains(targetApp, "shadowrocket"):
		converted = c.convertQXToSurge(scriptContent, input, isScriptConversion, compatibilityOnly, wrapResponse, prepend)
	case strings.Contains(targetApp, "loon"):
		converted = c.convertQXToLoon(scriptContent, input, isScriptConversion, compatibilityOnly, wrapResponse, prepend)
	case strings.Contains(targetApp, "stash"):
		converted = c.convertQXToStash(scriptContent, input, isScriptConversion, compatibilityOnly, wrapResponse, prepend)
	default:
		converted = scriptContent
	}

	// Wrap the full prefix+body in a try-catch compatibility guard, matching JS.
	if isScriptConversion || compatibilityOnly {
		converted = wrapTryCatch(converted)
	}

	// mock type: wrap the body into a done({response:{...}}) script payload
	if sourceType == "mock" {
		converted = buildMockScript(converted)
	}

	// Apply eval operations on converted content (after conversion)
	converted = eval.ApplyAfterConversion(ctx, converted, evalParams, c.client)

	// Build response headers: merge upstream headers with CORS
	headers := make(map[string]string)
	for k, v := range upstreamHeaders {
		headers[k] = v
	}
	for k, v := range c.corsHeaders() {
		headers[k] = v
	}
	headers = applyHeaderOverrides(headers, setHeader, setContentType, targetApp, input.URL)

	return ConvertOutput{
		Content: converted,
		Headers: headers,
		Status:  200,
	}, nil
}

// handleMockKeepHeader handles mock type with keepHeader=true.
// Fetches the URL as raw bytes (preserving binary content), builds a mock
// response script that either wraps the body as a string or as a binary
// Uint8Array, and forwards the original response headers with CORS.
// Matches JS script-converter.js mock+keepHeader branch (L338-470).
func (c *Converter) handleMockKeepHeader(ctx context.Context, input ConvertInput, targetApp string) (ConvertOutput, error) {
	reqHeaders := httpclient.ParseCustomHeaders(input.Arguments["headers"])
	rawBytes, status, origHeaders, err := c.client.GetBytesWithHeaders(ctx, input.URL, reqHeaders)
	if err != nil {
		return ConvertOutput{
			Content: "Script fetch error: " + err.Error(),
			Headers: map[string]string{"Content-Type": "text/plain; charset=UTF-8"},
			Status:  500,
		}, err
	}

	isBinary := !utf8.Valid(rawBytes)

	// Build mock response headers: original headers + CORS
	mockHeaders := make(map[string]string)
	for k, v := range origHeaders {
		mockHeaders[k] = v
	}
	mockHeaders["Access-Control-Allow-Origin"] = "*"
	mockHeaders["Access-Control-Allow-Methods"] = "POST,GET,OPTIONS,PUT,DELETE"
	mockHeaders["Access-Control-Allow-Headers"] = "Origin, X-Requested-With, Content-Type, Accept"

	// Apply user-specified header/contentType overrides
	setHeader := input.Arguments["header"]
	setContentType := input.Arguments["contentType"]
	if setHeader != "" {
		for _, i := range strings.Split(setHeader, "|") {
			i = strings.TrimSpace(i)
			if strings.Contains(i, ":") {
				kv := strings.SplitN(i, ":", 2)
				k := strings.TrimSpace(kv[0])
				v := strings.TrimSpace(kv[1])
				if k != "" && v != "" {
					mockHeaders[k] = v
				}
			}
		}
	}
	if targetApp == "plain-text" || strings.HasSuffix(input.URL, ".txt") {
		setContentTypeKey(mockHeaders, "text/plain; charset=utf-8")
	}
	if setContentType != "" {
		setContentTypeKey(mockHeaders, setContentType)
	}
	for _, k := range []string{"Content-Type", "content-type"} {
		if v, ok := mockHeaders[k]; ok && v != "" {
			mockHeaders[k] = utf8ContentType(v)
		}
	}
	for _, k := range []string{"content-length", "Content-Length", "content-encoding", "Content-Encoding", "content-security-policy", "Content-Security-Policy"} {
		delete(mockHeaders, k)
	}

	mockHeadersJSON, _ := json.Marshal(mockHeaders)

	var scriptBody string
	if isBinary {
		// Binary mock: encode bytes as charcode string, use strToArray on JS side
		binStr := binArrayToStr(rawBytes)
		binStrJSON, _ := json.Marshal(binStr)
		scriptBody = fmt.Sprintf(`function strToArray(str) {
  var ret = new Uint8Array(str.length)
  for (var i = 0; i < str.length; i++) {
    ret[i] = str.charCodeAt(i)
  }
  return ret
}

let done = $done
let result = {
  response: {
      status: %d,
      headers: %s,
      body: strToArray(%s),
    },
}
done(result)`, status, string(mockHeadersJSON), string(binStrJSON))
	} else {
		// Text mock: JSON-encode the body string
		bodyJSON, _ := json.Marshal(string(rawBytes))
		scriptBody = fmt.Sprintf(`let done = $done
let result = {
  response: {
      status: %d,
      body: %s,
      headers: %s,
    },
}
done(result)`, status, string(bodyJSON), string(mockHeadersJSON))
	}

	// Apply eval on the final mock script
	evalParams := eval.EvalParamsFromArgs(input.Arguments)
	scriptBody = eval.ApplyAfterConversion(ctx, scriptBody, evalParams, c.client)

	// The outer response is always a JS script served as text/plain
	outerHeaders := c.corsHeaders()
	return ConvertOutput{
		Content: scriptBody,
		Headers: outerHeaders,
		Status:  200,
	}, nil
}

// binArrayToStr converts raw bytes to a string by treating each byte as a
// character code, matching the JS binArrayToStr function in script-converter.js.
func binArrayToStr(data []byte) string {
	var b strings.Builder
	b.Grow(len(data))
	for _, byte := range data {
		b.WriteByte(byte)
	}
	return b.String()
}

// corsHeaders returns the default CORS response headers.
func (c *Converter) corsHeaders() map[string]string {
	return map[string]string{
		"Content-Type":                "text/plain; charset=UTF-8",
		"Access-Control-Allow-Origin":  "*",
		"Access-Control-Allow-Methods": "POST,GET,OPTIONS,PUT,DELETE",
		"Access-Control-Allow-Headers": "Origin, X-Requested-With, Content-Type, Accept",
	}
}

// wrapTryCatch wraps the converted content in the JS try-catch guard that
// matches script-converter.js — captures errors from the original script and
// degrades gracefully instead of crashing the whole response.
func wrapTryCatch(content string) string {
	return `const _scriptSonverterCompatibilityType = typeof $response !== 'undefined' ? 'response' : typeof $request !== 'undefined' ? 'request' : ''
const _scriptSonverterCompatibilityDone = $done
try {
  ` + content + `
} catch (e) {
  console.log('❌ Script Hub 兼容层捕获到原脚本未处理的错误')
  if (_scriptSonverterCompatibilityType) {
    console.log('⚠️ 故不修改本次' + (_scriptSonverterCompatibilityType === 'response' ? '响应' : '请求'))
  } else {
    console.log('⚠️ 因类型非请求或响应, 抛出错误')
  }
  console.log(e)
  if (_scriptSonverterCompatibilityType) {
    _scriptSonverterCompatibilityDone({})
  } else {
    throw e
  }
}`
}

// buildMockScript wraps the converted body into a done({response:{...}}) payload.
// Mirrors the JS mock branch: the body becomes the mock response body.
func buildMockScript(body string) string {
	bodyJSON, _ := json.Marshal(body)
	return `
// mock response generated by Script Hub
let done = $done
let result = {
  response: {
      status: 200,
      body: ` + string(bodyJSON) + `,
      headers: {
        'Content-Type': 'text/plain; charset=UTF-8',
        'Access-Control-Allow-Origin': '*',
        'Access-Control-Allow-Methods': 'POST,GET,OPTIONS,PUT,DELETE',
        'Access-Control-Allow-Headers': 'Origin, X-Requested-With, Content-Type, Accept',
      },
    },
}
done(result)`
}

// applyHeaderOverrides applies user-specified header/content-type overrides and
// charset fixing, mirroring the JS response post-processing.
func applyHeaderOverrides(headers map[string]string, setHeader, setContentType, targetApp, rawURL string) map[string]string {
	if setHeader != "" {
		for _, i := range strings.Split(setHeader, "|") {
			i = strings.TrimSpace(i)
			if strings.Contains(i, ":") {
				kv := strings.SplitN(i, ":", 2)
				k := strings.TrimSpace(kv[0])
				v := strings.TrimSpace(kv[1])
				if k != "" && v != "" {
					headers[k] = v
				}
			}
		}
	}

	// plain-text target forces text/plain content type
	if targetApp == "plain-text" || strings.HasSuffix(rawURL, ".txt") {
		setContentTypeKey(headers, "text/plain; charset=utf-8")
	}
	if setContentType != "" {
		setContentTypeKey(headers, setContentType)
	}

	// Append charset=UTF-8 to text/* and application/* content types
	for _, k := range []string{"Content-Type", "content-type"} {
		if v, ok := headers[k]; ok && v != "" {
			headers[k] = utf8ContentType(v)
		}
	}

	// Strip headers that must not be forwarded on a converted response
	for _, k := range []string{
		"content-length", "Content-Length",
		"content-encoding", "Content-Encoding",
		"content-security-policy", "Content-Security-Policy",
	} {
		delete(headers, k)
	}
	return headers
}

func setContentTypeKey(headers map[string]string, ct string) {
	if _, ok := headers["Content-Type"]; ok {
		headers["Content-Type"] = ct
	} else {
		headers["content-type"] = ct
	}
}

// utf8ContentType appends charset=UTF-8 to text/application content types.
func utf8ContentType(t string) string {
	if regexp.MustCompile(`(?i)^(text|application)/.+`).MatchString(t) &&
		!regexp.MustCompile(`(?i);\s*charset\s*=\s*`).MatchString(t) {
		return t + "; charset=UTF-8"
	}
	return t
}

// convertQXToSurge converts QX script syntax to Surge compatible syntax.
func (c *Converter) convertQXToSurge(script string, input ConvertInput, isScriptConversion, compatibilityOnly, wrapResponse bool, prepend string) string {
	result := script

	if isScriptConversion && !compatibilityOnly {
		// Full QX→Surge conversion: inject compatibility shim + QX mock layer
		prefix := surgeCompatPrefix(wrapResponse)
		// Replace $done with wrapped version
		result = strings.ReplaceAll(result, "$done(", "_scriptSonverterDone(")
		result = prefix + "\n" + result
	} else if compatibilityOnly {
		// Compatibility-only mode: inject headers proxy + try-catch, no QX mock
		prefix := compatOnlyPrefix()
		result = prefix + "\n" + result
	}

	// QX API simple replacements (always applied for script conversion)
	if isScriptConversion {
		result = strings.ReplaceAll(result, "$notify(", "$notification.post(")
		result = strings.ReplaceAll(result, "$prefs.valueForKey(", "$persistentStore.read(")
		result = strings.ReplaceAll(result, "$prefs.setValueForKey(", "$persistentStore.write(")
		result = strings.ReplaceAll(result, "$prefs.removeValueForKey(", "$persistentStore.write('', ")
		result = strings.ReplaceAll(result, "$prefs.get(", "$persistentStore.read(")
		result = strings.ReplaceAll(result, "$prefs.set(", "$persistentStore.write(")
		result = strings.ReplaceAll(result, "$prefs.remove(", "$persistentStore.write('', ")
		result = strings.ReplaceAll(result, "$task.fetch(", "$http.get(")
	}

	// Argument injection
	if argStr, ok := input.Arguments["argument"]; ok && argStr != "" {
		result = "var $argument = \"" + argStr + "\";\n\n" + result
	}

	// Prepend custom code
	if prepend != "" && isScriptConversion {
		result = prepend + "\n" + result
	}

	return result
}

// convertQXToLoon converts QX script syntax to Loon compatible syntax.
func (c *Converter) convertQXToLoon(script string, input ConvertInput, isScriptConversion, compatibilityOnly, wrapResponse bool, prepend string) string {
	result := script

	if isScriptConversion && !compatibilityOnly {
		prefix := loonCompatPrefix(wrapResponse)
		result = strings.ReplaceAll(result, "$done(", "_scriptSonverterDone(")
		result = prefix + "\n" + result
	} else if compatibilityOnly {
		prefix := compatOnlyPrefix()
		result = prefix + "\n" + result
	}

	if isScriptConversion {
		result = strings.ReplaceAll(result, "$notify(", "$notification.post(")
		result = strings.ReplaceAll(result, "$prefs.valueForKey(", "$persistentStore.read(")
		result = strings.ReplaceAll(result, "$prefs.setValueForKey(", "$persistentStore.write(")
		result = strings.ReplaceAll(result, "$prefs.removeValueForKey(", "$persistentStore.write('', ")
		result = strings.ReplaceAll(result, "$prefs.get(", "$persistentStore.read(")
		result = strings.ReplaceAll(result, "$prefs.set(", "$persistentStore.write(")
	}

	if argStr, ok := input.Arguments["argument"]; ok && argStr != "" {
		result = "var $argument = \"" + argStr + "\";\n\n" + result
	}

	if prepend != "" && isScriptConversion {
		result = prepend + "\n" + result
	}

	return result
}

// convertQXToStash converts QX script syntax to Stash compatible syntax.
// Stash uses the same JS engine as Surge, so the conversion is similar.
func (c *Converter) convertQXToStash(script string, input ConvertInput, isScriptConversion, compatibilityOnly, wrapResponse bool, prepend string) string {
	// Stash uses Surge-compatible JS engine
	return c.convertQXToSurge(script, input, isScriptConversion, compatibilityOnly, wrapResponse, prepend)
}

// --- Compatibility Layer Templates ---

// surgeCompatPrefix returns the full QX→Surge compatibility shim.
// This matches the original script-converter.js prefix + qxMock.
func surgeCompatPrefix(wrapResponse bool) string {
	wrapStr := "false"
	if wrapResponse {
		wrapStr = "true"
	}

	return `// Compatibility layer by Script Hub (Go)
// QX -> Surge/Shadowrocket compatibility shim

if (typeof $request !== 'undefined') {
  const lowerCaseRequestHeaders = Object.fromEntries(
    Object.entries($request.headers).map(([k, v]) => [k.toLowerCase(), v])
  );
  $request.headers = new Proxy(lowerCaseRequestHeaders, {
    get: function (target, propKey, receiver) {
      return Reflect.get(target, propKey.toLowerCase(), receiver);
    },
    set: function (target, propKey, value, receiver) {
      return Reflect.set(target, propKey.toLowerCase(), value, receiver);
    },
  });
}
if (typeof $response !== 'undefined') {
  const lowerCaseResponseHeaders = Object.fromEntries(
    Object.entries($response.headers).map(([k, v]) => [k.toLowerCase(), v])
  );
  $response.headers = new Proxy(lowerCaseResponseHeaders, {
    get: function (target, propKey, receiver) {
      return Reflect.get(target, propKey.toLowerCase(), receiver);
    },
    set: function (target, propKey, value, receiver) {
      return Reflect.set(target, propKey.toLowerCase(), value, receiver);
    },
  });
}
Object.getOwnPropertyNames($httpClient).forEach(method => {
  if(typeof $httpClient[method] === 'function') {
    $httpClient[method] = new Proxy($httpClient[method], {
      apply: (target, ctx, args) => {
        for (let field in args?.[0]?.headers) {
          if (['host'].includes(field.toLowerCase())) {
            delete args[0].headers[field];
          } else if (['number'].includes(typeof args[0].headers[field])) {
            args[0].headers[field] = args[0].headers[field].toString();
          }
        }
        return Reflect.apply(target, ctx, args);
      }
    });
  }
})

// QX API mocks
var setInterval = () => {}
var clearInterval = () => {}
var $task = {
  fetch: url => {
    return new Promise((resolve, reject) => {
      if (url.method == 'POST') {
        $httpClient.post(url, (error, response, data) => {
          if (response) {
            response.body = data
            resolve(response, { error: error })
          } else {
            resolve(null, { error: error })
          }
        })
      } else {
        $httpClient.get(url, (error, response, data) => {
          if (response) {
            response.body = data
            resolve(response, { error: error })
          } else {
            resolve(null, { error: error })
          }
        })
      }
    })
  },
}

var $prefs = {
  removeValueForKey: key => {
    let result
    try { result = $persistentStore.write('', key) } catch (e) {}
    if ($persistentStore.read(key) == null) return result
    try { result = $persistentStore.write(null, key) } catch (e) {}
    if ($persistentStore.read(key) == null) return result
    const err = 'Cannot simulate removeValueForKey for key: ' + key
    console.log(err)
    return result
  },
  valueForKey: key => $persistentStore.read(key),
  setValueForKey: (val, key) => $persistentStore.write(val, key),
}

var $notify = (title = '', subt = '', desc = '', opts) => {
  const toEnvOpts = (rawopts) => {
    if (!rawopts) return rawopts
    if (typeof rawopts === 'string') {
      if ('undefined' !== typeof $loon) return rawopts
      else if('undefined' !== typeof $rocket) return rawopts
      else return { url: rawopts }
    } else if (typeof rawopts === 'object') {
      if ('undefined' !== typeof $loon) {
        let openUrl = rawopts.openUrl || rawopts.url || rawopts['open-url']
        let mediaUrl = rawopts.mediaUrl || rawopts['media-url']
        return { openUrl, mediaUrl }
      } else {
        let openUrl = rawopts.url || rawopts.openUrl || rawopts['open-url']
        if('undefined' !== typeof $rocket) return openUrl
        return { url: openUrl }
      }
    }
    return undefined
  }
  console.log(title, subt, desc, toEnvOpts(opts))
  $notification.post(title, subt, desc, toEnvOpts(opts))
}

var _scriptSonverterOriginalDone = $done
var _scriptSonverterDone = (val = {}) => {
  let result
  if (
    (typeof $request !== 'undefined' &&
    typeof val === 'object' &&
    typeof val.status !== 'undefined' &&
    typeof val.headers !== 'undefined' &&
    typeof val.body !== 'undefined') || ` + wrapStr + `
  ) {
    try {
      for (const part of val?.status?.split(' ')) {
        const statusCode = parseInt(part, 10)
        if (!isNaN(statusCode)) {
          val.status = statusCode
          break
        }
      }
    } catch (e) {}
    if (!val.status) val.status = 200
    if (!val.headers) {
      val.headers = {
        'Content-Type': 'text/plain; charset=UTF-8',
        'Access-Control-Allow-Origin': '*',
        'Access-Control-Allow-Methods': 'POST,GET,OPTIONS,PUT,DELETE',
        'Access-Control-Allow-Headers': 'Origin, X-Requested-With, Content-Type, Accept',
      }
    }
    result = { response: val }
  } else {
    result = val
  }
  console.log('$done')
  try { console.log(JSON.stringify(result)) } catch (e) { console.log(result) }
  _scriptSonverterOriginalDone(result)
}
var window = globalThis
window.$done = _scriptSonverterDone
var global = globalThis
global.$done = _scriptSonverterDone`
}

// loonCompatPrefix returns the QX→Loon compatibility shim.
// Loon supports $notification.post and $persistentStore natively,
// so we only need $task, $prefs, $notify mocks and $done wrapper.
func loonCompatPrefix(wrapResponse bool) string {
	wrapStr := "false"
	if wrapResponse {
		wrapStr = "true"
	}

	return `// Compatibility layer by Script Hub (Go)
// QX -> Loon compatibility shim

if (typeof $request !== 'undefined') {
  const lowerCaseRequestHeaders = Object.fromEntries(
    Object.entries($request.headers).map(([k, v]) => [k.toLowerCase(), v])
  );
  $request.headers = new Proxy(lowerCaseRequestHeaders, {
    get: function (target, propKey, receiver) {
      return Reflect.get(target, propKey.toLowerCase(), receiver);
    },
    set: function (target, propKey, value, receiver) {
      return Reflect.set(target, propKey.toLowerCase(), value, receiver);
    },
  });
}
if (typeof $response !== 'undefined') {
  const lowerCaseResponseHeaders = Object.fromEntries(
    Object.entries($response.headers).map(([k, v]) => [k.toLowerCase(), v])
  );
  $response.headers = new Proxy(lowerCaseResponseHeaders, {
    get: function (target, propKey, receiver) {
      return Reflect.get(target, propKey.toLowerCase(), receiver);
    },
    set: function (target, propKey, value, receiver) {
      return Reflect.set(target, propKey.toLowerCase(), value, receiver);
    },
  });
}

// QX API mocks for Loon
var setInterval = () => {}
var clearInterval = () => {}
var $task = {
  fetch: url => {
    return new Promise((resolve, reject) => {
      if (url.method == 'POST') {
        $httpClient.post(url, (error, response, data) => {
          if (response) {
            response.body = data
            resolve(response, { error: error })
          } else {
            resolve(null, { error: error })
          }
        })
      } else {
        $httpClient.get(url, (error, response, data) => {
          if (response) {
            response.body = data
            resolve(response, { error: error })
          } else {
            resolve(null, { error: error })
          }
        })
      }
    })
  },
}

var $prefs = {
  removeValueForKey: key => {
    try { $persistentStore.write('', key) } catch (e) {}
    return $persistentStore.read(key) == null
  },
  valueForKey: key => $persistentStore.read(key),
  setValueForKey: (val, key) => $persistentStore.write(val, key),
}

var $notify = (title = '', subt = '', desc = '', opts) => {
  const toEnvOpts = (rawopts) => {
    if (!rawopts) return rawopts
    if (typeof rawopts === 'string') return { openUrl: rawopts }
    if (typeof rawopts === 'object') {
      let openUrl = rawopts.openUrl || rawopts.url || rawopts['open-url']
      let mediaUrl = rawopts.mediaUrl || rawopts['media-url']
      return { openUrl, mediaUrl }
    }
    return undefined
  }
  $notification.post(title, subt, desc, toEnvOpts(opts))
}

var _scriptSonverterOriginalDone = $done
var _scriptSonverterDone = (val = {}) => {
  let result
  if (
    (typeof $request !== 'undefined' &&
    typeof val === 'object' &&
    typeof val.status !== 'undefined' &&
    typeof val.headers !== 'undefined' &&
    typeof val.body !== 'undefined') || ` + wrapStr + `
  ) {
    try {
      for (const part of val?.status?.split(' ')) {
        const statusCode = parseInt(part, 10)
        if (!isNaN(statusCode)) { val.status = statusCode; break }
      }
    } catch (e) {}
    if (!val.status) val.status = 200
    if (!val.headers) {
      val.headers = {
        'Content-Type': 'text/plain; charset=UTF-8',
        'Access-Control-Allow-Origin': '*',
        'Access-Control-Allow-Methods': 'POST,GET,OPTIONS,PUT,DELETE',
        'Access-Control-Allow-Headers': 'Origin, X-Requested-With, Content-Type, Accept',
      }
    }
    result = { response: val }
  } else {
    result = val
  }
  _scriptSonverterOriginalDone(result)
}
var window = globalThis
window.$done = _scriptSonverterDone
var global = globalThis
global.$done = _scriptSonverterDone`
}

// compatOnlyPrefix returns the compatibility-only prefix (headers proxy + try-catch, no QX mock).
// This is used when compatibilityOnly=true — only fixes header case sensitivity,
// does not inject the full QX API mock layer.
func compatOnlyPrefix() string {
	return `// Compatibility-only layer by Script Hub (Go)
if (typeof $request !== 'undefined') {
  const lowerCaseRequestHeaders = Object.fromEntries(
    Object.entries($request.headers).map(([k, v]) => [k.toLowerCase(), v])
  );
  $request.headers = new Proxy(lowerCaseRequestHeaders, {
    get: function (target, propKey, receiver) {
      return Reflect.get(target, propKey.toLowerCase(), receiver);
    },
    set: function (target, propKey, value, receiver) {
      return Reflect.set(target, propKey.toLowerCase(), value, receiver);
    },
  });
}
if (typeof $response !== 'undefined') {
  const lowerCaseResponseHeaders = Object.fromEntries(
    Object.entries($response.headers).map(([k, v]) => [k.toLowerCase(), v])
  );
  $response.headers = new Proxy(lowerCaseResponseHeaders, {
    get: function (target, propKey, receiver) {
      return Reflect.get(target, propKey.toLowerCase(), receiver);
    },
    set: function (target, propKey, value, receiver) {
      return Reflect.set(target, propKey.toLowerCase(), value, receiver);
    },
  });
}`
}

// jsDelivrConvert converts a GitHub raw URL to jsDelivr CDN URL.
func jsDelivrConvert(urlStr string) string {
	if strings.HasPrefix(urlStr, "https://cdn.jsdelivr.net/") {
		return urlStr
	}
	// Match GitHub raw URLs
	if strings.HasPrefix(urlStr, "https://raw.githubusercontent.com/") {
		parts := strings.SplitN(strings.TrimPrefix(urlStr, "https://raw.githubusercontent.com/"), "/", 4)
		if len(parts) >= 3 {
			user := parts[0]
			repo := parts[1]
			branch := parts[2]
			path := ""
			if len(parts) >= 4 {
				path = parts[3]
			}
			return fmt.Sprintf("https://cdn.jsdelivr.net/gh/%s/%s@%s/%s", user, repo, branch, path)
		}
	}
	// Match github.com URLs
	if strings.HasPrefix(urlStr, "https://github.com/") {
		parts := strings.SplitN(strings.TrimPrefix(urlStr, "https://github.com/"), "/", 4)
		if len(parts) >= 3 {
			user := parts[0]
			repo := parts[1]
			rest := ""
			if len(parts) >= 4 {
				rest = parts[3]
			}
			return fmt.Sprintf("https://cdn.jsdelivr.net/gh/%s/%s@main/%s", user, repo, rest)
		}
	}
	return urlStr
}

// buildSubconverterURL constructs the subconverter request URL with default
// parameters and user-supplied query overrides, matching script-converter.js
// subconverter branch (L293-322).
func buildSubconverterURL(subconverterURL, req, localText string, args map[string]string) string {
	exclude := map[string]bool{
		"type": true, "evalScriptori": true, "evalScriptmodi": true,
		"evalUrlori": true, "evalUrlmodi": true, "subconverter": true,
		"headers": true,
	}

	params := url.Values{}
	// Hardcoded defaults from JS
	params.Set("insert", "false")
	params.Set("append_type", "false")
	params.Set("strict", "false")
	params.Set("sort", "true")
	params.Set("emoji", "false")
	params.Set("list", "true")
	params.Set("udp", "true")
	params.Set("tfo", "false")
	params.Set("expand", "true")
	params.Set("scv", "true")
	params.Set("fdn", "true")
	params.Set("surge.doh", "true")
	params.Set("clash.doh", "true")
	params.Set("new_name", "true")
	// url defaults to localtext || req
	if localText != "" {
		params.Set("url", localText)
	} else {
		params.Set("url", req)
	}
	// Spread user query params (overrides defaults)
	for k, v := range args {
		if !exclude[k] && v != "" {
			params.Set(k, v)
		}
	}

	sep := "?"
	if strings.Contains(subconverterURL, "?") {
		sep = "&"
	}
	return subconverterURL + sep + params.Encode()
}

