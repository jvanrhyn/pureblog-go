package server

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/johanvanrhyn/pureblog-go/internal/auth"
	"github.com/johanvanrhyn/pureblog-go/internal/blog"
	"github.com/johanvanrhyn/pureblog-go/web"
)

type testClient struct {
	t    *testing.T
	base string
	http *http.Client
}

func newTestSite(t *testing.T, dir string) (*testClient, *blog.Site, func()) {
	t.Helper()
	site, err := blog.Open(dir, web.FS, "v0.0.0-test")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(site, web.FS, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	jar, _ := cookiejar.New(nil)
	c := &testClient{t: t, base: ts.URL, http: &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
	return c, site, ts.Close
}

func (c *testClient) do(req *http.Request) (*http.Response, string) {
	c.t.Helper()
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func (c *testClient) get(path string) (*http.Response, string) {
	req, _ := http.NewRequest(http.MethodGet, c.base+path, nil)
	return c.do(req)
}

func (c *testClient) post(path string, form url.Values, headers ...string) (*http.Response, string) {
	req, _ := http.NewRequest(http.MethodPost, c.base+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return c.do(req)
}

var csrfRe = regexp.MustCompile(`name="csrf_token" value="([0-9a-f]+)"`)

func (c *testClient) csrf(path string) string {
	c.t.Helper()
	_, body := c.get(path)
	m := csrfRe.FindStringSubmatch(body)
	if m == nil {
		c.t.Fatalf("no CSRF token on %s", path)
	}
	return m[1]
}

func expectStatus(t *testing.T, resp *http.Response, want int, what string) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("%s: status %d, want %d", what, resp.StatusCode, want)
	}
}

func expectContains(t *testing.T, body, want, what string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Fatalf("%s: body does not contain %q\n---\n%s", what, want, truncate(body))
	}
}

func truncate(s string) string {
	if len(s) > 3000 {
		return s[:3000] + "..."
	}
	return s
}

func setupAndLogin(t *testing.T, c *testClient) {
	t.Helper()
	resp, _ := c.get("/")
	expectStatus(t, resp, http.StatusSeeOther, "redirect to setup")
	tok := c.csrf("/setup")
	resp, body := c.post("/setup", url.Values{
		"csrf_token": {tok}, "language": {"en"}, "site_title": {"Test Blog"}, "site_tagline": {"Hello"},
		"base_url": {c.base}, "admin_username": {"admin"}, "password": {"secret"}, "confirm_password": {"secret"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("setup failed: %d\n%s", resp.StatusCode, truncate(body))
	}
	tok = c.csrf("/admin")
	resp, _ = c.post("/admin", url.Values{"csrf_token": {tok}, "username": {"admin"}, "password": {"wrong"}})
	expectStatus(t, resp, http.StatusOK, "bad login re-renders")
	resp, _ = c.post("/admin", url.Values{"csrf_token": {tok}, "username": {"admin"}, "password": {"secret"}})
	expectStatus(t, resp, http.StatusSeeOther, "login")
	if loc := resp.Header.Get("Location"); !strings.HasSuffix(loc, "/admin/dashboard") {
		t.Fatalf("login redirected to %q", loc)
	}
}

func TestEndToEnd(t *testing.T) {
	dir := t.TempDir()
	c, site, done := newTestSite(t, dir)
	defer done()
	setupAndLogin(t, c)

	// Every admin page renders.
	for _, p := range []string{"/admin/dashboard", "/admin/content", "/admin/content?tab=pages", "/admin/edit-post?action=new",
		"/admin/edit-page?slug=demo", "/admin/images", "/admin/settings-site", "/admin/settings-theme",
		"/admin/settings-css", "/admin/settings-user", "/admin/settings-updates", "/admin/settings-user?setup_mfa=1"} {
		resp, body := c.get(p)
		expectStatus(t, resp, http.StatusOK, p)
		expectContains(t, body, "</html>", p)
	}
	// Legacy PHP admin URLs still work.
	resp, _ := c.get("/admin/content.php?tab=pages")
	expectStatus(t, resp, http.StatusOK, "legacy admin URL")

	// Create a post.
	tok := c.csrf("/admin/edit-post?action=new")
	content := "Intro with a [link](https://golang.org).\n\n<!--more-->\n\nMore text {.notice}\n\nEmail {{ site_email }} and `{{ site_email }}`."
	resp, body := c.post("/admin/edit-post", url.Values{
		"csrf_token": {tok}, "title": {"Hello Wörld: First post"}, "status": {"published"},
		"tags": {"Go, Web Dev, 📚 Books"}, "content": {content},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create post: %d\n%s", resp.StatusCode, truncate(body))
	}
	expectContains(t, resp.Header.Get("Location"), "slug=hello-woerld-first-post", "post slug")
	files, _ := filepath.Glob(filepath.Join(dir, "content", "posts", "*-hello-woerld-first-post.md"))
	if len(files) != 1 {
		t.Fatalf("post file not written: %v", files)
	}
	raw, _ := os.ReadFile(files[0])
	expectContains(t, string(raw), "tags: [Go, Web Dev, 📚 Books]", "front matter")
	expectContains(t, string(raw), "title: Hello Wörld: First post", "front matter title")

	// Public rendering.
	resp, body = c.get("/hello-woerld-first-post")
	expectStatus(t, resp, http.StatusOK, "post page")
	expectContains(t, body, `<p class="notice">More text</p>`, "paragraph attributes")
	expectContains(t, body, `rel="noopener noreferrer"`, "external link rel")
	expectContains(t, body, `<code>{{ site_email }}</code>`, "shortcodes skip inline code")
	expectContains(t, body, `href="`+"/tag/web-dev"+`"`, "tag link")
	expectContains(t, body, `class="admin-edit-link"`, "edit link for admin")
	expectContains(t, body, `<p class="site-title">`, "site title demoted on post pages")

	resp, body = c.get("/tag/web-dev")
	expectStatus(t, resp, http.StatusOK, "tag page")
	expectContains(t, body, "Hello Wörld: First post", "tag listing")
	resp, body = c.get("/tag/" + url.PathEscape("📚-books"))
	expectContains(t, body, "Hello Wörld: First post", "emoji tag listing")

	_, body = c.get("/")
	expectContains(t, body, `<p class="post-excerpt">Intro with a link.</p>`, "home excerpt")

	resp, body = c.get("/feed")
	expectStatus(t, resp, http.StatusOK, "feed")
	expectContains(t, body, "<title>Hello Wörld: First post</title>", "feed item")
	etag := resp.Header.Get("ETag")
	req, _ := http.NewRequest(http.MethodGet, c.base+"/feed", nil)
	req.Header.Set("If-None-Match", etag)
	resp, _ = c.do(req)
	expectStatus(t, resp, http.StatusNotModified, "feed conditional GET")

	_, body = c.get("/sitemap.xml")
	expectContains(t, body, "/hello-woerld-first-post</loc>", "sitemap")

	_, body = c.get("/search?q=wörld+first")
	expectContains(t, body, "1 result", "search")
	_, body = c.get("/search?q=nothing-matches-this")
	expectContains(t, body, "nothing-matches-this", "empty search")

	resp, body = c.get("/og-image.php?type=post&slug=hello-woerld-first-post")
	expectStatus(t, resp, http.StatusOK, "og image")
	if !strings.HasPrefix(body, "\x89PNG") {
		t.Fatal("og image is not a PNG")
	}

	// Autosave through htmx.
	resp, body = c.post("/admin/autosave", url.Values{
		"csrf_token": {tok}, "editor_type": {"post"}, "action": {"save"}, "slug": {"hello-woerld-first-post"},
		"title": {"Draft title"}, "content": {"draft body"},
	}, "HX-Request", "true")
	expectStatus(t, resp, http.StatusOK, "autosave")
	expectContains(t, body, "Autosaved", "autosave status")
	_, body = c.get("/admin/edit-post?slug=hello-woerld-first-post")
	expectContains(t, body, `"draft body"`, "autosave offered for restore")

	// Autosave without a CSRF token is rejected.
	resp, _ = c.post("/admin/autosave", url.Values{"editor_type": {"post"}, "slug": {"x"}})
	expectStatus(t, resp, http.StatusForbidden, "autosave without CSRF")

	// Upload an image.
	var img bytes.Buffer
	m := image.NewRGBA(image.Rect(0, 0, 4, 4))
	m.Set(1, 1, color.RGBA{255, 0, 0, 255})
	_ = png.Encode(&img, m)
	var mp bytes.Buffer
	w := multipart.NewWriter(&mp)
	_ = w.WriteField("csrf_token", tok)
	_ = w.WriteField("slug", "hello-woerld-first-post")
	fw, _ := w.CreateFormFile("image", "My Photo.png")
	_, _ = fw.Write(img.Bytes())
	w.Close()
	req, _ = http.NewRequest(http.MethodPost, c.base+"/admin/upload-image", &mp)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, _ = c.do(req)
	expectStatus(t, resp, http.StatusSeeOther, "upload")
	expectContains(t, resp.Header.Get("Location"), "uploaded=", "upload success")
	if !blog.IsFile(filepath.Join(dir, "content", "images", "hello-woerld-first-post", "My-Photo.png")) {
		t.Fatal("uploaded image missing")
	}

	// Set it as the feature image via htmx.
	resp, body = c.post("/admin/set-feature-image", url.Values{
		"editor_type": {"post"}, "slug": {"hello-woerld-first-post"}, "filename": {"My-Photo.png"},
	}, "HX-Request", "true", "X-CSRF-Token", tok)
	expectStatus(t, resp, http.StatusOK, "feature image")
	expectContains(t, body, `class="is-feature"`, "feature image marked")
	expectContains(t, body, `hx-swap-oob="true"`, "hidden field updated out of band")
	raw, _ = os.ReadFile(files[0])
	expectContains(t, string(raw), "feature_image: /content/images/hello-woerld-first-post/My-Photo.png", "feature image stored")
	_, body = c.get("/hello-woerld-first-post")
	expectContains(t, body, `og:image" content="`+c.base+`/content/images/hello-woerld-first-post/My-Photo.png"`, "og:image uses feature image")

	// Rename the post: images move and references are rewritten.
	resp, _ = c.post("/admin/edit-post", url.Values{
		"csrf_token": {tok}, "title": {"Hello Wörld: First post"}, "slug": {"renamed"}, "status": {"published"},
		"original_slug": {"hello-woerld-first-post"}, "original_status": {"published"},
		"date": {"2025-06-01 10:00"}, "tags": {"Go"}, "content": {"![x](/content/images/hello-woerld-first-post/My-Photo.png)"},
		"feature_image": {"/content/images/hello-woerld-first-post/My-Photo.png"},
	})
	expectStatus(t, resp, http.StatusSeeOther, "rename post")
	if !blog.IsFile(filepath.Join(dir, "content", "posts", "2025-06-01-renamed.md")) {
		t.Fatal("renamed post file missing")
	}
	if len(mustGlob(filepath.Join(dir, "content", "posts", "*hello-woerld*"))) != 0 {
		t.Fatal("old post file not removed")
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "content", "posts", "2025-06-01-renamed.md"))
	expectContains(t, string(raw), "/content/images/renamed/My-Photo.png", "image references rewritten")
	if !blog.IsFile(filepath.Join(dir, "content", "images", "renamed", "My-Photo.png")) {
		t.Fatal("image folder not renamed")
	}

	// Content list filters (htmx partial request returns the full page for hx-select).
	_, body = c.get("/admin/content?tab=posts&tag=go")
	expectContains(t, body, "Hello Wörld: First post", "filter by tag")
	_, body = c.get("/admin/content?tab=posts&status=draft")
	expectContains(t, body, "No posts found for Draft.", "filter by status")

	// Preview an unsaved draft.
	resp, _ = c.post("/admin/preview", url.Values{"csrf_token": {tok}, "editor_type": {"post"}, "markdown": {"# Preview heading"}, "title": {"Preview"}})
	expectStatus(t, resp, http.StatusSeeOther, "preview post")
	_, body = c.get("/admin/preview?type=post")
	expectContains(t, body, "Preview heading", "preview renders snapshot")

	// Settings: change the date format and title.
	stok := c.csrf("/admin/settings-site")
	form := url.Values{"csrf_token": {stok}, "site_title": {"Renamed Blog"}, "posts_per_page": {"5"}, "timezone": {"Europe/Amsterdam"},
		"date_format": {"j F Y"}, "language": {"en"}, "search_page_slug": {"search"}, "og_image_preferred": {"banner"},
		"cache_enabled": {"on"}, "search_include_pages": {"on"}, "base_url": {c.base}}
	resp, body = c.post("/admin/settings-site", form)
	expectStatus(t, resp, http.StatusOK, "save site settings")
	expectContains(t, body, "Settings updated", "site settings notice")
	if cfg := site.Config.Get(); cfg.SiteTitle != "Renamed Blog" || cfg.PostsPerPage != 5 {
		t.Fatalf("settings not saved: %+v", cfg)
	}
	_, body = c.get("/renamed")
	expectContains(t, body, "1 June 2025", "custom date format")

	// Theme: apply a bundled theme.
	themes := site.Themes()
	if len(themes) < 2 {
		t.Fatalf("expected bundled themes, got %d", len(themes))
	}
	ttok := c.csrf("/admin/settings-theme")
	resp, _ = c.post("/admin/settings-theme", url.Values{"csrf_token": {ttok}, "apply_theme_id": {themes[1].ID}})
	expectStatus(t, resp, http.StatusOK, "apply theme")
	if site.Config.Get().Theme.ActiveThemeName != themes[1].Name {
		t.Fatal("theme not applied")
	}

	// Clear the cache via htmx.
	resp, body = c.post("/admin/clear-cache", url.Values{}, "HX-Request", "true", "X-CSRF-Token", tok)
	expectStatus(t, resp, http.StatusOK, "clear cache")
	expectContains(t, body, `class="notice"`, "cache cleared toast")

	// Delete the post.
	resp, _ = c.post("/admin/delete-content", url.Values{"csrf_token": {tok}, "type": {"post"}, "slug": {"renamed"}})
	expectStatus(t, resp, http.StatusSeeOther, "delete post")
	if blog.IsFile(filepath.Join(dir, "content", "posts", "2025-06-01-renamed.md")) {
		t.Fatal("post not deleted")
	}
	resp, _ = c.get("/renamed")
	expectStatus(t, resp, http.StatusNotFound, "deleted post 404")

	// Sensitive files are not served.
	for _, p := range []string{"/content/pages/about.md", "/config/config.json", "/content/autosaves/post-x.json", "/data/remember-me.json"} {
		resp, _ = c.get(p)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s served with status %d", p, resp.StatusCode)
		}
	}
	resp, _ = c.get("/content/images/demo/goose.jpg")
	expectStatus(t, resp, http.StatusOK, "content image")

	// Logout.
	resp, _ = c.post("/admin/logout", url.Values{"csrf_token": {tok}})
	expectStatus(t, resp, http.StatusSeeOther, "logout")
	resp, _ = c.get("/admin/dashboard")
	expectStatus(t, resp, http.StatusSeeOther, "admin requires login after logout")
}

// bcryptHash returns a hash in PHP's $2y$ format.
func bcryptHash(password string) (string, error) {
	h, err := auth.HashPassword(password)
	return "$2y" + h[3:], err
}

func mustGlob(p string) []string {
	m, _ := filepath.Glob(p)
	return m
}

// A PHP config.php is imported on first start, keeping the password hash.
func TestImportPHPConfig(t *testing.T) {
	dir := t.TempDir()
	realHash, _ := bcryptHash("hunter2")
	php := `<?php
return array (
  'site_title' => 'Kev\'s Blog',
  'site_tagline' => '',
  'posts_per_page' => 7,
  'search_include_pages' => false,
  'admin_username' => 'kev',
  'admin_password_hash' => '` + realHash + `',
  'mfa_backup_codes' =>
  array (
  ),
  'cache' =>
  array (
    'enabled' => true,
    'rss_ttl' => 600,
  ),
  'theme' =>
  array (
    'font_stack' => 'serif',
  ),
  'enable_blog_posts' => true,
);
`
	_ = os.MkdirAll(filepath.Join(dir, "config"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, "config", "config.php"), []byte(php), 0o644); err != nil {
		t.Fatal(err)
	}
	c, site, done := newTestSite(t, dir)
	defer done()
	cfg := site.Config.Get()
	if cfg.SiteTitle != "Kev's Blog" || cfg.PostsPerPage != 7 || cfg.SearchIncludePages || cfg.Theme.FontStack != "serif" || cfg.Cache.RSSTTL != 600 {
		t.Fatalf("import mismatch: %+v", cfg)
	}
	if cfg.Theme.TextColor != "#212121" {
		t.Fatalf("defaults not applied for missing keys: %q", cfg.Theme.TextColor)
	}
	if !blog.IsFile(filepath.Join(dir, "config", "config.json")) {
		t.Fatal("config.json not written")
	}
	tok := c.csrf("/admin")
	resp, _ := c.post("/admin", url.Values{"csrf_token": {tok}, "username": {"kev"}, "password": {"hunter2"}})
	expectStatus(t, resp, http.StatusSeeOther, "login with imported PHP hash")
}
