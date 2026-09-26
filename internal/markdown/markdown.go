// Package markdown renders Markdown to HTML with the same extensions as Pure
// Blog's ParsedownPureblog: Markdown Extra features (tables, footnotes,
// definition lists, heading attributes, markdown="1" blocks), paragraph
// attributes ({.notice}), link attributes ({target="_blank"}) and
// rel="noopener noreferrer" on external links.
package markdown

import (
	"bytes"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Renderer converts Markdown to HTML.
type Renderer struct {
	md goldmark.Markdown
}

// New creates a renderer. baseHost returns the host of the site's base URL;
// links to that host are not treated as external.
func New(baseHost func() string) *Renderer {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.Table,
			extension.Strikethrough,
			extension.Footnote,
			extension.DefinitionList,
			extension.NewLinkify(extension.WithLinkifyAllowedProtocols([]string{"http:", "https:"})),
		),
		goldmark.WithParserOptions(
			parser.WithAttribute(),
			parser.WithASTTransformers(util.Prioritized(&transformer{baseHost: baseHost}, 100)),
		),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
	return &Renderer{md: md}
}

// Render converts Markdown to HTML. It adds loading="lazy" to images.
func (r *Renderer) Render(src string) string {
	return restorePrivateUseEmoji(addLazyLoading(r.render(src)))
}

// RenderFragment converts Markdown to HTML without post-processing.
func (r *Renderer) RenderFragment(src string) string {
	return r.render(src)
}

func (r *Renderer) render(src string) string {
	src = strings.ReplaceAll(strings.ReplaceAll(src, "\r\n", "\n"), "\r", "\n")
	src, blocks := r.extractMarkdownHTML(src)
	var buf bytes.Buffer
	if err := r.md.Convert([]byte(src), &buf); err != nil {
		return ""
	}
	out := buf.String()
	for token, html := range blocks {
		out = strings.Replace(out, "<p>"+token+"</p>", html, 1)
		out = strings.Replace(out, token, html, 1)
	}
	return out
}

// ---------------------------------------------------------------------------
// markdown="1" blocks
// ---------------------------------------------------------------------------

var (
	fenceRe     = regexp.MustCompile("(?s)```.*?```|~~~.*?~~~")
	mdOpenTagRe = regexp.MustCompile(`(?mi)^[ \t]*<(div|p|blockquote|aside|details|summary|section|article|header|footer|nav|figure|figcaption|pre|ul|ol|li|dl|dt|dd|table|thead|tbody|tfoot|tr|th|td|main|form)\b([^>]*?)\s+markdown\s*=\s*(?:"1"|'1'|1)([^>]*)>`)
)

// extractMarkdownHTML replaces each block-level HTML element carrying
// markdown="1" with a placeholder paragraph, rendering its inner content as
// Markdown. Fenced code blocks are left untouched.
func (r *Renderer) extractMarkdownHTML(src string) (string, map[string]string) {
	if !strings.Contains(src, "markdown") {
		return src, nil
	}
	fences := map[string]string{}
	i := 0
	src = fenceRe.ReplaceAllStringFunc(src, func(m string) string {
		tok := "PBFENCEBLOCK" + strconv.Itoa(i) + "X"
		i++
		fences[tok] = m
		return tok
	})
	restore := func(s string) string {
		for tok, v := range fences {
			s = strings.ReplaceAll(s, tok, v)
		}
		return s
	}

	blocks := map[string]string{}
	n := 0
	for {
		loc := mdOpenTagRe.FindStringSubmatchIndex(src)
		if loc == nil {
			break
		}
		tag := strings.ToLower(src[loc[2]:loc[3]])
		attrs := src[loc[4]:loc[5]] + src[loc[6]:loc[7]]
		innerStart := loc[1]
		innerEnd, closeEnd := findClosing(src, innerStart, tag)
		if innerEnd < 0 {
			// Unclosed element: drop the attribute and stop processing.
			src = src[:loc[0]] + "<" + src[loc[2]:loc[3]] + attrs + ">" + src[loc[1]:]
			continue
		}
		inner := restore(src[innerStart:innerEnd])
		rendered := "<" + tag + attrs + ">\n" + strings.TrimRight(r.render(inner), "\n") + "\n</" + tag + ">"
		tok := "PBMDHTMLBLOCK" + strconv.Itoa(n) + "X"
		n++
		blocks[tok] = rendered
		src = src[:loc[0]] + "\n\n" + tok + "\n\n" + src[closeEnd:]
	}
	return restore(src), blocks
}

