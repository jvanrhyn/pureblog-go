// Package server implements PureBlog-Go's HTTP interface: the public site,
// feeds, the setup wizard and the HTMX-powered admin area.
package server

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/johanvanrhyn/pureblog-go/internal/auth"
	"github.com/johanvanrhyn/pureblog-go/internal/blog"
	"github.com/johanvanrhyn/pureblog-go/internal/config"
	"github.com/johanvanrhyn/pureblog-go/internal/i18n"
)

// Options configure the server.
type Options struct {
	// ReleaseRepo is the GitHub "owner/repo" checked for new releases.
	// Empty disables update checks.
	ReleaseRepo string
	// TrustProxy uses X-Forwarded-For / X-Forwarded-Proto headers.
	TrustProxy bool
	// MaxUploadMB limits request bodies for uploads.
	MaxUploadMB int64
}

// Server serves a PureBlog-Go site.
type Server struct {
	site     *blog.Site
	sessions *auth.Manager
	opts     Options
	embedded fs.FS
	static   fs.FS
	mux      *http.ServeMux

	tmplMu     sync.Mutex
	publicSet  *template.Template
	publicSig  string
	adminSet   *template.Template
	setupSets  map[string]*template.Template
	sprite     template.HTML
	adminIcons template.HTML

	releaseMu    sync.Mutex
	releaseCache *releaseInfo
}

// New creates a server for site. embedded is the web package filesystem.
func New(site *blog.Site, embedded fs.FS, opts Options) (*Server, error) {
	static, err := fs.Sub(embedded, "static")
	if err != nil {
		return nil, err
	}
	if opts.MaxUploadMB <= 0 {
		opts.MaxUploadMB = 20
	}
	s := &Server{
		site:      site,
		sessions:  auth.NewManager(site.DataDir),
		opts:      opts,
		embedded:  embedded,
		static:    static,
		mux:       http.NewServeMux(),
		setupSets: map[string]*template.Template{},
	}
	if b, err := fs.ReadFile(static, "assets/icons/sprite.svg"); err == nil {
		s.sprite = template.HTML(b)
	}
	if b, err := fs.ReadFile(static, "admin/icons/sprite.svg"); err == nil {
		s.adminIcons = template.HTML(b)
	}
	if s.adminSet, err = s.buildAdminTemplates(nil); err != nil {
		return nil, err
	}
	if _, err := s.publicTemplates(); err != nil {
		return nil, err
	}
	s.routes()
	return s, nil
}

// StartScheduler publishes scheduled posts once a minute until ctx ends.
func (s *Server) StartScheduler(ctx context.Context) {
	go func() {
		s.site.PublishScheduled()
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n := s.site.PublishScheduled(); n > 0 {
					log.Printf("published %d scheduled post(s)", n)
				}
			}
		}
	}()
}

func (s *Server) routes() {
	m := s.mux
	// Static assets bundled in the binary.
	m.Handle("GET /assets/", s.staticHandler("assets"))
	m.Handle("GET /admin/css/", s.staticHandler("admin/css"))
	m.Handle("GET /admin/js/", s.staticHandler("admin/js"))
	m.Handle("GET /admin/icons/", s.staticHandler("admin/icons"))
	m.Handle("GET /admin/images/", s.staticHandler("admin/images"))
	m.HandleFunc("GET /content/", s.handleContentFile)

	// Setup, feeds and generated files.
	m.HandleFunc("/setup", s.handleSetup)
	m.HandleFunc("GET /feed", s.handleFeed)
	m.HandleFunc("GET /feed/", s.handleFeed)
	m.HandleFunc("GET /sitemap.xml", s.handleSitemap)
	m.HandleFunc("GET /og-image", s.handleOGImage)

	// Admin.
	m.HandleFunc("/admin", s.handleLogin)
	m.HandleFunc("/admin/", s.handleAdminFallback)
	m.HandleFunc("/admin/index", s.handleLogin)
	m.HandleFunc("POST /admin/logout", s.admin(s.handleLogout))
	m.HandleFunc("GET /admin/logout", s.admin(func(c *ctx) { c.redirect(c.adminLanding()) }))
	m.HandleFunc("GET /admin/dashboard", s.admin(s.handleDashboard))
	m.HandleFunc("GET /admin/content", s.admin(s.handleContent))
	m.HandleFunc("/admin/edit-post", s.admin(s.handleEditPost))
	m.HandleFunc("/admin/edit-page", s.admin(s.handleEditPage))
	m.HandleFunc("/admin/delete-content", s.admin(s.handleDeleteContent))
	m.HandleFunc("/admin/preview", s.admin(s.handlePreview))
	m.HandleFunc("POST /admin/autosave", s.admin(s.handleAutosave))
	m.HandleFunc("POST /admin/upload-image", s.admin(s.handleUploadImage))
	m.HandleFunc("POST /admin/delete-image", s.admin(s.handleDeleteImage))
	m.HandleFunc("POST /admin/delete-image-library", s.admin(s.handleDeleteImageLibrary))
	m.HandleFunc("POST /admin/set-feature-image", s.admin(s.handleSetFeatureImage))
	m.HandleFunc("GET /admin/images", s.admin(s.handleImages))
	m.HandleFunc("POST /admin/clear-cache", s.admin(s.handleClearCache))
	m.HandleFunc("POST /admin/search-page", s.admin(s.handleSearchPageNotice))
	m.HandleFunc("GET /admin/settings", s.admin(func(c *ctx) { c.redirect(c.basePath + "/admin/settings-site") }))
	m.HandleFunc("/admin/settings-site", s.admin(s.handleSettingsSite))
	m.HandleFunc("/admin/settings-theme", s.admin(s.handleSettingsTheme))
	m.HandleFunc("/admin/settings-css", s.admin(s.handleSettingsCSS))
	m.HandleFunc("/admin/settings-user", s.admin(s.handleSettingsUser))
	m.HandleFunc("/admin/settings-updates", s.admin(s.handleSettingsUpdates))

	// Everything else goes through the front controller.
	m.HandleFunc("/", s.handleFront)
}

