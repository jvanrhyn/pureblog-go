package server

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/johanvanrhyn/pureblog-go/internal/blog"
	"github.com/johanvanrhyn/pureblog-go/internal/config"
	"github.com/johanvanrhyn/pureblog-go/internal/og"
)

// NavItem is a navigation link in the masthead.
type NavItem struct {
	Label   string
	URL     string
	Current bool
}

// PostList is a paginated list of posts.
type PostList struct {
	Posts       []*blog.Post
	Layout      string
	CurrentPage int
	TotalPages  int
	TotalPosts  int
	PrevURL     string
	NextURL     string
}

// SearchView is the state of the search page.
type SearchView struct {
	Action  string
	Query   string
	Results *PostList
	Total   int
}

// PublicPage is the data passed to public templates. User template
// overrides can use any of these fields.
type PublicPage struct {
	Config          config.Config
	BasePath        string
	BaseURL         string
	Lang            string
	SiteTitle       string
	SiteTagline     string
	PageTitle       string
	FullTitle       string
	MetaDescription string
	CanonicalURL    string
	OGType          string
	OGLocale        string
	OGImage         string
	Favicon         string
	AppleTouchIcon  string
	ColorMode       string
	Light           blog.Palette
	Dark            blog.Palette
	FontStack       template.CSS
	MonoFontStack   template.CSS
	CustomCSS       template.CSS
	HeadInject      template.HTML
	CSSVersion      string
	Sprite          template.HTML
	ThemePreview    *blog.ThemeInfo
	CSRFToken       string
	Year            string

	// Context flags.
	Post         *blog.Post
	Page         *blog.Page
	IsTag        bool
	Is404        bool
	IsHome       bool
	IsAdmin      bool
	CurrentPath  string
	SiteTitleTag string

	// Masthead navigation.
	ShowNav       bool
	CustomNavOnly bool
	HomeCurrent   bool
	NavPages      []NavItem
	CustomNav     []NavItem

	// Views.
	List       *PostList
	TagName    string
	TagSlug    string
	HideFeed   bool
	Content    template.HTML
	Previous   *blog.Post
	Next       *blog.Post
	BlogFeed   *PostList
	Search     *SearchView
	UseHTMX    bool
	LayoutName string

	footerInject string
}

// FontStackCSS returns the CSS font-family list for a font choice.
func FontStackCSS(stack string) string {
	switch stack {
	case "serif":
		return `"Merriweather", Georgia, Times, "Times New Roman", serif`
	case "mono":
		return `"Iosevka", ui-monospace, "Cascadia Code", "Source Code Pro", Menlo, Consolas, "DejaVu Sans Mono", monospace`
	}
	return `"Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif`
}

var ogLocales = map[string]string{
	"de": "de_DE", "en": "en_US", "es": "es_ES", "fi": "fi_FI", "fr": "fr_FR", "it": "it_IT",
	"nl": "nl_NL", "pl": "pl_PL", "pt": "pt_PT", "ro": "ro_RO", "zh_TW": "zh_TW",
}

func (c *ctx) absolute(href string) string {
	if href != "" && href[0] == '/' {
		return c.baseURL + href
	}
	return href
}

// currentPath returns the request path relative to the base path, without
// surrounding slashes.
func (c *ctx) currentPath() string {
	return strings.Trim(c.r.URL.Path, "/")
}

