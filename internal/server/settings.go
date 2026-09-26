package server

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/johanvanrhyn/pureblog-go/internal/auth"
	"github.com/johanvanrhyn/pureblog-go/internal/blog"
	"github.com/johanvanrhyn/pureblog-go/internal/config"
)

// ---------------------------------------------------------------------------
// Site settings
// ---------------------------------------------------------------------------

type settingsSiteData struct {
	Pages     []*blog.Page
	Languages []struct{ Code, Name string }
}

var assetKeys = []string{"favicon", "apple_touch_icon", "og_image", "sidebar_logo"}

var lowerUnsafeRe = regexp.MustCompile(`[^a-z0-9._-]`)

func (s *Server) handleSettingsSite(c *ctx) {
	t := c.t
	cfg := c.cfg

	if asset := c.query("delete_asset"); asset != "" {
		if !c.validCSRF() {
			c.csrfFail()
			return
		}
		if ptr := assetPtr(&cfg.Assets, asset); ptr != nil {
			if strings.HasPrefix(*ptr, "/content/images/") {
				full := filepath.Join(s.site.Dir, filepath.FromSlash(*ptr))
				if blog.IsFile(full) && blog.WithinDir(s.site.ImagesDir, full) {
					_ = os.Remove(full)
				}
			}
			*ptr = ""
			if err := s.site.Config.Save(cfg); err == nil {
				c.flash(true, t.T("admin.settings.site.notice_updated"))
			} else {
				c.flash(false, t.T("admin.settings.site.error_save"))
			}
		}
		c.redirect(c.basePath + "/admin/settings-site")
		return
	}

	pages := []*blog.Page{}
	slugs := map[string]bool{}
	for _, p := range s.site.Pages(true) {
		if p.Slug != "" {
			pages = append(pages, p)
			slugs[p.Slug] = true
		}
	}

	var errs []string
	notice := ""
	if c.r.Method == http.MethodPost {
		f := c.formTrim
		checked := func(k string) bool { return c.form(k) != "" }
		postsPerPage, _ := strconv.Atoi(f("posts_per_page"))
		if f("posts_per_page") == "" {
			postsPerPage = 20
		}
		excerptLen, _ := strconv.Atoi(f("search_excerpt_length"))
		if f("search_excerpt_length") == "" {
			excerptLen = 2500
		}
		if excerptLen < 0 {
			excerptLen = 0
		}
		rssTTL, _ := strconv.Atoi(f("rss_ttl"))
		if f("rss_ttl") == "" {
			rssTTL = 3600
		}
		if rssTTL < 0 {
			rssTTL = 0
		}
		adminHome := f("admin_homepage")
		if adminHome != "dashboard" && adminHome != "content" {
			adminHome = "dashboard"
		}
		blogSlug := f("blog_page_slug")
		enableBlog := true
		if blogSlug == "__hidden__" {
			enableBlog = checked("enable_blog_posts")
		}
		homeSlug, searchSlug := f("homepage_slug"), f("search_page_slug")
		ogPref := f("og_image_preferred")
		if c.r.PostForm.Get("og_image_preferred") == "" {
			ogPref = "banner"
		}
		pcEnabled, pcURL := checked("purecomments_enabled"), f("purecomments_url")
		timezone, dateFormat := f("timezone"), f("date_format")

		if f("site_title") == "" {
			errs = append(errs, t.T("admin.settings.site.error_title"))
		}
		if postsPerPage < 1 || postsPerPage > 100 {
			errs = append(errs, t.T("admin.settings.site.error_posts_per_page"))
		}
		if !blog.ValidTimezone(timezone) {
			errs = append(errs, t.T("admin.settings.site.error_timezone"))
		}
		if dateFormat == "" {
			errs = append(errs, t.T("admin.settings.site.error_date_format"))
		}
		if homeSlug != "" && !slugs[homeSlug] {
			errs = append(errs, t.T("admin.settings.site.error_homepage"))
		}
		if blogSlug != "" && blogSlug != "__hidden__" && !slugs[blogSlug] {
			errs = append(errs, t.T("admin.settings.site.error_blog_page"))
		}
		if searchSlug != "" && !slugs[searchSlug] {
			errs = append(errs, t.T("admin.settings.site.error_search_page"))
		}
		if ogPref != "banner" && ogPref != "square" {
			errs = append(errs, t.T("admin.settings.site.error_og_format"))
		}
		if pcEnabled {
			if pcURL == "" {
				errs = append(errs, t.T("admin.settings.site.error_purecomments_url"))
			} else if u, err := url.Parse(pcURL); err != nil || u.Scheme == "" || u.Host == "" {
				errs = append(errs, t.T("admin.settings.site.error_purecomments_url_invalid"))
			}
		}

		if len(errs) == 0 {
			cfg.SiteTitle = f("site_title")
			cfg.SiteTagline = f("site_tagline")
			cfg.SiteDescription = f("site_description")
			cfg.SiteEmail = f("site_email")
			cfg.CustomNav = f("custom_nav")
			cfg.CustomNavOnly = checked("custom_nav_only")
			cfg.CustomRoutes = f("custom_routes")
			cfg.HeadInjectPage = f("head_inject_page")
			cfg.HeadInjectPost = f("head_inject_post")
			cfg.FooterInjectPage = f("footer_inject_page")
			cfg.FooterInjectPost = f("footer_inject_post")
			cfg.PostsPerPage = postsPerPage
			cfg.SearchExcerptLength = excerptLen
			cfg.SearchIncludePages = checked("search_include_pages")
			cfg.Language = orStr(f("language"), "en")
			cfg.Timezone = timezone
			cfg.DateFormat = dateFormat
			cfg.ShowReadingTime = checked("show_reading_time")
			cfg.BaseURL = f("base_url")
			cfg.HomepageSlug = homeSlug
			cfg.BlogPageSlug = blogSlug
			cfg.SearchPageSlug = searchSlug
			cfg.Cache.Enabled = checked("cache_enabled")
			cfg.Cache.RSSTTL = rssTTL
			cfg.AdminHomepage = adminHome
			cfg.AdminHideDashboard = adminHome == "content" && checked("admin_hide_dashboard")
			cfg.EnableBlogPosts = enableBlog
			cfg.Community.PurecommentsEnabled = pcEnabled
			cfg.Community.PurecommentsURL = strings.TrimRight(pcURL, "/")
			cfg.Assets.OGImagePreferred = ogPref

			_ = os.MkdirAll(s.site.ImagesDir, 0o755)
			for _, key := range assetKeys {
				file, header, err := c.r.FormFile(key)
				if err != nil {
					continue
				}
				head := make([]byte, 512)
				n, _ := io.ReadFull(file, head)
				mime := sniffImage(head[:n])
				_, allowed := allowedImageTypes[mime]
				if key == "apple_touch_icon" {
					allowed = mime == "image/png"
				}
				if !allowed {
					file.Close()
					if key == "apple_touch_icon" {
						errs = append(errs, t.T("admin.settings.site.error_apple_touch_icon_type"))
					} else {
						errs = append(errs, t.T("admin.editor.error_upload_type"))
					}
					continue
				}
				name := strings.ToLower(filepath.Base(header.Filename))
				name = strings.Trim(dashesRe.ReplaceAllString(lowerUnsafeRe.ReplaceAllString(name, "-"), "-"), "-")
				if name == "" {
					file.Close()
					continue
				}
				dest := filepath.Join(s.site.ImagesDir, name)
				out, err := os.Create(dest)
				if err == nil {
					_, err = out.Write(head[:n])
					if err == nil {
						_, err = io.Copy(out, file)
					}
					out.Close()
				}
				file.Close()
				if err == nil {
					*assetPtr(&cfg.Assets, key) = "/content/images/" + name
				}
			}

			if err := s.site.Config.Save(cfg); err == nil {
				s.site.CacheClear()
				_ = s.site.BuildSearchIndex()
				notice = t.T("admin.settings.site.notice_updated")
				c.cfg = cfg
				c.t = s.site.I18n.Translator(cfg.Language)
				c.basePath = s.site.BasePath()
			} else {
				errs = append(errs, t.T("admin.settings.site.error_save"))
			}
		}
	}

	d := settingsSiteData{Pages: pages}
	langs := s.site.I18n.Available()
	found := false
	for _, l := range langs {
		d.Languages = append(d.Languages, struct{ Code, Name string }{l.Code, l.Name})
		if l.Code == c.cfg.Language {
			found = true
		}
	}
	if !found && c.cfg.Language != "" {
		d.Languages = append(d.Languages, struct{ Code, Name string }{c.cfg.Language, c.cfg.Language})
	}
	p := c.adminPage(c.t.T("admin.settings.site.page_title"))
	p.Notice, p.Errors, p.Data = notice, errs, d
	c.renderAdmin("settings-site", p)
}