var legacyPaths = map[string]string{
	"/index.php":       "/",
	"/setup.php":       "/setup",
	"/feed.php":        "/feed",
	"/feed/index.php":  "/feed",
	"/sitemap.php":     "/sitemap.xml",
	"/og-image.php":    "/og-image",
	"/admin/index.php": "/admin",
}

var adminPHPRe = regexp.MustCompile(`^/admin/([a-z-]+)\.php$`)

// ServeHTTP strips the base path, maps legacy PHP URLs to their Go routes
// and recovers from panics.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("panic serving %s: %v\n%s", r.URL.Path, rec, debug.Stack())
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}()
	if bp := s.site.BasePath(); bp != "" {
		if r.URL.Path == bp {
			r.URL.Path = "/"
		} else if strings.HasPrefix(r.URL.Path, bp+"/") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, bp)
		}
		r.URL.RawPath = ""
	}
	if target, ok := legacyPaths[r.URL.Path]; ok {
		r.URL.Path = target
	} else if m := adminPHPRe.FindStringSubmatch(r.URL.Path); m != nil {
		r.URL.Path = "/admin/" + m[1]
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	s.mux.ServeHTTP(w, r)
}

func (s *Server) staticHandler(prefix string) http.Handler {
	sub, err := fs.Sub(s.static, prefix)
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.StripPrefix("/"+prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=2592000")
		fileServer.ServeHTTP(w, r)
	}))
}

// ---------------------------------------------------------------------------
// Request context
// ---------------------------------------------------------------------------

type ctx struct {
	s        *Server
	w        http.ResponseWriter
	r        *http.Request
	cfg      config.Config
	t        *i18n.Translator
	basePath string
	baseURL  string
	cookies  auth.CookieOptions
	sess     *auth.Session

	// adminChecked caches isAdmin for the request; csrfToken is set for
	// logged-in administrators.
	adminChecked bool
	admin        bool
	csrfToken    string
}

func (s *Server) newCtx(w http.ResponseWriter, r *http.Request) *ctx {
	cfg := s.site.Config.Get()
	c := &ctx{s: s, w: w, r: r, cfg: cfg, t: s.site.I18n.Translator(cfg.Language), basePath: s.site.BasePath()}
	c.baseURL = s.baseURL(r, cfg)
	sessName, remName := auth.CookieNames(c.baseURL)
	path := c.basePath
	if path == "" {
		path = "/"
	}
	c.cookies = auth.CookieOptions{SessionName: sessName, RememberName: remName, Path: path, Secure: s.isHTTPS(r)}
	return c
}

func (s *Server) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return s.opts.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

var hostRe = regexp.MustCompile(`^[a-z0-9.-]+(:\d+)?$`)

func (s *Server) baseURL(r *http.Request, cfg config.Config) string {
	if base := strings.TrimSpace(cfg.BaseURL); base != "" {
		if u, err := url.Parse(base); err == nil && u.Scheme != "" && u.Host != "" {
			return strings.TrimRight(base, "/")
		}
	}
	scheme := "http"
	if s.isHTTPS(r) {
		scheme = "https"
	}
	host := strings.ToLower(r.Host)
	if s.opts.TrustProxy && r.Header.Get("X-Forwarded-Host") != "" {
		host = strings.ToLower(r.Header.Get("X-Forwarded-Host"))
	}
	if !hostRe.MatchString(host) {
		host = "localhost"
	}
	return scheme + "://" + host + s.site.BasePath()
}