// findClosing returns the start of the matching </tag> and the index after it.
func findClosing(src string, from int, tag string) (int, int) {
	lower := strings.ToLower(src)
	open := "<" + tag
	closeTag := "</" + tag
	depth := 1
	pos := from
	for pos < len(lower) {
		nextOpen := indexTag(lower, open, pos)
		nextClose := indexTag(lower, closeTag, pos)
		if nextClose < 0 {
			return -1, -1
		}
		if nextOpen >= 0 && nextOpen < nextClose {
			depth++
			pos = nextOpen + len(open)
			continue
		}
		depth--
		end := strings.IndexByte(lower[nextClose:], '>')
		if end < 0 {
			return -1, -1
		}
		if depth == 0 {
			return nextClose, nextClose + end + 1
		}
		pos = nextClose + end + 1
	}
	return -1, -1
}

// indexTag finds "<tag" followed by a delimiter (space, > or /).
func indexTag(s, prefix string, from int) int {
	for from < len(s) {
		i := strings.Index(s[from:], prefix)
		if i < 0 {
			return -1
		}
		i += from
		after := i + len(prefix)
		if after < len(s) {
			c := s[after]
			if c == ' ' || c == '>' || c == '/' || c == '\t' || c == '\n' || c == '\r' {
				return i
			}
		}
		from = after
	}
	return -1
}

// ---------------------------------------------------------------------------
// AST transformer: paragraph attributes, link attributes, external links
// ---------------------------------------------------------------------------

type transformer struct {
	baseHost func() string
}

var (
	paragraphAttrRe = regexp.MustCompile(`[ ]*\{((?:[#.][-\w]+[ ]*)+)\}[ ]*$`)
	linkAttrRe      = regexp.MustCompile(`^[ ]*\{(.*?)\}`)
	customAttrRe    = regexp.MustCompile(`\.([-\w]+)|#([-\w]+)|([-\w]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s}]+))`)
	externalHrefRe  = regexp.MustCompile(`(?i)^(https?:)?//`)
)

func (t *transformer) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	src := reader.Source()
	var paragraphs []*ast.Paragraph
	var links []ast.Node
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.Paragraph:
			paragraphs = append(paragraphs, node)
		case *ast.Link, *ast.AutoLink:
			links = append(links, node)
		}
		return ast.WalkContinue, nil
	})
	for _, p := range paragraphs {
		applyParagraphAttributes(p, src)
	}
	host := ""
	if t.baseHost != nil {
		host = t.baseHost()
	}
	for _, l := range links {
		if link, ok := l.(*ast.Link); ok {
			applyLinkAttributes(link, src)
		}
		markExternal(l, src, host)
	}
}

// trailingText returns the run of Text nodes at the end of a paragraph.
func trailingText(p *ast.Paragraph) []*ast.Text {
	var nodes []*ast.Text
	for c := p.LastChild(); c != nil; c = c.PreviousSibling() {
		tn, ok := c.(*ast.Text)
		if !ok {
			break
		}
		nodes = append([]*ast.Text{tn}, nodes...)
		if tn.SoftLineBreak() || tn.HardLineBreak() {
			if len(nodes) > 1 {
				nodes = nodes[1:]
			}
			break
		}
	}
	return nodes
}

func applyParagraphAttributes(p *ast.Paragraph, src []byte) {
	nodes := trailingText(p)
	if len(nodes) == 0 {
		return
	}
	var sb strings.Builder
	for _, n := range nodes {
		sb.Write(n.Segment.Value(src))
	}
	joined := sb.String()
	m := paragraphAttrRe.FindStringSubmatchIndex(joined)
	if m == nil {
		return
	}
	for key, val := range parseAttributes(joined[m[2]:m[3]]) {
		p.SetAttributeString(key, []byte(val))
	}
	trimTrailing(nodes, len(joined)-m[0])
}

// trimTrailing removes n bytes from the end of a run of text nodes.
func trimTrailing(nodes []*ast.Text, n int) {
	for i := len(nodes) - 1; i >= 0 && n > 0; i-- {
		seg := nodes[i].Segment
		l := seg.Len()
		if l <= n {
			n -= l
			nodes[i].Parent().RemoveChild(nodes[i].Parent(), nodes[i])
			continue
		}
		nodes[i].Segment = seg.WithStop(seg.Stop - n)
		n = 0
	}
}