func assetPtr(a *config.Assets, key string) *string {
	switch key {
	case "favicon":
		return &a.Favicon
	case "apple_touch_icon":
		return &a.AppleTouchIcon
	case "og_image":
		return &a.OGImage
	case "sidebar_logo":
		return &a.SidebarLogo
	}
	return nil
}

// ---------------------------------------------------------------------------
// Theme settings
// ---------------------------------------------------------------------------

type themeCard struct {
	*blog.ThemeInfo
	Active bool
}

type settingsThemeData struct {
	Themes   []themeCard
	DocsLink template.HTML
}

func (s *Server) handleSettingsTheme(c *ctx) {
	t := c.t
	cfg := c.cfg
	var errs []string
	notice := ""
	if c.r.Method == http.MethodPost {
		action := c.form("action")
		switch {
		case c.r.PostForm.Has("apply_theme_id"):
			if th := s.site.ThemeByID(c.formTrim("apply_theme_id")); th == nil {
				errs = append(errs, t.T("admin.settings.theme.error_theme_not_found"))
			} else if err := s.site.ApplyTheme(th); err != nil {
				errs = append(errs, t.T("admin.settings.theme.error_save"))
			} else {
				s.site.CacheClear()
				notice = t.T("admin.settings.theme.notice_theme_applied")
			}
		case c.r.PostForm.Has("delete_theme_filename"):
			if s.site.DeleteThemeFile(c.formTrim("delete_theme_filename")) {
				notice = t.T("admin.settings.theme.notice_theme_deleted")
			} else {
				errs = append(errs, t.T("admin.settings.theme.error_theme_delete"))
			}
		case action == "create_theme":
			name := c.formTrim("new_theme_name")
			light := blog.Palette{Bg: c.formTrim("new_bg"), Text: c.formTrim("new_text"), Accent: c.formTrim("new_accent"), Border: c.formTrim("new_border"), AccentBg: c.formTrim("new_accent_bg")}
			dark := blog.Palette{Bg: c.formTrim("new_bg_dark"), Text: c.formTrim("new_text_dark"), Accent: c.formTrim("new_accent_dark"), Border: c.formTrim("new_border_dark"), AccentBg: c.formTrim("new_accent_bg_dark")}
			filled := func(p blog.Palette) int {
				n := 0
				for _, v := range []string{p.Bg, p.Text, p.Accent, p.Border, p.AccentBg} {
					if v != "" {
						n++
					}
				}
				return n
			}
			lf, df := filled(light), filled(dark)
			switch {
			case name == "":
				errs = append(errs, t.T("admin.settings.theme.error_theme_name"))
			case lf == 0 && df == 0:
				errs = append(errs, t.T("admin.settings.theme.error_theme_empty"))
			case lf > 0 && lf < 5:
				errs = append(errs, t.T("admin.settings.theme.error_theme_light_incomplete"))
			case df > 0 && df < 5:
				errs = append(errs, t.T("admin.settings.theme.error_theme_dark_incomplete"))
			default:
				var lp, dp *blog.Palette
				if lf == 5 {
					lp = &light
				}
				if df == 5 {
					dp = &dark
				}
				if _, err := s.site.SaveThemeJSON(name, lp, dp); err != nil {
					errs = append(errs, err.Error())
				} else {
					notice = t.T("admin.settings.theme.notice_theme_created")
				}
			}
		case action == "import_theme":
			file, header, err := c.r.FormFile("theme_file")
			if err != nil {
				errs = append(errs, t.T("admin.settings.theme.error_theme_upload"))
				break
			}
			data, _ := io.ReadAll(io.LimitReader(file, 1<<20))
			file.Close()
			if err := s.site.SaveUploadedTheme(header.Filename, data); err != nil {
				errs = append(errs, err.Error())
			} else {
				notice = t.T("admin.settings.theme.notice_theme_imported")
			}
		default:
			font, adminFont := orStr(c.form("font_stack"), "sans"), orStr(c.form("admin_font_stack"), "sans")
			adminMode, mode := orStr(c.form("admin_color_mode"), "auto"), orStr(c.form("color_mode"), "light")
			layout := orStr(c.form("post_list_layout"), "excerpt")
			in := func(v string, opts ...string) bool {
				for _, o := range opts {
					if v == o {
						return true
					}
				}
				return false
			}
			if !in(font, "sans", "serif", "mono") {
				errs = append(errs, t.T("admin.settings.theme.error_font"))
			}
			if !in(adminFont, "sans", "serif", "mono") {
				errs = append(errs, t.T("admin.settings.theme.error_admin_font"))
			}
			if !in(adminMode, "light", "dark", "auto") {
				errs = append(errs, t.T("admin.settings.theme.error_admin_color"))
			}
			if !in(mode, "light", "dark", "auto") {
				errs = append(errs, t.T("admin.settings.theme.error_color_mode"))
			}
			if !in(layout, "excerpt", "full", "archive") {
				errs = append(errs, t.T("admin.settings.theme.error_post_layout"))
			}
			if len(errs) == 0 {
				cfg.Theme.FontStack, cfg.Theme.AdminFontStack = font, adminFont
				cfg.Theme.AdminColorMode, cfg.Theme.ColorMode = adminMode, mode
				cfg.Theme.PostListLayout = layout
				if err := s.site.Config.Save(cfg); err != nil {
					errs = append(errs, t.T("admin.settings.theme.error_save"))
				} else {
					s.site.CacheClear()
					notice = t.T("admin.settings.theme.notice_updated")
				}
			}
		}
		c.cfg = s.site.Config.Get()
	}

	d := settingsThemeData{
		DocsLink: template.HTML(`<a target="_blank" rel="noopener noreferrer" href="https://docs.pureblog.org/themes/">` + template.HTMLEscapeString(t.T("admin.settings.theme.docs_link")) + `</a>`),
	}
	for _, th := range s.site.Themes() {
		d.Themes = append(d.Themes, themeCard{ThemeInfo: th, Active: blog.IsThemeActive(th, c.cfg.Theme)})
	}
	p := c.adminPage(t.T("admin.settings.theme.page_title"))
	p.Notice, p.Errors, p.Data = notice, errs, d
	c.renderAdmin("settings-theme", p)
}