func (c *ctx) newPublicPage(post *blog.Post, page *blog.Page, title, description string) *PublicPage {
	cfg := c.cfg
	s := c.s
	d := &PublicPage{
		Config:      cfg,
		BasePath:    c.basePath,
		BaseURL:     c.baseURL,
		Lang:        strings.ReplaceAll(cfg.Language, "_", "-"),
		SiteTitle:   cfg.SiteTitle,
		SiteTagline: strings.TrimSpace(cfg.SiteTagline),
		PageTitle:   title,
		Post:        post,
		Page:        page,
		ColorMode:   cfg.Theme.ColorMode,
		Sprite:      s.sprite,
		OGType:      "website",
		Year:        time.Now().In(blog.Location(cfg)).Format("2006"),
		CurrentPath: c.currentPath(),
	}
	if post != nil {
		d.OGType = "article"
	}
	d.IsHome = d.CurrentPath == ""
	if strings.TrimSpace(description) == "" {
		description = strings.TrimSpace(cfg.SiteDescription)
	}
	d.MetaDescription = description
	if d.IsHome {
		d.FullTitle = title
	} else {
		d.FullTitle = strings.TrimSpace(title + " - " + cfg.SiteTitle)
	}
	reqPath := c.r.URL.Path
	if reqPath == "" {
		reqPath = "/"
	}
	d.CanonicalURL = c.baseURL + reqPath
	d.OGLocale = ogLocales[cfg.Language]
	if d.OGLocale == "" {
		d.OGLocale = "en_US"
	}
	d.Favicon = c.absolute(cfg.Assets.Favicon)
	d.AppleTouchIcon = c.absolute(cfg.Assets.AppleTouchIcon)
	d.OGImage = c.ogImageFor(post, page)

	// Contextual head/footer injects: post views use the post inject, all
	// other front-end views use the page inject.
	if post != nil {
		d.HeadInject = template.HTML(cfg.HeadInjectPost)
		d.footerInject = cfg.FooterInjectPost
	} else {
		d.HeadInject = template.HTML(cfg.HeadInjectPage)
		d.footerInject = cfg.FooterInjectPage
	}

	d.Light = blog.Palette{Bg: cfg.Theme.BackgroundColor, Text: cfg.Theme.TextColor, Accent: cfg.Theme.AccentColor, Border: cfg.Theme.BorderColor, AccentBg: cfg.Theme.AccentBgColor}
	d.Dark = blog.Palette{Bg: cfg.Theme.BackgroundColorDk, Text: cfg.Theme.TextColorDk, Accent: cfg.Theme.AccentColorDk, Border: cfg.Theme.BorderColorDk, AccentBg: cfg.Theme.AccentBgColorDk}
	if id := c.query("theme_preview"); id != "" && c.isAdmin() {
		if th := s.site.ThemeByID(id); th != nil {
			d.ThemePreview = th
			d.Light, d.Dark = th.Light, th.Dark
			d.CSRFToken = c.csrfToken
		}
	}
	d.FontStack = template.CSS(FontStackCSS(cfg.Theme.FontStack))
	d.MonoFontStack = template.CSS(FontStackCSS("mono"))
	if css, err := os.ReadFile(filepath.Join(s.site.CSSDir, "custom.css")); err == nil {
		d.CustomCSS = template.CSS(css)
	}
	if info, err := fs.Stat(s.static, "assets/css/style.css"); err == nil {
		d.CSSVersion = strconv.FormatInt(info.ModTime().Unix(), 10)
	}
	if d.CSSVersion == "" || d.CSSVersion == "0" || d.CSSVersion == "-62135596800" {
		d.CSSVersion = s.site.Version
	}

	// Masthead.
	d.CustomNavOnly = cfg.CustomNavOnly
	if !cfg.CustomNavOnly {
		for _, p := range s.site.Pages(false) {
			if p.IncludeInNav {
				d.NavPages = append(d.NavPages, NavItem{Label: p.Title, URL: c.basePath + "/" + p.Slug, Current: d.CurrentPath == p.Slug})
			}
		}
	}
	for _, item := range parseCustomNav(cfg.CustomNav) {
		u := item.URL
		if u[0] != '/' {
			parsed, err := url.Parse(u)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				continue
			}
		}
		if parsed, err := url.Parse(u); err == nil && parsed.Scheme == "" {
			p := parsed.Path
			if c.basePath != "" {
				p = strings.TrimPrefix(p, c.basePath)
			}
			item.Current = strings.Trim(p, "/") == d.CurrentPath
		}
		d.CustomNav = append(d.CustomNav, item)
	}
	d.HomeCurrent = d.CurrentPath == ""
	if cfg.CustomNavOnly {
		d.ShowNav = len(d.CustomNav) > 0
	} else {
		d.ShowNav = len(d.NavPages) > 0 || len(d.CustomNav) > 0
	}
	d.SiteTitleTag = "h1"
	return d
}

// finalizeTitleTag decides whether the site title is the page's h1.
func (d *PublicPage) finalizeTitleTag() {
	if d.Post != nil || d.IsTag || d.Is404 || (d.Page != nil && blog.ContentHasH1(d.Page.Content)) {
		d.SiteTitleTag = "p"
	}
}

