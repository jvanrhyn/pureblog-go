package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jvanrhyn/pureblog-go/internal/blog"
	"github.com/jvanrhyn/pureblog-go/internal/i18n"
)

// baseFuncs are shared by public and admin templates.
func (s *Server) baseFuncs(tr func() *i18n.Translator) template.FuncMap {
	return template.FuncMap{
		"t": func(key string, args ...any) string { return tr().T(key, args...) },
		// tHTML escapes the translation, then substitutes HTML values for
		// placeholders (used for strings that embed links).
		"tHTML": func(key string, args ...any) template.HTML {
			out := template.HTMLEscapeString(tr().T(key))
			for i := 0; i+1 < len(args); i += 2 {
				out = strings.ReplaceAll(out, "{"+fmt.Sprint(args[i])+"}", fmt.Sprint(args[i+1]))
			}
			return template.HTML(out)
		},
		"safeHTML": func(s string) template.HTML { return template.HTML(s) },
		"safeCSS":  func(s string) template.CSS { return template.CSS(s) },
		"safeURL":  func(s string) template.URL { return template.URL(s) },
		"json": func(v any) template.JS {
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(true)
			_ = enc.Encode(v)
			return template.JS(strings.TrimSpace(buf.String()))
		},
		"urlquery":   url.QueryEscape,
		"pathescape": url.PathEscape,
		"add":        func(a, b int) int { return a + b },
		"sub":        func(a, b int) int { return a - b },
		"seq": func(from, to int) []int {
			var out []int
			for i := from; i <= to; i++ {
				out = append(out, i)
			}
			return out
		},
		"dict": func(kv ...any) map[string]any {
			m := map[string]any{}
			for i := 0; i+1 < len(kv); i += 2 {
				m[fmt.Sprint(kv[i])] = kv[i+1]
			}
			return m
		},
		"join":       strings.Join,
		"hasPrefix":  strings.HasPrefix,
		"contains":   strings.Contains,
		"lower":      strings.ToLower,
		"upper":      strings.ToUpper,
		"trimPrefix": strings.TrimPrefix,
		"date":       func(value string) string { return s.site.FormatDate(value, "") },
		"dateFmt":    func(value, layout string) string { return s.site.FormatDate(value, layout) },
		"isoDate":    func(value string) string { return s.site.FormatDate(value, time.RFC3339) },
		"tagSlug":    blog.NormalizeTag,
		"number":     numberFormat,
		"number1":    func(f float64) string { return numberFormat1(f) },
		"markdown": func(md string) template.HTML {
			return template.HTML(s.site.RenderMarkdown(md, blog.RenderContext{}))
		},
		"markdownPost": func(p *blog.Post) template.HTML {
			return template.HTML(s.site.RenderMarkdown(p.Content, blog.RenderContext{PostTitle: p.Title}))
		},
		"markdownPage": func(p *blog.Page) template.HTML {
			return template.HTML(s.site.RenderMarkdown(p.Content, blog.RenderContext{PageTitle: p.Title}))
		},
		"excerpt": func(p *blog.Post) string {
			if d := strings.TrimSpace(p.Description); d != "" {
				return d
			}
			return blog.Excerpt(p.Content, 200)
		},
		"readingTime": s.site.ReadingTime,
		"tagLinks": func(tags []string) template.HTML {
			var links []string
			bp := s.site.BasePath()
			for _, tag := range tags {
				tag = strings.TrimSpace(tag)
				if tag == "" {
					continue
				}
				links = append(links, `<a href="`+bp+`/tag/`+template.HTMLEscapeString(blog.NormalizeTag(tag))+`">`+template.HTMLEscapeString(tag)+`</a>`)
			}
			return template.HTML(strings.Join(links, ", "))
		},
	}
}