// ---------------------------------------------------------------------------
// Custom CSS
// ---------------------------------------------------------------------------

type settingsCSSData struct {
	FrontCSS string
	AdminCSS string
}

func (s *Server) handleSettingsCSS(c *ctx) {
	t := c.t
	frontPath := filepath.Join(s.site.CSSDir, "custom.css")
	adminPath := filepath.Join(s.site.CSSDir, "admin-custom.css")
	read := func(p string) string {
		b, _ := os.ReadFile(p)
		return string(b)
	}
	d := settingsCSSData{FrontCSS: read(frontPath), AdminCSS: read(adminPath)}
	var errs []string
	notice := ""
	if c.r.Method == http.MethodPost {
		d.FrontCSS, d.AdminCSS = c.form("front_css"), c.form("admin_css")
		if err := os.MkdirAll(s.site.CSSDir, 0o755); err != nil {
			errs = append(errs, t.T("admin.settings.css.error_dir_front"))
		} else {
			if os.WriteFile(frontPath, []byte(d.FrontCSS), 0o644) != nil {
				errs = append(errs, t.T("admin.settings.css.error_save_front"))
			}
			if os.WriteFile(adminPath, []byte(d.AdminCSS), 0o644) != nil {
				errs = append(errs, t.T("admin.settings.css.error_save_admin"))
			}
		}
		if len(errs) == 0 {
			s.site.CacheClear()
			notice = t.T("admin.settings.css.notice_saved")
		}
	}
	p := c.adminPage(t.T("admin.settings.css.page_title"))
	p.CodeEditor = "css"
	p.Notice, p.Errors, p.Data = notice, errs, d
	c.renderAdmin("settings-css", p)
}