func (c *ctx) ogImageFor(post *blog.Post, page *blog.Page) string {
	feature := ""
	if post != nil {
		feature = post.FeatureImage()
	}
	if feature == "" && page != nil {
		feature = page.FeatureImage()
	}
	if feature != "" {
		return c.absolute(feature)
	}
	custom := strings.TrimSpace(c.cfg.Assets.OGImage)
	if custom != "" && custom != "/assets/images/og-image.png" {
		return c.absolute(custom)
	}
	if c.cfg.Assets.OGImagePreferred == "banner" {
		switch {
		case post != nil && post.Slug != "":
			return c.baseURL + "/og-image.php?type=post&slug=" + url.QueryEscape(post.Slug)
		case page != nil && page.Slug != "":
			return c.baseURL + "/og-image.php?type=page&slug=" + url.QueryEscape(page.Slug)
		}
		return c.baseURL + "/og-image.php?type=home"
	}
	return c.absolute(custom)
}

func parseCustomNav(raw string) []NavItem {
	var out []NavItem
	for _, line := range strings.Split(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(raw), "\n") {
		line = strings.TrimSpace(line)
		label, u, ok := strings.Cut(line, "|")
		if !ok {
			continue
		}
		label, u = strings.TrimSpace(label), strings.TrimSpace(u)
		if label == "" || u == "" {
			continue
		}
		out = append(out, NavItem{Label: label, URL: u})
	}
	return out
}

type customRoute struct{ Path, Target string }

var routePathRe = regexp.MustCompile(`^/[a-zA-Z0-9/_-]+$`)

func parseCustomRoutes(raw string) []customRoute {
	var out []customRoute
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p, target, ok := strings.Cut(line, "|")
		if !ok {
			continue
		}
		p, target = strings.TrimSpace(p), strings.TrimSpace(target)
		if p == "" || target == "" {
			continue
		}
		if p[0] != '/' {
			p = "/" + p
		}
		if p != "/" {
			p = strings.TrimRight(p, "/")
		}
		if (p != "/" && !routePathRe.MatchString(p)) || strings.Contains(p, "//") || strings.Contains(p, "..") || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, customRoute{p, target})
	}
	return out
}

func paginate(posts []*blog.Post, perPage, current int) ([]*blog.Post, int, int) {
	if perPage < 1 {
		perPage = 1
	}
	if current < 1 {
		current = 1
	}
	total := 1
	if len(posts) > 0 {
		total = (len(posts) + perPage - 1) / perPage
	}
	start := (current - 1) * perPage
	if start > len(posts) {
		start = len(posts)
	}
	end := start + perPage
	if end > len(posts) {
		end = len(posts)
	}
	return posts[start:end], total, current
}

func (c *ctx) postList(posts []*blog.Post, base string, extra url.Values) *PostList {
	page, _ := strconv.Atoi(c.r.URL.Query().Get("page"))
	paged, totalPages, current := paginate(posts, c.cfg.PostsPerPage, page)
	l := &PostList{Posts: paged, Layout: c.cfg.Theme.PostListLayout, CurrentPage: current, TotalPages: totalPages, TotalPosts: len(posts)}
	link := func(n int) string {
		// Keep extra params before page, as http_build_query does.
		enc := ""
		if len(extra) > 0 {
			enc = extra.Encode() + "&"
		}
		return base + "?" + enc + "page=" + strconv.Itoa(n)
	}
	if len(paged) > 0 && totalPages > 1 {
		if current > 1 {
			l.PrevURL = link(current - 1)
		}
		if current < totalPages {
			l.NextURL = link(current + 1)
		}
	}
	return l
}

// ---------------------------------------------------------------------------
// Front controller
// ---------------------------------------------------------------------------

// capture buffers a response so it can be cached.
type capture struct {
	header http.Header
	status int
	buf    bytes.Buffer
}

func (w *capture) Header() http.Header { return w.header }
func (w *capture) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.buf.Write(b)
}
func (w *capture) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}