func (c *ctx) clientIP() string {
	if c.s.opts.TrustProxy {
		if fwd := c.r.Header.Get("X-Forwarded-For"); fwd != "" {
			return strings.TrimSpace(strings.Split(fwd, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(c.r.RemoteAddr)
	if err != nil {
		return c.r.RemoteAddr
	}
	return host
}

// session returns the existing session without creating one.
func (c *ctx) session() *auth.Session {
	if c.sess == nil {
		c.sess = c.s.sessions.Get(c.r, c.cookies)
	}
	return c.sess
}

// startSession returns the session, creating it if needed.
func (c *ctx) startSession() *auth.Session {
	if c.sess == nil {
		c.sess = c.s.sessions.Start(c.w, c.r, c.cookies)
	}
	return c.sess
}

// isAdmin reports whether the visitor is logged in, restoring a session
// from the remember-me cookie when present. The result is cached for the
// request.
func (c *ctx) isAdmin() bool {
	if c.adminChecked {
		return c.admin
	}
	c.adminChecked = true
	if !auth.HasCookies(c.r, c.cookies) {
		return false
	}
	sess := c.startSession()
	sess.Lock()
	defer sess.Unlock()
	c.admin = c.s.sessions.RestoreFromCookie(c.w, c.r, sess, c.cookies)
	if c.admin {
		c.csrfToken = sess.CSRF()
	}
	return c.admin
}

func (c *ctx) redirect(to string) {
	if c.r.Header.Get("HX-Request") == "true" && c.r.Header.Get("HX-Boosted") != "true" && c.r.Method != http.MethodGet {
		c.w.Header().Set("HX-Redirect", to)
		c.w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(c.w, c.r, to, http.StatusSeeOther)
}

func (c *ctx) isHTMX() bool { return c.r.Header.Get("HX-Request") == "true" }

func (c *ctx) form(key string) string { return c.r.FormValue(key) }

func (c *ctx) formTrim(key string) string { return strings.TrimSpace(c.r.FormValue(key)) }

func (c *ctx) query(key string) string { return strings.TrimSpace(c.r.URL.Query().Get(key)) }

func (c *ctx) validCSRF() bool {
	token := c.r.Header.Get("X-CSRF-Token")
	if token == "" {
		token = c.r.FormValue("csrf_token")
	}
	return auth.ValidCSRF(c.session(), token)
}

func (c *ctx) csrfFail() {
	http.Error(c.w, "Invalid CSRF token.", http.StatusForbidden)
}

func (c *ctx) adminLanding() string {
	if !c.cfg.EnableBlogPosts || c.cfg.AdminHomepage == "content" {
		return c.basePath + "/admin/content"
	}
	return c.basePath + "/admin/dashboard"
}

// admin wraps a handler that requires a logged-in administrator.
func (s *Server) admin(h func(*ctx)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := s.newCtx(w, r)
		if !s.site.Config.Installed() {
			c.redirect(c.basePath + "/setup")
			return
		}
		sess := c.startSession()
		sess.Lock()
		defer sess.Unlock()
		if !s.sessions.RestoreFromCookie(w, r, sess, c.cookies) {
			if c.isHTMX() {
				w.Header().Set("HX-Redirect", c.basePath+"/admin")
				return
			}
			http.Redirect(w, r, c.basePath+"/admin", http.StatusSeeOther)
			return
		}
		c.adminChecked, c.admin, c.csrfToken = true, true, sess.CSRF()
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, s.opts.MaxUploadMB<<20)
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				if err := r.ParseMultipartForm(8 << 20); err != nil {
					c.uploadTooLarge(err)
					return
				}
			} else if err := r.ParseForm(); err != nil {
				http.Error(w, "Bad request", http.StatusBadRequest)
				return
			}
			if !c.validCSRF() {
				c.csrfFail()
				return
			}
		}
		h(c)
	}
}

func (s *Server) handleAdminFallback(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/admin/" {
		s.handleLogin(w, r)
		return
	}
	s.handleFront(w, r)
}

// render executes a template into a buffer and writes it.
func (c *ctx) render(set *template.Template, name string, data any, status int) {
	var buf bytes.Buffer
	if err := set.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
		http.Error(c.w, "Template error: "+template.HTMLEscapeString(err.Error()), http.StatusInternalServerError)
		return
	}
	c.w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if status != 0 {
		c.w.WriteHeader(status)
	}
	_, _ = c.w.Write(buf.Bytes())
}

func (c *ctx) flash(ok bool, msg string) {
	if sess := c.session(); sess != nil {
		sess.Flash = &auth.Flash{OK: ok, Message: msg}
	}
}

func (c *ctx) uploadTooLarge(err error) {
	msg := c.t.T("admin.editor.error_upload_post_size", "limit", fmt.Sprintf("%dM", c.s.opts.MaxUploadMB))
	c.redirect(c.basePath + "/admin/content?upload_error=" + url.QueryEscape(msg))
	_ = err
}