// ---------------------------------------------------------------------------
// User settings and two-factor authentication
// ---------------------------------------------------------------------------

type settingsUserData struct {
	MFAEnabled     bool
	SettingUp      bool
	QRCode         template.HTML
	Secret         string
	NewBackupCodes []string
	BackupCodesStr string
}

func (s *Server) handleSettingsUser(c *ctx) {
	t := c.t
	cfg := c.cfg
	sess := c.sess
	var errs []string
	notice := ""
	newCodes := sess.NewBackupCodes
	sess.NewBackupCodes = nil
	mfaEnabled := cfg.MFAEnabled && cfg.MFASecret != ""
	settingUp := sess.MFASetupSecret != "" && !mfaEnabled

	if c.r.URL.Query().Has("cancel_mfa_setup") {
		sess.MFASetupSecret = ""
		c.redirect(c.basePath + "/admin/settings-user")
		return
	}
	if c.r.URL.Query().Has("setup_mfa") && !mfaEnabled {
		if sess.MFASetupSecret == "" {
			sess.MFASetupSecret = auth.GenerateSecret(16)
		}
		settingUp = true
	}

	if c.r.Method == http.MethodPost {
		switch c.form("mfa_action") {
		case "confirm_setup":
			if sess.MFASetupSecret == "" {
				break
			}
			if auth.VerifyTOTP(sess.MFASetupSecret, c.formTrim("mfa_code")) {
				plain := auth.GenerateBackupCodes(8)
				cfg.MFAEnabled, cfg.MFASecret, cfg.MFABackupCodes = true, sess.MFASetupSecret, auth.HashBackupCodes(plain)
				if err := s.site.Config.Save(cfg); err == nil {
					sess.MFASetupSecret = ""
					s.sessions.ClearAllRememberMe()
					notice = t.T("admin.settings.user.mfa_notice_enabled")
					mfaEnabled, settingUp, newCodes = true, false, plain
				} else {
					errs = append(errs, t.T("admin.settings.user.error_save"))
				}
			} else {
				errs = append(errs, t.T("admin.settings.user.mfa_error_verify_code"))
				settingUp = true
			}
		case "disable_mfa":
			if !mfaEnabled {
				break
			}
			if auth.CheckPassword(cfg.AdminPasswordHash, c.form("mfa_disable_password")) {
				cfg.MFAEnabled, cfg.MFASecret, cfg.MFABackupCodes = false, "", []string{}
				if err := s.site.Config.Save(cfg); err == nil {
					s.sessions.ClearAllRememberMe()
					notice = t.T("admin.settings.user.mfa_notice_disabled")
					mfaEnabled = false
				} else {
					errs = append(errs, t.T("admin.settings.user.error_save"))
				}
			} else {
				errs = append(errs, t.T("admin.settings.user.mfa_error_password"))
			}
		case "regenerate_backup_codes":
			if !mfaEnabled {
				break
			}
			if auth.CheckPassword(cfg.AdminPasswordHash, c.form("mfa_regenerate_password")) {
				plain := auth.GenerateBackupCodes(8)
				cfg.MFABackupCodes = auth.HashBackupCodes(plain)
				if err := s.site.Config.Save(cfg); err == nil {
					notice = t.T("admin.settings.user.mfa_notice_codes_regenerated")
					newCodes = plain
				} else {
					errs = append(errs, t.T("admin.settings.user.error_save"))
				}
			} else {
				errs = append(errs, t.T("admin.settings.user.mfa_error_password"))
			}
		default:
			username := c.formTrim("admin_username")
			current, newPw, confirm := c.form("current_password"), c.form("new_password"), c.form("confirm_password")
			if username == "" {
				errs = append(errs, t.T("admin.settings.user.error_username"))
			}
			if (newPw != "" || confirm != "") && newPw != confirm {
				errs = append(errs, t.T("admin.settings.user.error_password_match"))
			}
			if newPw != "" && !auth.CheckPassword(cfg.AdminPasswordHash, current) {
				errs = append(errs, t.T("admin.settings.user.error_password_wrong"))
			}
			if len(errs) == 0 {
				cfg.AdminUsername = username
				if newPw != "" {
					if hash, err := auth.HashPassword(newPw); err == nil {
						cfg.AdminPasswordHash = hash
						s.sessions.ClearAllRememberMe()
					}
				}
				if err := s.site.Config.Save(cfg); err == nil {
					notice = t.T("admin.settings.user.notice_updated")
				} else {
					errs = append(errs, t.T("admin.settings.user.error_save"))
				}
			}
		}
		c.cfg = s.site.Config.Get()
	}

	d := settingsUserData{MFAEnabled: mfaEnabled, SettingUp: settingUp, NewBackupCodes: newCodes, BackupCodesStr: strings.Join(newCodes, "\n")}
	if settingUp && sess.MFASetupSecret != "" {
		d.Secret = sess.MFASetupSecret
		issuer := c.cfg.SiteTitle
		if issuer == "" {
			issuer = "PureBlog-Go"
		}
		account := orStr(c.cfg.AdminUsername, "admin")
		d.QRCode = template.HTML(auth.QRCodeSVG(auth.ProvisioningURI(d.Secret, account, issuer), 200))
	}
	p := c.adminPage(t.T("admin.settings.user.page_title"))
	p.Notice, p.Errors, p.Data = notice, errs, d
	c.renderAdmin("settings-user", p)
}

