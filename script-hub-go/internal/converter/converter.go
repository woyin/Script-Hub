package converter

import (
	"context"
	"fmt"
	"strings"

	"github.com/script-hub-org/script-hub/internal/config"
	"github.com/script-hub-org/script-hub/internal/eval"
	"github.com/script-hub-org/script-hub/internal/httpclient"
	"github.com/script-hub-org/script-hub/internal/types"
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
	var err error

	// Fetch script content
	if input.URL != "" && !strings.HasPrefix(input.URL, "http://local.text") {
		scriptContent, _, err = c.client.Get(ctx, input.URL)
		if err != nil {
			return ConvertOutput{
				Content: "Script fetch error: " + err.Error(),
				Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
				Status:  500,
			}, err
		}
	} else {
		scriptContent = input.LocalText
	}

	if scriptContent == "" {
		return ConvertOutput{
			Content: "",
			Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
			Status:  200,
		}, nil
	}

	// Apply eval operations on original content (before conversion)
	evalParams := eval.EvalParamsFromArgs(input.Arguments)
	scriptContent = eval.ApplyBeforeConversion(ctx, scriptContent, evalParams, c.client)

	// Determine if this is a script conversion (type ends with -script)
	isScriptConversion := strings.HasSuffix(input.SourceType, "-script")
	compatibilityOnly := isTrue(input.Arguments["compatibilityOnly"])
	wrapResponse := isTrue(input.Arguments["wrap_response"])
	prepend := input.Arguments["prepend"]

	// Convert based on target app
	targetApp := strings.ToLower(input.TargetApp)
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

	// Apply eval operations on converted content (after conversion)
	converted = eval.ApplyAfterConversion(ctx, converted, evalParams, c.client)

	return ConvertOutput{
		Content: converted,
		Headers: map[string]string{
			"Content-Type":                "text/plain; charset=utf-8",
			"Access-Control-Allow-Origin":  "*",
			"Access-Control-Allow-Methods": "POST,GET,OPTIONS,PUT,DELETE",
			"Access-Control-Allow-Headers": "Origin, X-Requested-With, Content-Type, Accept",
		},
		Status:      200,
		ContentType: "text/plain; charset=utf-8",
	}, nil
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
		result = strings.ReplaceAll(result, "$prefs.remove(", "// $prefs.remove(")
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

// isTrue checks if a string represents a truthy value.
func isTrue(s string) bool {
	return s == "true" || s == "1" || s == "True"
}