func applyLinkAttributes(link *ast.Link, src []byte) {
	var nodes []*ast.Text
	var sb strings.Builder
	for c := link.NextSibling(); c != nil; c = c.NextSibling() {
		tn, ok := c.(*ast.Text)
		if !ok {
			break
		}
		nodes = append(nodes, tn)
		sb.Write(tn.Segment.Value(src))
		if tn.SoftLineBreak() || tn.HardLineBreak() {
			break
		}
	}
	if len(nodes) == 0 {
		return
	}
	joined := sb.String()
	m := linkAttrRe.FindStringSubmatchIndex(joined)
	if m == nil {
		return
	}
	attrs := parseCustomAttributes(joined[m[2]:m[3]])
	if len(attrs) == 0 {
		return
	}
	if cls, ok := attrs["class"]; ok {
		if existing, ok := link.AttributeString("class"); ok {
			attrs["class"] = string(existing.([]byte)) + " " + cls
		}
	}
	for k, v := range attrs {
		link.SetAttributeString(k, []byte(v))
	}
	// Consume the attribute block from the following text nodes.
	n := m[1]
	for _, tn := range nodes {
		if n <= 0 {
			break
		}
		seg := tn.Segment
		if seg.Len() <= n {
			n -= seg.Len()
			if tn.SoftLineBreak() || tn.HardLineBreak() {
				// Keep the node so the line break survives, but empty it.
				tn.Segment = seg.WithStart(seg.Stop)
				continue
			}
			tn.Parent().RemoveChild(tn.Parent(), tn)
			continue
		}
		tn.Segment = seg.WithStart(seg.Start + n)
		n = 0
	}
}

// parseAttributes handles the {#id .class} form used on paragraphs.
func parseAttributes(s string) map[string]string {
	attrs := map[string]string{}
	var classes []string
	for _, part := range strings.Fields(s) {
		switch {
		case strings.HasPrefix(part, "#"):
			attrs["id"] = part[1:]
		case strings.HasPrefix(part, "."):
			classes = append(classes, part[1:])
		}
	}
	if len(classes) > 0 {
		attrs["class"] = strings.Join(classes, " ")
	}
	return attrs
}

// parseCustomAttributes handles .class, #id and key="value" pairs on links.
func parseCustomAttributes(s string) map[string]string {
	attrs := map[string]string{}
	var classes []string
	for _, m := range customAttrRe.FindAllStringSubmatch(s, -1) {
		switch {
		case m[1] != "":
			classes = append(classes, m[1])
		case m[2] != "":
			attrs["id"] = m[2]
		case m[3] != "":
			val := m[4]
			if val == "" {
				val = m[5]
			}
			if val == "" {
				val = m[6]
			}
			attrs[m[3]] = val
		}
	}
	if len(classes) > 0 {
		attrs["class"] = strings.Join(classes, " ")
	}
	return attrs
}

func markExternal(n ast.Node, src []byte, baseHost string) {
	var href string
	switch l := n.(type) {
	case *ast.Link:
		href = string(l.Destination)
	case *ast.AutoLink:
		href = string(l.URL(src))
		if l.AutoLinkType == ast.AutoLinkEmail {
			return
		}
		if !strings.Contains(href, "://") && strings.HasPrefix(strings.ToLower(href), "www.") {
			href = "http://" + href
		}
	}
	if !externalHrefRe.MatchString(href) {
		return
	}
	if baseHost != "" {
		u, err := url.Parse(href)
		if err == nil && u.Host != "" && strings.EqualFold(u.Hostname(), baseHost) {
			return
		}
	}
	if _, ok := n.AttributeString("rel"); !ok {
		n.SetAttributeString("rel", []byte("noopener noreferrer"))
	}
}

// ---------------------------------------------------------------------------
// HTML post-processing
// ---------------------------------------------------------------------------

var (
	imgTagRe  = regexp.MustCompile(`(?i)<img\s+([^>]+)>`)
	loadingRe = regexp.MustCompile(`(?i)\bloading\s*=`)
)

func addLazyLoading(html string) string {
	return imgTagRe.ReplaceAllStringFunc(html, func(tag string) string {
		m := imgTagRe.FindStringSubmatch(tag)
		if loadingRe.MatchString(m[1]) {
			return tag
		}
		return `<img loading="lazy" ` + m[1] + ">"
	})
}

// restorePrivateUseEmoji maps characters in U+F000–U+F8FF back to the
// supplementary plane, matching the PHP version's workaround for emoji that
// were stored with their high bits stripped.
func restorePrivateUseEmoji(html string) string {
	hasPUA := false
	for _, r := range html {
		if r >= 0xF000 && r <= 0xF8FF {
			hasPUA = true
			break
		}
	}
	if !hasPUA {
		return html
	}
	var b strings.Builder
	b.Grow(len(html))
	for _, r := range html {
		if r >= 0xF000 && r <= 0xF8FF {
			r += 0x10000
		}
		b.WriteRune(r)
	}
	return b.String()
}
