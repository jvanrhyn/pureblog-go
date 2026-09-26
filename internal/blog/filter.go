package blog

import (
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// RenderContext carries the titles available to shortcodes.
type RenderContext struct {
	PostTitle    string
	PageTitle    string
	ContentTitle string
}

// RenderMarkdown applies shortcodes and data loops, then renders Markdown.
func (s *Site) RenderMarkdown(md string, ctx RenderContext) string {
	return s.MD.Render(s.FilterContent(md, ctx))
}

var (
	fencedBacktickRe = regexp.MustCompile("```[\\s\\S]*?```")
	fencedTildeRe    = regexp.MustCompile(`~~~[\s\S]*?~~~`)
	inlineSpanRe     = regexp.MustCompile("`[^`\\n]*`")
	shortcodeRe      = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.]+)\s*\}\}`)
	dataLoopRe       = regexp.MustCompile(`(?s)\{%\s*for\s+site\s+in\s+site\.data\.([a-zA-Z0-9_-]+)\s*%\}(.*?)\{%\s*endfor\s*%\}`)
	feedIfRe         = regexp.MustCompile(`(?s)\{%\s*if\s+site\.feed\s*%\}(.*?)\{%\s*endif\s*%\}`)
	markdownifyRe    = regexp.MustCompile(`\{\{\s*site\.([a-zA-Z0-9_]+)\s*\|\s*markdownify\s*\}\}`)
	siteFieldRe      = regexp.MustCompile(`\{\{\s*site\.([a-zA-Z0-9_]+)\s*\}\}`)
)

// FilterContent replaces shortcodes and data loops outside code blocks.
func (s *Site) FilterContent(md string, ctx RenderContext) string {
	tokens := map[string]string{}
	i := 0
	protect := func(re *regexp.Regexp, prefix string) {
		md = re.ReplaceAllStringFunc(md, func(m string) string {
			tok := prefix + strconv.Itoa(i) + "__"
			i++
			tokens[tok] = m
			return tok
		})
	}
	protect(fencedBacktickRe, "__PUREBLOG_GO_CODE_BLOCK_")
	protect(fencedTildeRe, "__PUREBLOG_GO_CODE_BLOCK_")
	protect(inlineSpanRe, "__PUREBLOG_GO_INLINE_CODE_")

	md = s.RenderShortcodes(md, ctx)
	md = s.RenderDataLoops(md)

	// Restore in reverse so nested tokens resolve.
	for n := i - 1; n >= 0; n-- {
		for _, prefix := range []string{"__PUREBLOG_GO_INLINE_CODE_", "__PUREBLOG_GO_CODE_BLOCK_"} {
			tok := prefix + strconv.Itoa(n) + "__"
			if v, ok := tokens[tok]; ok {
				md = strings.Replace(md, tok, v, 1)
			}
		}
	}
	return md
}

// RenderShortcodes replaces {{ site_email }}, {{ post_title }} and friends.
func (s *Site) RenderShortcodes(text string, ctx RenderContext) string {
	email := strings.TrimSpace(s.Config.Get().SiteEmail)
	post := strings.TrimSpace(ctx.PostTitle)
	page := strings.TrimSpace(ctx.PageTitle)
	content := strings.TrimSpace(ctx.ContentTitle)
	if content == "" {
		content = post
		if content == "" {
			content = page
		}
	}
	codes := map[string]string{
		"site_email": email, "site.email": email,
		"post_title": post, "page_title": page, "content_title": content,
	}
	return shortcodeRe.ReplaceAllStringFunc(text, func(m string) string {
		key := shortcodeRe.FindStringSubmatch(m)[1]
		if v, ok := codes[key]; ok {
			return v
		}
		return m
	})
}

// RenderDataLoops expands {% for site in site.data.NAME %}...{% endfor %}
// using data/NAME.yml.
func (s *Site) RenderDataLoops(text string) string {
	return dataLoopRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := dataLoopRe.FindStringSubmatch(m)
		items := LoadYAMLList(filepath.Join(s.DataDir, sub[1]+".yml"))
		if len(items) == 0 {
			return ""
		}
		var b strings.Builder
		for _, item := range items {
			chunk := feedIfRe.ReplaceAllStringFunc(sub[2], func(fm string) string {
				if v := item["feed"]; v != "" && v != "0" {
					return feedIfRe.FindStringSubmatch(fm)[1]
				}
				return ""
			})
			chunk = markdownifyRe.ReplaceAllStringFunc(chunk, func(mm string) string {
				return s.MD.RenderFragment(item[markdownifyRe.FindStringSubmatch(mm)[1]])
			})
			chunk = siteFieldRe.ReplaceAllStringFunc(chunk, func(mm string) string {
				return html.EscapeString(item[siteFieldRe.FindStringSubmatch(mm)[1]])
			})
			b.WriteString(chunk)
		}
		return b.String()
	})
}

var yamlItemRe = regexp.MustCompile(`^\s*-\s*(.*)$`)

// LoadYAMLList parses the simple YAML list format used by data files: a list
// of flat mappings, with | and > block scalars.
func LoadYAMLList(path string) []map[string]string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	var items []map[string]string
	var current map[string]string
	blockKey := ""
	var blockLines []string
	blockIndent := -1
	blockFold := false

	flush := func() {
		if blockKey == "" || current == nil {
			blockKey = ""
			return
		}
		for len(blockLines) > 0 && blockLines[len(blockLines)-1] == "" {
			blockLines = blockLines[:len(blockLines)-1]
		}
		if blockFold {
			var r strings.Builder
			for _, bl := range blockLines {
				if bl == "" {
					trimmed := strings.TrimRight(r.String(), " \t\n")
					r.Reset()
					r.WriteString(trimmed + "\n\n")
				} else {
					r.WriteString(bl + " ")
				}
			}
			current[blockKey] = strings.TrimRight(r.String(), " \t\n")
		} else {
			current[blockKey] = strings.Join(blockLines, "\n")
		}
		blockKey, blockLines, blockIndent, blockFold = "", nil, -1, false
	}

	setKV := func(kv string) {
		key, value, ok := strings.Cut(kv, ":")
		if !ok {
			return
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		if value == "|" || value == ">" {
			blockKey, blockFold = key, value == ">"
			return
		}
		current[key] = value
	}

	parseLine := func(line string) {
		if line == "" || strings.HasPrefix(strings.TrimLeft(line, " \t"), "#") {
			return
		}
		if m := yamlItemRe.FindStringSubmatch(line); m != nil {
			if current != nil {
				items = append(items, current)
			}
			current = map[string]string{}
			if rest := strings.TrimSpace(m[1]); rest != "" && strings.Contains(rest, ":") {
				setKV(rest)
			}
			return
		}
		if current == nil || !strings.Contains(strings.TrimLeft(line, " \t"), ":") {
			return
		}
		setKV(strings.TrimSpace(line))
	}

	for _, line := range lines {
		line = strings.TrimRight(line, " \t\r")
		if blockKey != "" {
			if line == "" {
				blockLines = append(blockLines, "")
				continue
			}
			indent := len(line) - len(strings.TrimLeft(line, " \t"))
			if blockIndent < 0 {
				blockIndent = indent
			}
			if indent >= blockIndent && indent > 0 {
				blockLines = append(blockLines, line[blockIndent:])
				continue
			}
			flush()
		}
		parseLine(line)
	}
	flush()
	if current != nil {
		items = append(items, current)
	}
	return items
}