func (s *Server) handleFront(w http.ResponseWriter, r *http.Request) {
	c := s.newCtx(w, r)
	if !s.site.Config.Installed() {
		c.redirect(c.basePath + "/setup")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		// Only the theme preview banner posts; everything else is GET.
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	reqPath := strings.Trim(r.URL.Path, "/")
	cacheKey := "/" + reqPath
	if r.URL.RawQuery != "" {
		cacheKey += "?" + r.URL.RawQuery
	}
	cacheable := c.cfg.Cache.Enabled && r.URL.Query()["q"] == nil && !c.isAdmin()
	if cacheable {
		if data, ok := s.site.CacheRead(cacheKey, 0, "html"); ok {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(data)
			return
		}
	}

	cw := &capture{header: w.Header()}
	c.w = cw
	s.front(c, reqPath)
	if cw.status == 0 {
		cw.status = http.StatusOK
	}
	if cacheable && cw.status == http.StatusOK && cw.buf.Len() > 0 && strings.HasPrefix(cw.header.Get("Content-Type"), "text/html") {
		s.site.CacheWrite(cacheKey, cw.buf.Bytes(), "html")
	}
	w.WriteHeader(cw.status)
	_, _ = w.Write(cw.buf.Bytes())
}

func (s *Server) front(c *ctx, reqPath string) {
	set, err := s.publicTemplates()
	if err != nil {
		log.Printf("public templates: %v", err)
		if set == nil {
			http.Error(c.w, "Template error", http.StatusInternalServerError)
			return
		}
	}
	cfg := c.cfg
	withSlash := "/" + reqPath

	if strings.HasSuffix(reqPath, ".md") {
		s.render404(c, set)
		return
	}

	for _, route := range parseCustomRoutes(cfg.CustomRoutes) {
		if route.Path != withSlash {
			continue
		}
		name := s.customRouteTemplate(route.Target)
		if name == "" {
			break
		}
		d := c.newPublicPage(nil, nil, cfg.SiteTitle, cfg.SiteDescription)
		d.IsAdmin = c.isAdmin()
		d.finalizeTitleTag()
		c.render(set, name, d, 0)
		return
	}

	homepageSlug := strings.TrimSpace(cfg.HomepageSlug)
	blogPageSlug := strings.TrimSpace(cfg.BlogPageSlug)
	feedHidden := blogPageSlug == "__hidden__"

	isTag := strings.HasPrefix(reqPath, "tag/")
	isSingle := !isTag && reqPath != "" && !strings.Contains(reqPath, ".") &&
		!strings.HasPrefix(reqPath, "admin/") && !strings.HasPrefix(reqPath, "assets/") &&
		!strings.HasPrefix(reqPath, "content/") && !strings.HasPrefix(reqPath, "config/")

	if isSingle && homepageSlug != "" && reqPath == homepageSlug {
		to := c.basePath + "/"
		if c.r.URL.RawQuery != "" {
			to += "?" + c.r.URL.RawQuery
		}
		http.Redirect(c.w, c.r, to, http.StatusFound)
		return
	}
	if !isTag && reqPath == "" && homepageSlug != "" {
		if home := s.site.PageBySlug(homepageSlug, true); home != nil {
			s.renderPage(c, set, home)
			return
		}
	}
	if !isTag && reqPath != "" && strings.Contains(reqPath, ".") {
		s.render404(c, set)
		return
	}
	if isSingle {
		if page := s.site.PageBySlug(reqPath, false); page != nil {
			s.renderPage(c, set, page)
			return
		}
		if post := s.site.PostBySlug(reqPath, false); post != nil {
			s.renderPost(c, set, post)
			return
		}
		s.render404(c, set)
		return
	}

	// List views: home or tag archive.
	var posts []*blog.Post
	tagParam := ""
	tagSlug := ""
	if isTag {
		tagParam = strings.TrimPrefix(reqPath, "tag/")
		tagSlug = blog.NormalizeTag(tagParam)
		if tagSlug != "" {
			for _, p := range s.site.Posts(false) {
				for _, tag := range p.Tags {
					if blog.NormalizeTag(tag) == tagSlug {
						posts = append(posts, p)
						break
					}
				}
			}
		}
	} else {
		posts = s.site.Posts(false)
	}
	title := cfg.SiteTitle
	if isTag && tagParam != "" {
		title = "Tag: " + tagParam
	}
	d := c.newPublicPage(nil, nil, title, "")
	d.MetaDescription = strings.TrimSpace(cfg.SiteDescription)
	d.IsTag = isTag
	d.TagName = tagParam
	d.TagSlug = tagSlug
	d.HideFeed = feedHidden
	base := c.basePath + "/"
	if isTag {
		base = c.basePath + "/tag/" + tagSlug
	}
	d.List = c.postList(posts, base, nil)
	d.IsAdmin = c.isAdmin()
	d.finalizeTitleTag()
	c.render(set, "index", d, 0)
}

func (s *Server) render404(c *ctx, set *template.Template) {
	d := c.newPublicPage(nil, nil, c.t.T("frontend.page_not_found"), "")
	d.Is404 = true
	d.finalizeTitleTag()
	c.render(set, "404", d, http.StatusNotFound)
}

func (s *Server) renderPost(c *ctx, set *template.Template, post *blog.Post) {
	d := c.newPublicPage(post, nil, post.Title, post.Description)
	d.IsAdmin = c.isAdmin()
	d.Previous, d.Next = s.site.Store.Adjacent(s.site.Loc(), post.Slug)
	d.Content = template.HTML(s.site.RenderMarkdown(post.Content, blog.RenderContext{PostTitle: post.Title}))
	if name := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(strings.TrimSpace(post.Layout()), ""); name != "" {
		if set.Lookup("layout:"+name) != nil {
			d.LayoutName = "layout:" + name
		}
	}
	d.finalizeTitleTag()
	c.render(set, "post", d, 0)
}

func (s *Server) renderPage(c *ctx, set *template.Template, page *blog.Page) {
	cfg := c.cfg
	d := c.newPublicPage(nil, page, page.Title, page.Description)
	d.IsAdmin = c.isAdmin()
	d.Content = template.HTML(s.site.RenderMarkdown(page.Content, blog.RenderContext{PageTitle: page.Title}))

	blogSlug := strings.TrimSpace(cfg.BlogPageSlug)
	if blogSlug != "" && blogSlug != "__hidden__" && page.Slug == blogSlug {
		base := c.basePath + "/" + page.Slug
		if cfg.HomepageSlug != "" && cfg.HomepageSlug == page.Slug {
			base = c.basePath + "/"
		}
		d.BlogFeed = c.postList(s.site.Posts(false), base, nil)
	}
	searchSlug := strings.TrimSpace(cfg.SearchPageSlug)
	if searchSlug != "" && page.Slug == searchSlug {
		d.Search = s.searchView(c, searchSlug)
		d.UseHTMX = true
	}
	d.finalizeTitleTag()
	c.render(set, "page", d, 0)
}

func (s *Server) searchView(c *ctx, searchSlug string) *SearchView {
	query := strings.TrimSpace(c.r.URL.Query().Get("q"))
	v := &SearchView{Action: c.basePath + "/" + searchSlug, Query: query}
	words := blog.QueryWords(query)
	var results []*blog.Post
	if query != "" {
		index, ok := s.site.LoadSearchIndex()
		if !ok {
			index = s.site.SearchIndex()
		}
		for _, e := range index {
			if !blog.MatchesQuery(e.SearchText(), words) || e.Slug == "" {
				continue
			}
			if e.IsPage {
				if p := s.site.PageBySlug(e.Slug, false); p != nil {
					results = append(results, &blog.Post{
						Title: p.Title, Slug: p.Slug, Description: p.Description, Content: p.Content,
						Status: p.Status, Tags: []string{}, Extra: p.Extra,
					})
				}
				continue
			}
			if p := s.site.PostBySlug(e.Slug, false); p != nil {
				results = append(results, p)
			}
		}
	}
	extra := url.Values{}
	if query != "" {
		extra.Set("q", query)
	}
	v.Results = c.postList(results, v.Action, extra)
	v.Total = len(results)
	return v
}

// ---------------------------------------------------------------------------
// Feed and sitemap
// ---------------------------------------------------------------------------

var invalidXMLRe = regexp.MustCompile("[^\u0009\u000a\u000d -퟿-�\U00010000-\U0010ffff]")

func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request) {
	c := s.newCtx(w, r)
	if !s.site.Config.Installed() {
		c.redirect(c.basePath + "/setup")
		return
	}
	cfg := c.cfg
	ttl := cfg.Cache.RSSTTL
	posts := s.site.Posts(false)
	if len(posts) > 10 {
		posts = posts[:10]
	}
	baseURL := strings.TrimRight(c.baseURL, "/")

	var newest int64
	for _, p := range posts {
		if p.Timestamp > newest {
			newest = p.Timestamp
		}
	}
	if newest == 0 {
		newest = time.Now().Unix()
	}
	sum := md5.Sum([]byte(strconv.FormatInt(newest, 10)))
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	lastMod := time.Unix(newest, 0).UTC()
	if inm := strings.TrimSpace(r.Header.Get("If-None-Match")); inm != "" && inm == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if ims := strings.TrimSpace(r.Header.Get("If-Modified-Since")); ims != "" {
		if t, err := http.ParseTime(ims); err == nil && t.Unix() >= newest {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	h := w.Header()
	h.Set("Content-Type", "application/rss+xml; charset=UTF-8")
	h.Set("Cache-Control", "public, max-age="+strconv.Itoa(ttl))
	h.Set("Last-Modified", lastMod.Format(http.TimeFormat))
	h.Set("Expires", time.Now().Add(time.Duration(ttl)*time.Second).UTC().Format(http.TimeFormat))
	h.Set("ETag", etag)

	if cfg.Cache.Enabled {
		if data, ok := s.site.CacheRead("__feed__", time.Duration(ttl)*time.Second, "xml"); ok {
			_, _ = w.Write(data)
			return
		}
	}

	esc := template.HTMLEscapeString
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:atom="http://www.w3.org/2005/Atom">` + "\n")
	b.WriteString("    <channel>\n")
	desc := cfg.SiteTagline
	if desc == "" {
		desc = cfg.SiteTitle
	}
	fmt.Fprintf(&b, "        <title>%s</title>\n", esc(cfg.SiteTitle))
	fmt.Fprintf(&b, "        <link>%s</link>\n", esc(baseURL))
	fmt.Fprintf(&b, "        <atom:link href=\"%s/feed\" rel=\"self\" type=\"application/rss+xml\"/>\n", esc(baseURL))
	fmt.Fprintf(&b, "        <description>%s</description>\n", esc(desc))
	fmt.Fprintf(&b, "        <language>%s</language>\n", esc(strings.ReplaceAll(cfg.Language, "_", "-")))
	loc := blog.Location(cfg)
	for _, p := range posts {
		postURL := baseURL + "/" + p.Slug
		pub := time.Now().In(loc)
		if t, ok := blog.ParsePostTime(p.Date, loc); ok {
			pub = t
		}
		content := s.site.RenderMarkdown(p.Content, blog.RenderContext{PostTitle: p.Title})
		content = absolutizeFeedHTML(content, baseURL, postURL)
		content = invalidXMLRe.ReplaceAllString(content, "")
		content = strings.ReplaceAll(content, "]]>", "]]]]><![CDATA[>")
		b.WriteString("            <item>\n")
		fmt.Fprintf(&b, "                <title>%s</title>\n", esc(p.Title))
		fmt.Fprintf(&b, "                <link>%s</link>\n", esc(postURL))
		fmt.Fprintf(&b, "                <guid>%s</guid>\n", esc(postURL))
		fmt.Fprintf(&b, "                <pubDate>%s</pubDate>\n", esc(pub.Format(time.RFC1123Z)))
		fmt.Fprintf(&b, "                <description><![CDATA[%s]]></description>\n", content)
		b.WriteString("            </item>\n")
	}
	b.WriteString("    </channel>\n</rss>\n")
	if cfg.Cache.Enabled {
		s.site.CacheWrite("__feed__", b.Bytes(), "xml")
	}
	_, _ = w.Write(b.Bytes())
}

var (
	preCodeRe  = regexp.MustCompile(`(?is)<pre[^>]*>.*?</pre>|<code[^>]*>.*?</code>`)
	relAttrRe  = regexp.MustCompile(`(?i)(href|src)="/([^"]*)"`)
	hashLinkRe = regexp.MustCompile(`href="#`)
)

