package server

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jvanrhyn/pureblog-go/internal/auth"
	"github.com/jvanrhyn/pureblog-go/internal/blog"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTwoFactorLogin(t *testing.T) {
	c, site, done := newTestSite(t, t.TempDir())
	defer done()
	setupAndLogin(t, c)

	_, body := c.get("/admin/settings-user?setup_mfa=1")
	m := regexp.MustCompile(`user-select: all;">([A-Z2-7]{16})</code>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("MFA secret not shown")
	}
	expectContains(t, body, "<svg", "QR code")
	secret := m[1]
	tok := csrfRe.FindStringSubmatch(body)[1]
	resp, body := c.post("/admin/settings-user", url.Values{
		"csrf_token": {tok}, "mfa_action": {"confirm_setup"}, "mfa_code": {auth.TOTPCode(secret, time.Now())},
	})
	expectStatus(t, resp, http.StatusOK, "confirm MFA")
	var codes []string
	if block := regexp.MustCompile(`(?s)<pre[^>]*><code>(.*?)</code></pre>`).FindStringSubmatch(body); block != nil {
		codes = strings.Fields(block[1])
	}
	if !site.Config.Get().MFAEnabled || len(codes) < 8 {
		t.Fatalf("MFA not enabled or backup codes missing (%d)", len(codes))
	}

	c.post("/admin/logout", url.Values{"csrf_token": {tok}})
	tok = c.csrf("/admin")
	resp, body = c.post("/admin", url.Values{"csrf_token": {tok}, "username": {"admin"}, "password": {"secret"}, "remember_me": {"1"}})
	expectStatus(t, resp, http.StatusOK, "password step")
	expectContains(t, body, `name="mfa_code"`, "MFA prompt")
	tok = csrfRe.FindStringSubmatch(body)[1]
	resp, _ = c.post("/admin", url.Values{"csrf_token": {tok}, "mfa_code": {"000000"}})
	expectStatus(t, resp, http.StatusOK, "wrong code re-renders")
	resp, _ = c.post("/admin", url.Values{"csrf_token": {tok}, "mfa_code": {codes[0]}})
	expectStatus(t, resp, http.StatusSeeOther, "backup code accepted")
	if n := len(site.Config.Get().MFABackupCodes); n != 7 {
		t.Fatalf("used backup code not removed: %d left", n)
	}

	// The remember-me cookie restores the session after the session cookie is gone.
	u, _ := url.Parse(c.base)
	var remember *http.Cookie
	for _, ck := range c.http.Jar.Cookies(u) {
		if strings.HasPrefix(ck.Name, "pb_remember_") {
			remember = ck
		}
	}
	if remember == nil {
		t.Fatal("remember-me cookie not set")
	}
	req, _ := http.NewRequest(http.MethodGet, c.base+"/admin/dashboard", nil)
	req.AddCookie(remember)
	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	expectStatus(t, resp, http.StatusOK, "remember-me login")
}

func TestLoginLockout(t *testing.T) {
	c, _, done := newTestSite(t, t.TempDir())
	defer done()
	setupAndLogin(t, c)
	tok := c.csrf("/admin/dashboard")
	c.post("/admin/logout", url.Values{"csrf_token": {tok}})
	tok = c.csrf("/admin")
	var body string
	for i := 0; i < 5; i++ {
		_, body = c.post("/admin", url.Values{"csrf_token": {tok}, "username": {"admin"}, "password": {"nope"}})
	}
	expectContains(t, body, "disabled", "form disabled after lockout")
	resp, _ := c.post("/admin", url.Values{"csrf_token": {tok}, "username": {"admin"}, "password": {"secret"}})
	expectStatus(t, resp, http.StatusOK, "correct password rejected while locked out")
}

func TestSiteFeatures(t *testing.T) {
	dir := t.TempDir()
	c, site, done := newTestSite(t, dir)
	defer done()
	setupAndLogin(t, c)
	posts := filepath.Join(dir, "content", "posts")

	// A scheduled post in the past is published by the scheduler.
	writeFile(t, filepath.Join(posts, "2020-01-01-later.md"),
		"---\ntitle: Later\nslug: later\ndate: 2020-01-01 09:00\nstatus: scheduled\ntags: []\ndescription: \nmood: happy\n---\n\nScheduled body\n")
	if n := site.PublishScheduled(); n != 1 {
		t.Fatalf("PublishScheduled = %d", n)
	}
	raw, _ := os.ReadFile(filepath.Join(posts, "2020-01-01-later.md"))
	expectContains(t, string(raw), "status: published", "scheduled post published")
	expectContains(t, string(raw), "mood: happy", "custom fields kept when publishing")

	// Custom layout with a field definition.
	writeFile(t, filepath.Join(dir, "content", "layouts", "photo.html"),
		`<article class="photo"><h1>{{.Post.Title}}</h1><p class="mood">{{field .Post "mood"}}</p>{{.Content}}{{postMeta .}}</article>`)
	writeFile(t, filepath.Join(dir, "content", "layouts", "photo.json"),
		`{"label": "Photo", "fields": [{"name": "mood", "label": "Mood", "type": "select", "options": ["happy", "sad"]}]}`)
	writeFile(t, filepath.Join(posts, "2021-05-05-snap.md"),
		"---\ntitle: Snap\nslug: snap\ndate: 2021-05-05 10:00\nstatus: published\ntags: [Photos]\ndescription: \nlayout: photo\nmood: sad\n---\n\nA photo\n")
	_, body := c.get("/snap")
	expectContains(t, body, `<article class="photo"><h1>Snap</h1><p class="mood">sad</p>`, "custom layout")
	expectContains(t, body, "Previous post", "postMeta in layout")
	_, body = c.get("/admin/edit-post?slug=snap")
	expectContains(t, body, `name="layout_field__mood"`, "layout field in editor")
	expectContains(t, body, `<option value="sad" selected>`, "layout field value")
	_, body = c.get("/admin/dashboard")
	expectContains(t, body, "js-open-layout-picker", "layout picker offered")

	// Override the footer template and add a custom route.
	writeFile(t, filepath.Join(dir, "content", "includes", "footer.html"),
		`{{define "footer"}}<footer><p>Custom footer {{.SiteTitle}}</p></footer>{{end}}`)
	writeFile(t, filepath.Join(dir, "content", "includes", "archive.html"),
		`{{template "header" .}}{{template "masthead" .}}<main><h1>Archive</h1></main>{{footer .}}</body></html>`)
	cfg := site.Config.Get()
	cfg.CustomRoutes = "/archive | archive.php\n/bad | ../../etc/passwd"
	cfg.FooterInjectPage = "<script>/*page inject*/</script>"
	cfg.CustomNav = "GitHub | https://github.com\nEvil | javascript:alert(1)\nArchive | /archive"
	_ = site.Config.Save(cfg)
	resp, body := c.get("/archive")
	expectStatus(t, resp, http.StatusOK, "custom route")
	expectContains(t, body, "<h1>Archive</h1>", "custom route template")
	expectContains(t, body, "Custom footer", "footer override")
	expectContains(t, body, "/*page inject*/</script>\n</footer>", "footer inject placed before </footer>")
	expectContains(t, body, `href="/archive" class="current"`, "custom nav current")
	if strings.Contains(body, "javascript:") {
		t.Fatal("javascript: nav link rendered")
	}
	resp, _ = c.get("/bad")
	expectStatus(t, resp, http.StatusNotFound, "unsafe custom route ignored")

	// Homepage page + blog page.
	tok := c.csrf("/admin/edit-page?action=new")
	c.post("/admin/edit-page", url.Values{"csrf_token": {tok}, "title": {"Welcome"}, "status": {"published"}, "include_in_nav": {"no"}, "content": {"# Welcome home"}})
	c.post("/admin/edit-page", url.Values{"csrf_token": {tok}, "title": {"Blog"}, "status": {"published"}, "include_in_nav": {"yes"}, "content": {"All posts:"}})
	cfg = site.Config.Get()
	cfg.HomepageSlug, cfg.BlogPageSlug = "welcome", "blog"
	_ = site.Config.Save(cfg)
	site.CacheClear()
	_, body = c.get("/")
	expectContains(t, body, `<h1>Welcome home</h1>`, "homepage page")
	expectContains(t, body, `<p class="site-title">`, "site title demoted when page has h1")
	resp, _ = c.get("/welcome")
	expectStatus(t, resp, http.StatusFound, "homepage slug redirects to /")
	_, body = c.get("/blog")
	expectContains(t, body, `class="blog-feed"`, "blog page feed")
	expectContains(t, body, "Snap", "blog page lists posts")

	// Page cache: anonymous views are cached, admin views bypass it.
	anon := &testClient{t: t, base: c.base, http: &http.Client{}}
	site.CacheClear()
	_, first := anon.get("/snap")
	_, second := anon.get("/snap")
	if strings.Contains(first, "Cached at") || !strings.Contains(second, "<!-- Cached at") {
		t.Fatal("anonymous page not served from cache on second view")
	}
	if strings.Contains(second, "admin-edit-link") {
		t.Fatal("cached page contains admin-only markup")
	}
	_, adminView := c.get("/snap")
	if strings.Contains(adminView, "Cached at") || !strings.Contains(adminView, "admin-edit-link") {
		t.Fatal("admin view served from cache")
	}

	// Theme preview banner for admins only.
	themeID := site.Themes()[0].ID
	_, body = c.get("/?theme_preview=" + url.QueryEscape(themeID))
	expectContains(t, body, "theme-preview-banner", "theme preview banner")
	_, body = anon.get("/?theme_preview=" + url.QueryEscape(themeID))
	if strings.Contains(body, "theme-preview-banner") {
		t.Fatal("theme preview shown to anonymous visitor")
	}

	// Data loops from data/*.yml.
	writeFile(t, filepath.Join(dir, "data", "links.yml"), "- name: Go\n  url: https://go.dev\n  feed: true\n- name: \"<Rust>\"\n  url: https://rust-lang.org\n")
	tok = c.csrf("/admin/edit-page?action=new")
	c.post("/admin/edit-page", url.Values{"csrf_token": {tok}, "title": {"Links"}, "status": {"published"},
		"content": {"{% for site in site.data.links %}<p>{{ site.name }}{% if site.feed %} (feed){% endif %}</p>\n{% endfor %}"}})
	_, body = c.get("/links")
	expectContains(t, body, "<p>Go (feed)</p>", "data loop with condition")
	expectContains(t, body, "<p>&lt;Rust&gt;</p>", "data loop escapes values")
}

func TestBasePath(t *testing.T) {
	dir := t.TempDir()
	c, site, done := newTestSite(t, dir)
	defer done()
	setupAndLogin(t, c)
	cfg := site.Config.Get()
	cfg.BaseURL = c.base + "/blog"
	_ = site.Config.Save(cfg)

	// The session cookie name depends on the base URL, so log in again.
	tok := c.csrf("/blog/admin")
	resp, _ := c.post("/blog/admin", url.Values{"csrf_token": {tok}, "username": {"admin"}, "password": {"secret"}})
	expectStatus(t, resp, http.StatusSeeOther, "login under base path")
	if loc := resp.Header.Get("Location"); loc != "/blog/admin/dashboard" {
		t.Fatalf("redirect %q", loc)
	}
	resp, body := c.get("/blog/about")
	expectStatus(t, resp, http.StatusOK, "page under base path")
	expectContains(t, body, `href="`+c.base+`/blog/assets/css/style.css`, "asset URL has base path")
	expectContains(t, body, `<a href="/blog/">`, "home link has base path")
	resp, _ = c.get("/blog/assets/css/style.css")
	expectStatus(t, resp, http.StatusOK, "asset under base path")
	_, body = c.get("/blog/feed")
	expectContains(t, body, "<link>"+c.base+"/blog</link>", "feed link")
	if !blog.IsFile(filepath.Join(dir, "config", "config.json")) {
		t.Fatal("config missing")
	}
}