// numberFormat formats an integer with thousands separators.
func numberFormat(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

func numberFormat1(f float64) string {
	whole := int(f)
	frac := int((f-float64(whole))*10 + 0.5)
	if frac >= 10 {
		whole++
		frac -= 10
	}
	return numberFormat(whole) + "." + strconv.Itoa(frac)
}

// ---------------------------------------------------------------------------
// Public templates (overridable)
// ---------------------------------------------------------------------------

// overrideSignature fingerprints user templates so changes trigger a reload.
func (s *Server) overrideSignature() string {
	var parts []string
	for _, dir := range []string{s.site.IncludesDir, s.site.LayoutsDir} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
				continue
			}
			if info, err := e.Info(); err == nil {
				parts = append(parts, fmt.Sprintf("%s/%s:%d:%d", dir, e.Name(), info.ModTime().UnixNano(), info.Size()))
			}
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

// publicTemplates returns the public template set, rebuilding it when user
// overrides in content/includes or content/layouts change.
func (s *Server) publicTemplates() (*template.Template, error) {
	sig := s.overrideSignature()
	s.tmplMu.Lock()
	defer s.tmplMu.Unlock()
	if s.publicSet != nil && sig == s.publicSig {
		return s.publicSet, nil
	}
	set, err := s.buildPublicTemplates()
	if err != nil {
		if s.publicSet != nil {
			// Keep serving the last good set if an override is broken.
			return s.publicSet, err
		}
		return nil, err
	}
	s.publicSet, s.publicSig = set, sig
	return set, nil
}

var safeName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func (s *Server) buildPublicTemplates() (*template.Template, error) {
	var set *template.Template
	funcs := s.baseFuncs(s.site.T)
	funcs["footer"] = func(data *PublicPage) (template.HTML, error) {
		var buf bytes.Buffer
		if err := set.ExecuteTemplate(&buf, "footer", data); err != nil {
			return "", err
		}
		out := buf.String()
		inject := strings.TrimSpace(data.footerInject)
		if inject != "" {
			if i := strings.LastIndex(strings.ToLower(out), "</footer>"); i >= 0 {
				out = out[:i] + inject + "\n" + out[i:]
			} else {
				out += "\n" + inject + "\n"
			}
		}
		return template.HTML(out), nil
	}
	funcs["postMeta"] = func(data *PublicPage) (template.HTML, error) {
		var buf bytes.Buffer
		if err := set.ExecuteTemplate(&buf, "post-meta", data); err != nil {
			return "", err
		}
		ctx := blog.RenderContext{}
		if data.Post != nil {
			ctx.PostTitle, ctx.ContentTitle = data.Post.Title, data.Post.Title
		}
		out := s.site.RenderShortcodes(buf.String(), ctx)
		return template.HTML(s.site.RenderDataLoops(out)), nil
	}
	// renderLayout runs the post's custom layout (content/layouts/NAME.html).
	funcs["renderLayout"] = func(data *PublicPage) (template.HTML, error) {
		var buf bytes.Buffer
		if err := set.ExecuteTemplate(&buf, data.LayoutName, data); err != nil {
			return "", err
		}
		return template.HTML(buf.String()), nil
	}
	funcs["field"] = func(p *blog.Post, name string) string {
		if p == nil {
			return ""
		}
		return p.Field(name)
	}

	set = template.New("public").Funcs(funcs)
	if _, err := set.ParseFS(s.embedded, "templates/public/*.html"); err != nil {
		return nil, err
	}
	// User overrides: content/includes/*.html may redefine header, masthead,
	// footer, post-list or post-meta, and provide custom route templates.
	for _, dir := range []struct{ path, prefix string }{{s.site.IncludesDir, ""}, {s.site.LayoutsDir, "layout:"}} {
		files, _ := filepath.Glob(filepath.Join(dir.path, "*.html"))
		sort.Strings(files)
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			name := filepath.Base(f)
			if dir.prefix != "" {
				name = dir.prefix + strings.TrimSuffix(name, ".html")
			}
			if _, err := set.New(name).Parse(string(raw)); err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
		}
	}
	return set, nil
}

// ---------------------------------------------------------------------------
// Admin templates
// ---------------------------------------------------------------------------

func (s *Server) buildAdminTemplates(tr *i18n.Translator) (*template.Template, error) {
	get := s.site.T
	if tr != nil {
		get = func() *i18n.Translator { return tr }
	}
	funcs := s.baseFuncs(get)
	funcs["settingsNav"] = func() []settingsNavItem { return settingsNavItems }
	funcs["featureToggle"] = func(img ImageItem) string {
		if img.IsFeature {
			return ""
		}
		return img.Filename
	}
	funcs["baseName"] = path.Base
	set := template.New("admin").Funcs(funcs)
	return set.ParseFS(s.embedded, "templates/admin/*.html")
}

type settingsNavItem struct{ Path, Label, Icon string }

var settingsNavItems = []settingsNavItem{
	{"admin/settings-site", "admin.settings.nav.site", "globe"},
	{"admin/settings-theme", "admin.settings.nav.theme", "paintbrush"},
	{"admin/settings-css", "admin.settings.nav.css", "braces"},
	{"admin/settings-user", "admin.settings.nav.user", "user"},
	{"admin/settings-updates", "admin.settings.nav.updates", "upgrade"},
}

// setupTemplates returns an admin template set translated into lang, used
// by the setup wizard before a language has been saved.
func (s *Server) setupTemplates(lang string) (*template.Template, error) {
	s.tmplMu.Lock()
	defer s.tmplMu.Unlock()
	if set, ok := s.setupSets[lang]; ok {
		return set, nil
	}
	set, err := s.buildAdminTemplates(s.site.I18n.Translator(lang))
	if err != nil {
		return nil, err
	}
	s.setupSets[lang] = set
	return set, nil
}

// customRouteTemplate resolves a custom route target to a template name.
func (s *Server) customRouteTemplate(target string) string {
	target = strings.TrimSpace(strings.ReplaceAll(target, "\\", "/"))
	if target == "" {
		return ""
	}
	if strings.HasPrefix(target, "/") {
		if !strings.HasPrefix(target, "/content/includes/") {
			return ""
		}
		target = strings.TrimPrefix(target, "/content/includes/")
	}
	if strings.Contains(target, "/") || strings.Contains(target, "..") {
		return ""
	}
	// Targets written for the PHP version (reading.php) map to reading.html.
	if strings.HasSuffix(strings.ToLower(target), ".php") {
		target = target[:len(target)-4] + ".html"
	}
	if !strings.HasSuffix(strings.ToLower(target), ".html") || !safeName.MatchString(strings.TrimSuffix(target, filepath.Ext(target))) {
		return ""
	}
	if !blog.IsFile(filepath.Join(s.site.IncludesDir, target)) {
		return ""
	}
	return target
}