// absolutizeFeedHTML turns root-relative URLs into absolute ones.
func absolutizeFeedHTML(html, baseURL, postURL string) string {
	if strings.TrimSpace(html) == "" {
		return html
	}
	var holders []string
	html = preCodeRe.ReplaceAllStringFunc(html, func(m string) string {
		holders = append(holders, m)
		return "\x00PH_" + strconv.Itoa(len(holders)-1) + "\x00"
	})
	u, _ := url.Parse(baseURL)
	basePath := strings.TrimRight(u.Path, "/")
	domain := u.Scheme + "://" + u.Host
	html = relAttrRe.ReplaceAllStringFunc(html, func(m string) string {
		sub := relAttrRe.FindStringSubmatch(m)
		p := "/" + sub[2]
		if basePath != "" && (strings.HasPrefix(p, basePath+"/") || p == basePath) {
			return sub[1] + `="` + domain + p + `"`
		}
		return sub[1] + `="` + baseURL + p + `"`
	})
	if postURL != "" {
		html = hashLinkRe.ReplaceAllString(html, `href="`+postURL+`#`)
	}
	for i, h := range holders {
		html = strings.Replace(html, "\x00PH_"+strconv.Itoa(i)+"\x00", h, 1)
	}
	return html
}

func (s *Server) handleSitemap(w http.ResponseWriter, r *http.Request) {
	c := s.newCtx(w, r)
	if !s.site.Config.Installed() {
		c.redirect(c.basePath + "/setup")
		return
	}
	esc := template.HTMLEscapeString
	loc := s.site.Loc()
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	fmt.Fprintf(&b, "<url><loc>%s/</loc><priority>1.0</priority></url>\n", esc(c.baseURL))
	for _, p := range s.site.Pages(false) {
		if strings.TrimSpace(p.Slug) == "" {
			continue
		}
		fmt.Fprintf(&b, "<url><loc>%s</loc><lastmod>%s</lastmod><priority>0.8</priority></url>\n",
			esc(c.baseURL+"/"+p.Slug), p.ModTime.In(loc).Format("2006-01-02"))
	}
	for _, p := range s.site.Posts(false) {
		if strings.TrimSpace(p.Slug) == "" {
			continue
		}
		lastmod := ""
		if p.Timestamp > 0 {
			lastmod = time.Unix(p.Timestamp, 0).In(loc).Format("2006-01-02")
		} else if len(p.Date) >= 10 {
			lastmod = p.Date[:10]
		}
		fmt.Fprintf(&b, "<url><loc>%s</loc><lastmod>%s</lastmod><priority>0.9</priority></url>\n", esc(c.baseURL+"/"+p.Slug), lastmod)
	}
	b.WriteString("</urlset>\n")
	w.Header().Set("Content-Type", "application/xml; charset=UTF-8")
	_, _ = w.Write(b.Bytes())
}