// ---------------------------------------------------------------------------
// Updates
// ---------------------------------------------------------------------------

type releaseInfo struct {
	OK          bool
	Error       string
	Tag         string
	Name        string
	URL         string
	PublishedAt string
	fetched     time.Time
}

func (s *Server) fetchLatestRelease() releaseInfo {
	if s.opts.ReleaseRepo == "" {
		return releaseInfo{Error: "Update checks are not configured."}
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+s.opts.ReleaseRepo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "PureBlog-Go/"+s.site.Version)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return releaseInfo{Error: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return releaseInfo{Error: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	var body struct {
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		HTMLURL     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return releaseInfo{Error: err.Error()}
	}
	if body.HTMLURL == "" {
		body.HTMLURL = "https://github.com/" + s.opts.ReleaseRepo + "/releases"
	}
	return releaseInfo{OK: true, Tag: body.TagName, Name: body.Name, URL: body.HTMLURL, PublishedAt: body.PublishedAt, fetched: time.Now()}
}

// cachedLatestVersion returns the latest release tag, refreshed at most
// every six hours (stored in content/.version-cache like the PHP version).
func (s *Server) cachedLatestVersion() string {
	cacheFile := filepath.Join(s.site.ContentDir, ".version-cache")
	if info, err := os.Stat(cacheFile); err == nil && time.Since(info.ModTime()) < 6*time.Hour {
		b, _ := os.ReadFile(cacheFile)
		return strings.TrimSpace(string(b))
	}
	rel := s.fetchLatestRelease()
	if rel.OK && rel.Tag != "" {
		_ = os.WriteFile(cacheFile, []byte(rel.Tag), 0o644)
		return rel.Tag
	}
	return ""
}

func versionsMatch(a, b string) bool {
	a = strings.TrimLeft(strings.ToLower(strings.TrimSpace(a)), "v")
	b = strings.TrimLeft(strings.ToLower(strings.TrimSpace(b)), "v")
	return a != "" && b != "" && a == b
}

type settingsUpdatesData struct {
	Current         string
	Repo            string
	Checked         bool
	Release         releaseInfo
	UpdateAvailable bool
	LatestDisplay   string
	Published       string
}

func (s *Server) handleSettingsUpdates(c *ctx) {
	d := settingsUpdatesData{Current: s.site.Version, Repo: s.opts.ReleaseRepo}
	if c.r.URL.Query().Has("check") {
		d.Checked = true
		d.Release = s.fetchLatestRelease()
		if d.Release.OK {
			d.UpdateAvailable = d.Release.Tag != "" && !versionsMatch(d.Current, d.Release.Tag)
			d.LatestDisplay = strings.TrimLeft(d.Release.Tag, "vV")
			if d.Release.PublishedAt != "" {
				d.Published = s.site.FormatDate(d.Release.PublishedAt, "Y-m-d")
			}
			_ = os.WriteFile(filepath.Join(s.site.ContentDir, ".version-cache"), []byte(d.Release.Tag), 0o644)
		}
	}
	p := c.adminPage(c.t.T("admin.settings.updates.page_title"))
	p.Data = d
	c.renderAdmin("settings-updates", p)
}
