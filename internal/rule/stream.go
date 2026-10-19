// Input: context, bufio, io, internal/cache, internal/httpclient, internal/types
// Output: Parser.ParseStream
// Pos: 业务层-规则集逐行转换、分类临时文件及完整结果组装
package rule

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/script-hub-org/script-hub/internal/cache"
	"github.com/script-hub-org/script-hub/internal/httpclient"
	"github.com/script-hub-org/script-hub/internal/types"
)

// ParseStream 下载源文件到可重用临时文件，逐行分类并组装文件响应。
// 仅完整下载的源参与转换；任何源失败均禁止缓存最终结果。
// 成功后 workspace 的生命周期由返回的 FileBody 接管。
func (p *Parser) ParseStream(ctx context.Context, input ParseInput, workspace *cache.Workspace) (types.ResponseData, error) {
	success := false
	defer func() {
		if !success {
			workspace.Close()
		}
	}()
	var files []*cache.TempFile
	create := func() (*cache.TempFile, error) {
		f, err := workspace.Create()
		if err == nil {
			files = append(files, f)
		}
		return f, err
	}
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	source, err := create()
	if err != nil {
		return types.ResponseData{}, err
	}
	supported, err := create()
	if err != nil {
		return types.ResponseData{}, err
	}
	unsupported, err := create()
	if err != nil {
		return types.ResponseData{}, err
	}
	excluded, err := create()
	if err != nil {
		return types.ResponseData{}, err
	}
	decoder := p.newLineParser(input)
	target := strings.ToLower(input.TargetApp)
	isStash := strings.Contains(target, "stash")
	isLoon := strings.Contains(target, "loon")
	isSurge := strings.Contains(target, "surge") || strings.Contains(target, "shadowrocket") || strings.Contains(target, "egern") || strings.Contains(target, "lancex")
	shadow := strings.Contains(target, "shadowrocket")
	domainSet := strings.Contains(target, "domain-set")
	domainSet2 := strings.HasSuffix(target, "2")
	var total, otherCount, excludedCount, selected int
	var incomplete, hasBody bool
	clean := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "t&zd;", ","), " ;#", " #") }
	partWriters := map[*cache.TempFile]*bufio.Writer{supported: bufio.NewWriterSize(supported, 64<<10), unsupported: bufio.NewWriterSize(unsupported, 64<<10), excluded: bufio.NewWriterSize(excluded, 64<<10)}
	write := func(dst *cache.TempFile, s string) error {
		_, err := io.WriteString(partWriters[dst], clean(s))
		return err
	}
	consume := func(r io.Reader) error {
		scanner := bufio.NewScanner(r)
		maxLine := p.cfg.MaxBodyKB*1024 + 1
		if maxLine < (2<<20)+1 {
			maxLine = (2 << 20) + 1
		}
		scanner.Buffer(make([]byte, 64<<10), maxLine)
		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				return err
			}
			rl := decoder.parse(scanner.Text())
			if rl == nil {
				continue
			}
			if rl.Excluded {
				if err := write(excluded, "\n#"+hoStToHost(rl.Raw)); err != nil {
					return err
				}
				excludedCount++
				continue
			}
			rt := strings.ToUpper(rl.RuleType)
			logical := rt == "OR" || rt == "AND" || rt == "NOT"
			if (logical && (isStash || isLoon)) || (!logical && isUnsupportedForTarget(rt, isStash, isLoon, isSurge)) {
				raw := rl.Raw
				if logical {
					raw = rl.Value
				}
				if err := write(unsupported, "\n#"+hoStToHost(raw)); err != nil {
					return err
				}
				otherCount++
				continue
			}
			var formatted string
			if logical {
				formatted = rl.Value
			} else if isStash {
				formatted = formatStashRule(*rl)
			} else if isLoon {
				formatted = formatLoonRule(*rl)
			} else {
				formatted = formatSurgeRule(*rl, shadow)
			}
			if formatted == "" {
				continue
			}
			total++
			if domainSet {
				upper := strings.ToUpper(formatted)
				isDomain := strings.Contains(upper, "DOMAIN,") || strings.Contains(upper, "DOMAIN-SUFFIX,")
				if (!domainSet2 && !isDomain) || (domainSet2 && isDomain) {
					continue
				}
				if !domainSet2 {
					if isStash {
						formatted = ruleReStashDomain.ReplaceAllString(formatted, "")
						formatted = ruleReStashDomainSF.ReplaceAllString(formatted, ".")
						formatted = ruleReStripPolicy.ReplaceAllString(formatted, "$1")
					} else {
						formatted = ruleReDomain.ReplaceAllString(formatted, "")
						formatted = ruleReDomainSuffix.ReplaceAllString(formatted, ".")
					}
				}
			}
			prefix := ""
			if selected > 0 {
				prefix = "\n"
			}
			if err := write(supported, prefix+formatted); err != nil {
				return err
			}
			selected++
		}
		return scanner.Err()
	}
	local := input.Arguments["localtext"]
	localOnly := len(input.URLs) == 0 || input.URLs[0] == "http://local.text" || input.URLs[0] == "http://local.text/"
	if !localOnly {
		headers := httpclient.ParseCustomHeaders(input.Arguments["headers"])
		for _, u := range input.URLs {
			if err := ctx.Err(); err != nil {
				return types.ResponseData{}, err
			}
			if err := source.Reset(); err != nil {
				return types.ResponseData{}, err
			}
			decoded, err := decodeURL(u)
			if err != nil {
				decoded = u
			}
			status, fetchErr := p.client.GetTo(ctx, decoded, headers, source)
			if fetchErr != nil || status != 200 {
				incomplete = true
				if fetchErr != nil {
					p.incFetchError()
				}
				if err := ctx.Err(); err != nil {
					return types.ResponseData{}, err
				}
				// 与旧行为一致：404 是一条被过滤的注释，但输入并非空文件。
				if status == 404 && fetchErr == nil {
					hasBody = true
				}
				continue
			}
			if source.Size() > 0 {
				hasBody = true
			}
			if _, err := source.Seek(0, io.SeekStart); err != nil {
				return types.ResponseData{}, err
			}
			if err := consume(source); err != nil {
				return types.ResponseData{}, err
			}
		}
	}
	if local != "" {
		hasBody = true
		if err := consume(strings.NewReader(local)); err != nil {
			return types.ResponseData{}, err
		}
	}
	if err := source.Reset(); err != nil {
		return types.ResponseData{}, err
	}
	for _, w := range partWriters {
		if err := w.Flush(); err != nil {
			return types.ResponseData{}, err
		}
	}
	output, err := workspace.Create()
	if err != nil {
		return types.ResponseData{}, err
	}
	failed := true
	defer func() {
		if failed {
			output.Close()
		}
	}()
	writer := bufio.NewWriterSize(output, 64<<10)
	copyPart := func(f *cache.TempFile) error {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		_, err := io.CopyBuffer(writer, struct{ io.Reader }{f}, make([]byte, 64<<10))
		return err
	}
	empty := !hasBody || (domainSet && selected == 0)
	if !empty {
		header := fmt.Sprintf("#规则数量:%d\n#不支持的规则数量:%d\n#已排除的规则数量:%d", total, otherCount, excludedCount)
		if domainSet {
			label := "域名"
			if domainSet2 {
				label = "非域名"
			}
			header = fmt.Sprintf("#总规则数量:%d\n#%s规则数量:%d\n#不支持的规则数量:%d\n#已排除的规则数量:%d", total, label, selected, otherCount, excludedCount)
		}
		if _, err := io.WriteString(writer, header); err != nil {
			return types.ResponseData{}, err
		}
		if otherCount > 0 {
			if _, err := io.WriteString(writer, "\n#不支持的规则:"); err != nil {
				return types.ResponseData{}, err
			}
			if err := copyPart(unsupported); err != nil {
				return types.ResponseData{}, err
			}
		}
		if excludedCount > 0 {
			if _, err := io.WriteString(writer, "\n#已排除规则:"); err != nil {
				return types.ResponseData{}, err
			}
			if err := copyPart(excluded); err != nil {
				return types.ResponseData{}, err
			}
		}
		if _, err := io.WriteString(writer, "\n\n#-----------------以下为解析后的规则-----------------#\n\n"); err != nil {
			return types.ResponseData{}, err
		}
		if isStash && (!domainSet || domainSet2) {
			if _, err := io.WriteString(writer, "payload:\n"); err != nil {
				return types.ResponseData{}, err
			}
		}
		if err := copyPart(supported); err != nil {
			return types.ResponseData{}, err
		}
	}
	if err := writer.Flush(); err != nil {
		return types.ResponseData{}, err
	}
	info, err := output.Stat()
	if err != nil {
		return types.ResponseData{}, err
	}
	rd := types.ResponseData{Status: 200, Incomplete: incomplete, Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"}, File: types.NewFileBody(output.File, 0, info.Size(), workspace.Close)}
	if hasBody {
		rd.Headers["Access-Control-Allow-Origin"] = "*"
		rd.Headers["Access-Control-Allow-Methods"] = "POST,GET,OPTIONS,PUT,DELETE"
		rd.Headers["Access-Control-Allow-Headers"] = "Origin, X-Requested-With, Content-Type, Accept"
	}
	success = true
	failed = false
	return rd, nil
}