// ---------------------------------------------------------------------------
// Open Graph images
// ---------------------------------------------------------------------------

func (s *Server) handleOGImage(w http.ResponseWriter, r *http.Request) {
	c := s.newCtx(w, r)
	cfg := c.cfg
	q := r.URL.Query()
	typ := strings.TrimSpace(q.Get("type"))
	slug := strings.TrimSpace(q.Get("slug"))
	if slug == "" {
		slug = strings.TrimSpace(q.Get("amp;slug"))
	}

	var title string
	if slug != "" {
		var post *blog.Post
		var page *blog.Page
		if typ == "page" {
			if page = s.site.PageBySlug(slug, false); page == nil {
				post = s.site.PostBySlug(slug, false)
			}
		} else if post = s.site.PostBySlug(slug, false); post == nil {
			page = s.site.PageBySlug(slug, false)
		}
		switch {
		case post != nil && strings.TrimSpace(post.Title) != "":
			title = strings.TrimSpace(post.Title)
		case post != nil && post.Description != "":
			title = strings.TrimSpace(post.Description)
		case post != nil && post.Content != "":
			clean := strings.TrimSpace(blog.StripTags(s.site.RenderMarkdown(post.Content, blog.RenderContext{})))
			title = slug
			if clean != "" {
				r := []rune(clean)
				if len(r) > 120 {
					r = r[:120]
				}
				title = string(r)
			}
		case page != nil && strings.TrimSpace(page.Title) != "":
			title = strings.TrimSpace(page.Title)
		case page != nil && page.Description != "":
			title = strings.TrimSpace(page.Description)
		default:
			title = slug
		}
	} else if d := strings.TrimSpace(cfg.SiteDescription); d != "" {
		title = d
	} else {
		title = cfg.SiteTitle
		if title == "" {
			title = "PureBlog-Go"
		}
	}
	siteTitle := strings.TrimSpace(cfg.SiteTitle)
	if siteTitle == "" {
		siteTitle = "PureBlog-Go"
	}

	bg, fg := cfg.Theme.BackgroundColor, cfg.Theme.TextColor
	if cfg.Theme.ColorMode == "dark" {
		bg, fg = cfg.Theme.BackgroundColorDk, cfg.Theme.TextColorDk
	}
	if bg == "" {
		bg = "#f2f2f2"
		if cfg.Theme.ColorMode == "dark" {
			bg = "#1a1a1a"
		}
	}
	if fg == "" {
		fg = "#1a1a1a"
		if cfg.Theme.ColorMode == "dark" {
			fg = "#f2f2f2"
		}
	}
	fontFile := map[string]string{
		"mono":  "assets/fonts/iosevka/Iosevka-Bold.ttf",
		"serif": "assets/fonts/merriweather/Merriweather-Bold.ttf",
	}[cfg.Theme.FontStack]
	if fontFile == "" {
		fontFile = "assets/fonts/inter/Inter-Bold.ttf"
	}
	faviconPath := ""
	if fav := strings.TrimSpace(cfg.Assets.Favicon); strings.HasPrefix(fav, "/content/") {
		candidate := filepath.Join(s.site.Dir, filepath.FromSlash(fav))
		if blog.IsFile(candidate) && blog.WithinDir(s.site.ContentDir, candidate) {
			faviconPath = candidate
		}
	}

	sum := md5.Sum([]byte(strings.Join([]string{typ, slug, title, siteTitle, bg, fg, fontFile, faviconPath}, ":")))
	cacheFile := filepath.Join(s.site.CacheDir, "og", hex.EncodeToString(sum[:])+".png")
	if data, err := os.ReadFile(cacheFile); err == nil {
		writePNG(w, data)
		return
	}
	fontData, err := fs.ReadFile(s.static, fontFile)
	if err != nil {
		http.Error(w, "Error generating image.", http.StatusInternalServerError)
		return
	}
	data, err := og.Render(og.Options{
		Title: blog.DecodeEntities(title), SiteTitle: blog.DecodeEntities(siteTitle),
		Background: bg, Text: fg, Font: fontData, FaviconPath: faviconPath,
	})
	if err != nil {
		http.Error(w, "Error generating image.", http.StatusInternalServerError)
		return
	}
	if os.MkdirAll(filepath.Dir(cacheFile), 0o755) == nil {
		_ = os.WriteFile(cacheFile, data, 0o644)
	}
	writePNG(w, data)
}

