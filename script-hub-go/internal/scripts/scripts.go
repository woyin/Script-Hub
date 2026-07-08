package scripts

import (
	"fmt"
	"regexp"
	"strings"
)

// ReplaceHeaderInput contains input for header replacement.
type ReplaceHeaderInput struct {
	Method  string
	URL     string
	Headers map[string]string
	Arg     string
}

// ReplaceHeaderOutput contains the result of header replacement.
type ReplaceHeaderOutput struct {
	Status  int
	Headers map[string]string
	Body    string
}

// ReplaceHeader applies regex-based header replacements.
func ReplaceHeader(input ReplaceHeaderInput) ReplaceHeaderOutput {
	replacements := parseArgReplacements(input.Arg)
	headers := input.Headers

	for _, rep := range replacements {
		re := getRegexp(rep.Pattern)
		for k, v := range headers {
			if re.MatchString(k + ": " + v) {
				newVal := re.ReplaceAllString(k+": "+v, rep.Replacement)
				parts := strings.SplitN(newVal, ": ", 2)
				if len(parts) == 2 {
					delete(headers, k)
					headers[parts[0]] = parts[1]
				}
			}
		}
	}

	return ReplaceHeaderOutput{
		Status:  200,
		Headers: headers,
		Body:    "",
	}
}

type replacement struct {
	Pattern     string
	Replacement string
}

func parseArgReplacements(arg string) []replacement {
	if arg == "" {
		return nil
	}
	var reps []replacement
	pairs := strings.Split(arg, "&")
	for _, pair := range pairs {
		parts := strings.SplitN(pair, "->", 2)
		if len(parts) == 2 {
			reps = append(reps, replacement{
				Pattern:     parts[0],
				Replacement: parts[1],
			})
		}
	}
	return reps
}

func getRegexp(pattern string) *regexp.Regexp {
	reParts := regexp.MustCompile(`^/(.*?)/([gims]*)$`).FindStringSubmatch(pattern)
	if len(reParts) > 2 {
		return regexp.MustCompile(reParts[1])
	}
	return regexp.MustCompile(pattern)
}

// EchoResponseInput contains input for echo response.
type EchoResponseInput struct {
	Arg       string
	URL       string
	Headers   map[string]string
	Content   string
}

// EchoResponseOutput contains the result of echo response.
type EchoResponseOutput struct {
	Status     int
	Headers    map[string]string
	Body       string
	Redirect   bool
	RedirectURL string
}

// EchoResponse processes echo-response type rewrites.
func EchoResponse(input EchoResponseInput) EchoResponseOutput {
	args := parseQueryString(input.Arg)
	contentType := args["type"]
	echoURL := args["url"]
	statusCode := 200
	if sc := args["status-code"]; sc != "" {
		fmt.Sscanf(sc, "%d", &statusCode)
	}

	if contentType != "" && echoURL != "" {
		return EchoResponseOutput{
			Status:  statusCode,
			Headers: map[string]string{"Content-Type": contentType},
			Body:    input.Content,
		}
	}

	if echoURL != "" {
		return EchoResponseOutput{
			Status:      302,
			Redirect:    true,
			RedirectURL: echoURL,
		}
	}

	if text := args["text"]; text != "" {
		return EchoResponseOutput{
			Status:  statusCode,
			Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
			Body:    text,
		}
	}

	return EchoResponseOutput{
		Status:  statusCode,
		Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
		Body:    input.Content,
	}
}

// ReplaceBodyInput contains input for body replacement.
type ReplaceBodyInput struct {
	Body string
	Arg  string
}

// ReplaceBodyOutput contains the result of body replacement.
type ReplaceBodyOutput struct {
	Body string
}

// ReplaceBody applies regex-based body replacements.
func ReplaceBody(input ReplaceBodyInput) ReplaceBodyOutput {
	body := input.Body
	replacements := parseArgReplacements(input.Arg)

	for _, rep := range replacements {
		re := getRegexp(rep.Pattern)
		body = re.ReplaceAllString(body, rep.Replacement)
	}

	return ReplaceBodyOutput{Body: body}
}

func parseQueryString(query string) map[string]string {
	result := make(map[string]string)
	if query == "" {
		return result
	}
	for _, pair := range strings.Split(query, "&") {
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			result[kv[0]] = kv[1]
		} else {
			result[kv[0]] = ""
		}
	}
	return result
}