func writePNG(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}

// ---------------------------------------------------------------------------
// Files under /content
// ---------------------------------------------------------------------------

// handleContentFile serves public files from the content directory. Only
// images, custom CSS, themes and the two index files are exposed.
func (s *Server) handleContentFile(w http.ResponseWriter, r *http.Request) {
	rel := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/content/"))
	rel = strings.TrimPrefix(rel, "/")
	allowed := rel == "tag-index.json" || rel == "search-index.json" ||
		strings.HasPrefix(rel, "images/") || strings.HasPrefix(rel, "css/") || strings.HasPrefix(rel, "themes/")
	lower := strings.ToLower(rel)
	if !allowed || strings.HasSuffix(lower, ".md") || strings.Contains(lower, ".ph") || strings.HasSuffix(lower, "/") {
		s.handleFront(w, r)
		return
	}
	full := filepath.Join(s.site.ContentDir, filepath.FromSlash(rel))
	info, err := os.Stat(full)
	if err != nil || info.IsDir() || !blog.WithinDir(s.site.ContentDir, full) {
		s.handleFront(w, r)
		return
	}
	if strings.HasPrefix(rel, "images/") {
		w.Header().Set("Cache-Control", "public, max-age=2592000")
	}
	http.ServeFile(w, r, full)
}
