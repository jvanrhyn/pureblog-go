package server

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/johanvanrhyn/pureblog-go/internal/auth"
	"github.com/johanvanrhyn/pureblog-go/internal/blog"
	"github.com/johanvanrhyn/pureblog-go/internal/config"
)

// Layout is a custom post layout from content/layouts.
type Layout struct {
	Name   string
	Label  string
	Fields []LayoutField
}

// LayoutField is an extra editor field defined by a layout's JSON file.
type LayoutField struct {
	Name    string   `json:"name"`
	Label   string   `json:"label"`
	Type    string   `json:"type"`
	Options []string `json:"options"`
}

// Layouts lists content/layouts/*.html with their optional .json field
// definitions.
func (s *Server) Layouts() []Layout {
	files, _ := filepath.Glob(filepath.Join(s.site.LayoutsDir, "*.html"))
	sort.Strings(files)
	var out []Layout
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".html")
		l := Layout{Name: name, Label: name}
		if raw, err := os.ReadFile(filepath.Join(s.site.LayoutsDir, name+".json")); err == nil {
			var def struct {
				Label  string            `json:"label"`
				Fields []json.RawMessage `json:"fields"`
			}
			if json.Unmarshal(raw, &def) == nil {
				if strings.TrimSpace(def.Label) != "" {
					l.Label = strings.TrimSpace(def.Label)
				}
				for _, rf := range def.Fields {
					var fld LayoutField
					var generic map[string]any
					if json.Unmarshal(rf, &generic) != nil {
						continue
					}
					fld.Name = fmt.Sprint(valueOr(generic["name"], ""))
					fld.Label = fmt.Sprint(valueOr(generic["label"], fld.Name))
					fld.Type = fmt.Sprint(valueOr(generic["type"], ""))
					if opts, ok := generic["options"].([]any); ok {
						for _, o := range opts {
							fld.Options = append(fld.Options, fmt.Sprint(o))
						}
					}
					l.Fields = append(l.Fields, fld)
				}
			}
		}
		out = append(out, l)
	}
	return out
}

func valueOr(v any, def string) any {
	if v == nil {
		return def
	}
	return v
}

// AdminPage is the data passed to admin templates.
type AdminPage struct {
	Config           config.Config
	BasePath         string
	BaseURL          string
	Lang             string
	Title            string
	FontStack        template.CSS
	AdminFont        string
	ColorMode        string
	CSSVersion       string
	JSVersion        string
	CustomCSS        template.CSS
	Icons            template.HTML
	HideNav          bool
	CodeEditor       string
	CSRF             string
	Path             string
	RequestURI       string
	IsSettings       bool
	BlogPostsEnabled bool
	AdminHomepage    string
	HideDashboard    bool
	Tab              string
	Layouts          []Layout
	Flash            *auth.Flash
	SearchNoticeSlug string
	Notice           string
	Errors           []string
	Data             any
}

func (s *Server) assetVersion(name string) string {
	if info, err := fs.Stat(s.static, name); err == nil && info.ModTime().Unix() > 0 {
		return strconv.FormatInt(info.ModTime().Unix(), 10)
	}
	return s.site.Version
}

func (c *ctx) adminPage(title string) *AdminPage {
	cfg := c.cfg
	p := &AdminPage{
		Config:           cfg,
		BasePath:         c.basePath,
		BaseURL:          c.baseURL,
		Lang:             strings.ReplaceAll(cfg.Language, "_", "-"),
		Title:            title,
		FontStack:        template.CSS(FontStackCSS(cfg.Theme.AdminFontStack)),
		AdminFont:        cfg.Theme.AdminFontStack,
		ColorMode:        cfg.Theme.AdminColorMode,
		CSSVersion:       c.s.assetVersion("admin/css/admin.css"),
		JSVersion:        c.s.assetVersion("admin/js/editor.js"),
		Icons:            c.s.adminIcons,
		Path:             strings.Trim(c.r.URL.Path, "/"),
		RequestURI:       c.basePath + c.r.URL.RequestURI(),
		BlogPostsEnabled: cfg.EnableBlogPosts,
	}
	if p.AdminFont == "" {
		p.AdminFont = "sans"
	}
	if p.ColorMode == "" {
		p.ColorMode = "auto"
	}
	if css, err := os.ReadFile(filepath.Join(c.s.site.CSSDir, "admin-custom.css")); err == nil {
		p.CustomCSS = template.CSS(css)
	}
	if sess := c.session(); sess != nil {
		p.CSRF = sess.CSRF()
		if sess.IsAdmin {
			p.Flash = sess.TakeFlash()
		}
	}
	p.IsSettings = strings.HasPrefix(p.Path, "admin/settings")
	p.AdminHomepage = "content"
	if cfg.EnableBlogPosts {
		p.AdminHomepage = cfg.AdminHomepage
		if p.AdminHomepage == "" {
			p.AdminHomepage = "dashboard"
		}
	}
	p.HideDashboard = !cfg.EnableBlogPosts || (cfg.AdminHideDashboard && p.AdminHomepage == "content")
	p.Layouts = c.s.Layouts()
	if slug := strings.TrimSpace(cfg.SearchPageSlug); slug != "" && !cfg.SearchPageNotified && c.s.site.PageBySlug(slug, false) == nil {
		p.SearchNoticeSlug = slug
	}
	return p
}

func (c *ctx) renderAdmin(name string, p *AdminPage) {
	c.render(c.s.adminSet, name, p, 0)
}

// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

type setupData struct {
	Values    map[string]string
	Languages []struct{ Code, Name string }
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	c := s.newCtx(w, r)
	if s.site.Config.Installed() {
		c.redirect(c.basePath + "/admin")
		return
	}
	_ = r.ParseForm()
	lang := strings.TrimSpace(r.PostFormValue("language"))
	if lang == "" {
		lang = strings.TrimSpace(r.URL.Query().Get("lang"))
	}
	if lang == "" {
		lang = "en"
	}
	tr := s.site.I18n.Translator(lang)
	values := map[string]string{"site_title": "", "site_tagline": "", "language": lang, "base_url": "", "admin_username": ""}
	var errs []string
	sess := c.startSession()
	sess.Lock()
	defer sess.Unlock()

	if r.Method == http.MethodPost {
		if !auth.ValidCSRF(sess, r.PostFormValue("csrf_token")) {
			c.csrfFail()
			return
		}
		for _, k := range []string{"site_title", "site_tagline", "base_url", "admin_username"} {
			values[k] = strings.TrimSpace(r.PostFormValue(k))
		}
		password, confirm := r.PostFormValue("password"), r.PostFormValue("confirm_password")
		if values["site_title"] == "" {
			errs = append(errs, tr.T("setup.error_title"))
		}
		if values["admin_username"] == "" {
			errs = append(errs, tr.T("setup.error_username"))
		}
		if password == "" || confirm == "" {
			errs = append(errs, tr.T("setup.error_password"))
		} else if password != confirm {
			errs = append(errs, tr.T("setup.error_mismatch"))
		}
		if len(errs) == 0 {
			cfg := config.Default()
			cfg.SiteTitle = values["site_title"]
			cfg.SiteTagline = values["site_tagline"]
			cfg.Language = lang
			cfg.BaseURL = values["base_url"]
			if cfg.BaseURL == "" {
				cfg.BaseURL = c.baseURL
			}
			cfg.AdminUsername = values["admin_username"]
			hash, err := auth.HashPassword(password)
			if err == nil {
				cfg.AdminPasswordHash = hash
				err = s.site.Config.Save(cfg)
			}
			if err == nil {
				s.seedContent()
				http.Redirect(w, r, s.site.BasePath()+"/admin?setup=1", http.StatusSeeOther)
				return
			}
			errs = append(errs, tr.T("setup.error_config_write"))
		}
	}

	set, err := s.setupTemplates(lang)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	p := c.adminPage(tr.T("setup.page_title"))
	p.HideNav = true
	p.Lang = strings.ReplaceAll(lang, "_", "-")
	p.CSRF = sess.CSRF()
	p.Errors = errs
	p.SearchNoticeSlug = ""
	d := setupData{Values: values}
	for _, l := range s.site.I18n.Available() {
		d.Languages = append(d.Languages, struct{ Code, Name string }{l.Code, l.Name})
	}
	p.Data = d
	c.render(set, "setup", p, 0)
}

// seedContent copies the bundled demo pages, images and CSS into a fresh
// installation, then builds the indexes.
func (s *Server) seedContent() {
	_ = fs.WalkDir(s.embedded, "demo", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, "demo/")
		dest := filepath.Join(s.site.ContentDir, filepath.FromSlash(rel))
		if blog.IsFile(dest) {
			return nil
		}
		data, err := fs.ReadFile(s.embedded, p)
		if err != nil {
			return nil
		}
		if os.MkdirAll(filepath.Dir(dest), 0o755) == nil {
			_ = os.WriteFile(dest, data, 0o644)
		}
		return nil
	})
	for _, dir := range []string{s.site.PostsDir, s.site.ImagesDir, s.site.DataDir} {
		_ = os.MkdirAll(dir, 0o755)
	}
	_ = s.site.BuildSearchIndex()
	_ = s.site.BuildTagIndex()
}

// ---------------------------------------------------------------------------
// Login / logout
// ---------------------------------------------------------------------------

type loginData struct {
	MFAPending bool
	LockedOut  bool
	Error      string
	Username   string
	SetupDone  bool
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	c := s.newCtx(w, r)
	if !s.site.Config.Installed() {
		c.redirect(c.basePath + "/setup")
		return
	}
	sess := c.startSession()
	sess.Lock()
	defer sess.Unlock()
	if s.sessions.RestoreFromCookie(w, r, sess, c.cookies) {
		c.redirect(c.adminLanding())
		return
	}
	cfg := c.cfg
	ip := c.clientIP()
	now := time.Now().Unix()
	lockout := s.sessions.LockoutUntil(ip)
	d := loginData{MFAPending: sess.MFAPending, LockedOut: lockout > now, SetupDone: r.URL.Query().Get("setup") != ""}

	if r.URL.Query().Has("cancel_mfa") && sess.MFAPending {
		sess.MFAPending, sess.MFARememberMe = false, false
		c.redirect(c.basePath + "/admin")
		return
	}

	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		if !auth.ValidCSRF(sess, r.PostFormValue("csrf_token")) {
			c.csrfFail()
			return
		}
		fail := func(msgKey string) {
			if until := s.sessions.RecordFailure(ip); until > now {
				d.LockedOut = true
				d.Error = c.t.T("admin.login.error_lockout_5")
			} else {
				d.Error = c.t.T(msgKey)
			}
		}
		switch {
		case d.LockedOut:
			d.Error = c.t.T("admin.login.error_lockout", "minutes", int(math.Ceil(float64(lockout-now)/60)))
		case sess.MFAPending:
			code := strings.TrimSpace(r.PostFormValue("mfa_code"))
			verified := code != "" && cfg.MFASecret != "" && auth.VerifyTOTP(cfg.MFASecret, code)
			if !verified && code != "" && len(cfg.MFABackupCodes) > 0 {
				if i := auth.VerifyBackupCode(code, cfg.MFABackupCodes); i >= 0 {
					verified = true
					cfg.MFABackupCodes = append(cfg.MFABackupCodes[:i], cfg.MFABackupCodes[i+1:]...)
					_ = s.site.Config.Save(cfg)
				}
			}
			if verified {
				s.sessions.Regenerate(w, sess, c.cookies)
				sess.IsAdmin = true
				remember := sess.MFARememberMe
				sess.MFAPending, sess.MFARememberMe = false, false
				s.sessions.ClearFailures(ip)
				if remember {
					s.sessions.SetRememberMe(w, c.cookies)
				}
				c.redirect(c.adminLanding())
				return
			}
			fail("admin.login.mfa_error_invalid")
		default:
			username := strings.TrimSpace(r.PostFormValue("username"))
			d.Username = username
			password := r.PostFormValue("password")
			if username != "" && auth.ConstantTimeEqual(cfg.AdminUsername, username) && auth.CheckPassword(cfg.AdminPasswordHash, password) {
				s.sessions.Regenerate(w, sess, c.cookies)
				if cfg.MFAEnabled && cfg.MFASecret != "" {
					sess.MFAPending = true
					sess.MFARememberMe = r.PostFormValue("remember_me") != ""
					d.MFAPending = true
				} else {
					sess.IsAdmin = true
					s.sessions.ClearFailures(ip)
					if r.PostFormValue("remember_me") != "" {
						s.sessions.SetRememberMe(w, c.cookies)
					}
					c.redirect(c.adminLanding())
					return
				}
			} else {
				fail("admin.login.error_invalid")
			}
		}
	}

	title := c.t.T("admin.login.page_title")
	if d.MFAPending {
		title = c.t.T("admin.login.mfa_page_title")
	}
	p := c.adminPage(title)
	p.HideNav = true
	p.SearchNoticeSlug = ""
	p.CSRF = sess.CSRF()
	p.Data = d
	c.renderAdmin("login", p)
}

func (s *Server) handleLogout(c *ctx) {
	s.sessions.ClearRememberMe(c.w, c.r, c.cookies)
	s.sessions.Destroy(c.w, c.sess, c.cookies)
	c.redirect(c.basePath + "/admin")
}

func (s *Server) handleClearCache(c *ctx) {
	s.site.CacheClear()
	msg := c.t.T("admin.nav.cache_cleared")
	if c.isHTMX() {
		fmt.Fprintf(c.w, `<p class="notice" data-auto-dismiss>%s</p>`, template.HTMLEscapeString(msg))
		return
	}
	c.flash(true, msg)
	c.redirect(c.safeReturn(c.form("return")))
}

func (s *Server) handleSearchPageNotice(c *ctx) {
	cfg := c.cfg
	if c.form("search_page_action") == "create" {
		_, _ = s.site.SavePage(blog.PageInput{
			Title: "Search", Slug: "search", Status: "published",
			Description:  "Search posts on this site.",
			IncludeInNav: true,
			Content:      "# Search this site\n\nThis is the search page. You can replace (or remove) this text by editing the page in PureBlog-Go admin.",
		}, "")
		c.flash(true, c.t.T("admin.settings.site.notice_search_created"))
	}
	cfg.SearchPageNotified = true
	_ = s.site.Config.Save(cfg)
	if c.isHTMX() {
		if f := c.sess.TakeFlash(); f != nil {
			fmt.Fprintf(c.w, `<p class="notice" data-auto-dismiss>%s</p>`, template.HTMLEscapeString(f.Message))
		}
		return
	}
	c.redirect(c.safeReturn(c.form("return")))
}

// safeReturn only allows redirects back into the admin area.
func (c *ctx) safeReturn(to string) string {
	if strings.HasPrefix(to, c.basePath+"/admin") && !strings.HasPrefix(to, "//") {
		return to
	}
	return c.adminLanding()
}

// ---------------------------------------------------------------------------
// Dashboard
// ---------------------------------------------------------------------------

type chartCol struct {
	Label string
	Count int
	Bar   int
	URL   string
}

type tagCount struct {
	Slug  string
	Name  string
	Count int
}

type dashboardData struct {
	CurrentVersion   string
	LatestVersion    string
	UpdateAvailable  bool
	UpdatesEnabled   bool
	LastPublishedAgo string
	PublishedCount   int
	CurrentYear      int
	Recent           []chartCol
	RecentTotal      int
	RecentCount      int
	RecentWords      int
	RecentBooks      float64
	RecentTopTags    []tagCount
	RecentCutoff     int64
	Years            []chartCol
	TotalWords       int
	Books            float64
	AvgWords         int
	Months           []chartCol
	Days             []chartCol
	TopTags          []tagCount
	AllTags          []tagCount
}

func (s *Server) handleDashboard(c *ctx) {
	if !c.cfg.EnableBlogPosts {
		c.redirect(c.basePath + "/admin/content")
		return
	}
	loc := s.site.Loc()
	now := time.Now().In(loc)
	t := c.t
	posts := s.site.Posts(false)
	d := dashboardData{PublishedCount: len(posts), CurrentYear: now.Year()}

	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	index := map[string]int{}
	for i := 11; i >= 0; i-- {
		m := first.AddDate(0, -i, 0)
		index[fmt.Sprintf("%d-%d", m.Year(), m.Month())] = len(d.Recent)
		d.Recent = append(d.Recent, chartCol{
			Label: t.T("date.months_short." + strconv.Itoa(int(m.Month())-1)),
			URL:   fmt.Sprintf("%s/admin/content?tab=posts&year=%d&month=%d", c.basePath, m.Year(), m.Month()),
		})
	}
	cutoff := first.AddDate(0, -11, 0).Unix()
	d.RecentCutoff = cutoff
	months := make([]int, 13)
	days := make([]int, 8)
	years := map[int]int{}
	tagCounts, recentTags := map[string]int{}, map[string]int{}
	tagNames := map[string]string{}
	var lastTS int64
	for _, p := range posts {
		words := blog.WordCount(p.Content)
		d.TotalWords += words
		if p.Timestamp > 0 {
			pt := time.Unix(p.Timestamp, 0).In(loc)
			if i, ok := index[fmt.Sprintf("%d-%d", pt.Year(), pt.Month())]; ok {
				d.Recent[i].Count++
			}
			if p.Timestamp >= cutoff {
				d.RecentCount++
				d.RecentWords += words
			}
			months[pt.Month()]++
			wd := int(pt.Weekday())
			if wd == 0 {
				wd = 7
			}
			days[wd]++
			years[pt.Year()]++
		}
		if p.Timestamp > lastTS {
			lastTS = p.Timestamp
		}
		for _, tag := range p.Tags {
			name := strings.TrimSpace(tag)
			if name == "" {
				continue
			}
			slug := blog.NormalizeTag(name)
			if _, ok := tagNames[slug]; !ok {
				tagNames[slug] = name
			}
			tagCounts[slug]++
			if p.Timestamp >= cutoff {
				recentTags[slug]++
			}
		}
	}
	scale := func(n, max, px int) int {
		if n <= 0 {
			return 0
		}
		v := int(math.Round(float64(n) / float64(max) * float64(px)))
		if v < 3 {
			v = 3
		}
		return v
	}
	maxOf := func(vals []int) int {
		m := 1
		for _, v := range vals {
			if v > m {
				m = v
			}
		}
		return m
	}
	var recentCounts []int
	for _, col := range d.Recent {
		recentCounts = append(recentCounts, col.Count)
		d.RecentTotal += col.Count
	}
	maxRecent := maxOf(recentCounts)
	for i := range d.Recent {
		d.Recent[i].Bar = scale(d.Recent[i].Count, maxRecent, 120)
	}
	var yearKeys []int
	for y := range years {
		yearKeys = append(yearKeys, y)
	}
	sort.Ints(yearKeys)
	var yearVals []int
	for _, y := range yearKeys {
		yearVals = append(yearVals, years[y])
	}
	maxYear := maxOf(yearVals)
	for _, y := range yearKeys {
		d.Years = append(d.Years, chartCol{Label: strconv.Itoa(y), Count: years[y], Bar: scale(years[y], maxYear, 120),
			URL: fmt.Sprintf("%s/admin/content?tab=posts&year=%d", c.basePath, y)})
	}
	maxMonth := maxOf(months[1:])
	for m := 1; m <= 12; m++ {
		d.Months = append(d.Months, chartCol{Label: t.T("date.months_short." + strconv.Itoa(m-1)), Count: months[m], Bar: scale(months[m], maxMonth, 100)})
	}
	maxDay := maxOf(days[1:])
	for wd := 1; wd <= 7; wd++ {
		d.Days = append(d.Days, chartCol{Label: t.T("date.days_short." + strconv.Itoa(wd%7)), Count: days[wd], Bar: scale(days[wd], maxDay, 100)})
	}
	if d.PublishedCount > 0 {
		d.AvgWords = int(math.Round(float64(d.TotalWords) / float64(d.PublishedCount)))
	}
	d.Books = math.Round(float64(d.TotalWords)/80000*10) / 10
	d.RecentBooks = math.Round(float64(d.RecentWords)/80000*10) / 10
	sorted := func(m map[string]int) []tagCount {
		var out []tagCount
		for slug, n := range m {
			out = append(out, tagCount{Slug: slug, Name: tagNames[slug], Count: n})
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].Count != out[j].Count {
				return out[i].Count > out[j].Count
			}
			return out[i].Slug < out[j].Slug
		})
		return out
	}
	d.AllTags = sorted(tagCounts)
	d.TopTags = d.AllTags
	if len(d.TopTags) > 5 {
		d.TopTags = d.TopTags[:5]
	}
	d.RecentTopTags = sorted(recentTags)
	if len(d.RecentTopTags) > 5 {
		d.RecentTopTags = d.RecentTopTags[:5]
	}
	if lastTS > 0 {
		d.LastPublishedAgo = s.site.RelativeTime(lastTS)
	} else {
		d.LastPublishedAgo = t.T("admin.dashboard.no_posts_yet")
	}

	d.CurrentVersion = s.site.Version
	d.UpdatesEnabled = s.opts.ReleaseRepo != ""
	if d.UpdatesEnabled {
		d.LatestVersion = s.cachedLatestVersion()
		d.UpdateAvailable = d.LatestVersion != "" && !versionsMatch(d.CurrentVersion, d.LatestVersion)
	}

	p := c.adminPage(t.T("admin.dashboard.page_title"))
	p.Data = d
	c.renderAdmin("dashboard", p)
}

// ---------------------------------------------------------------------------
// Content list
// ---------------------------------------------------------------------------

type pageRow struct {
	Page     *blog.Page
	ViewURL  string
	IsHome   bool
	IsBlog   bool
	IsSearch bool
}

type contentData struct {
	Tab            string
	HasLayouts     bool
	Search         string
	FilterYear     int
	FilterMonth    int
	FilterTag      string
	FilterStatus   string
	FilterLayout   string
	FilterSince    int64
	FilterLabel    string
	DetailsOpen    bool
	Years          []int
	Tags           []tagCount
	UsedLayouts    []Layout
	Posts          []*blog.Post
	PostDateFormat string
	PrevURL        string
	NextURL        string
	Pages          []pageRow
	Saved          bool
	Deleted        bool
	UploadError    string
	DocsLink       template.HTML
}

func (s *Server) handleContent(c *ctx) {
	cfg := c.cfg
	t := c.t
	q := c.r.URL.Query()
	d := contentData{Tab: q.Get("tab")}
	if !cfg.EnableBlogPosts {
		d.Tab = "pages"
	} else if d.Tab != "posts" && d.Tab != "pages" {
		d.Tab = "posts"
	}
	layouts := s.Layouts()
	d.HasLayouts = len(layouts) > 0
	d.Saved = q.Get("saved") != ""
	d.Deleted = q.Get("deleted") != ""
	d.UploadError = q.Get("upload_error")
	d.DocsLink = template.HTML(`<a href="https://docs.pureblog.org/special-pages/" target="_blank" rel="noopener noreferrer">` + template.HTMLEscapeString(t.T("admin.content.badge_docs_link")) + `</a>`)

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	d.Search = strings.TrimSpace(q.Get("q"))
	d.FilterYear, _ = strconv.Atoi(q.Get("year"))
	d.FilterMonth, _ = strconv.Atoi(q.Get("month"))
	d.FilterTag = strings.TrimSpace(q.Get("tag"))
	d.FilterStatus = strings.TrimSpace(q.Get("status"))
	d.FilterLayout = strings.TrimSpace(q.Get("layout"))
	d.FilterSince, _ = strconv.ParseInt(q.Get("since"), 10, 64)
	if d.FilterStatus != "draft" && d.FilterStatus != "scheduled" && d.FilterStatus != "published" {
		d.FilterStatus = ""
	}
	if d.FilterYear < 2000 || d.FilterYear > 2100 {
		d.FilterYear = 0
	}
	if d.FilterMonth < 1 || d.FilterMonth > 12 {
		d.FilterMonth = 0
	}
	if d.FilterSince < 0 || d.FilterSince > time.Now().Unix() {
		d.FilterSince = 0
	}

	all := s.site.Posts(true)
	order := map[string]int{"draft": 0, "scheduled": 1, "published": 2}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if order[a.Status] != order[b.Status] {
			return order[a.Status] < order[b.Status]
		}
		if a.Status == "scheduled" {
			return a.Timestamp < b.Timestamp
		}
		return a.Timestamp > b.Timestamp
	})

	yearSet := map[int]bool{}
	tagNames := map[string]string{}
	used := map[string]string{}
	for _, p := range all {
		if p.Timestamp > 0 {
			yearSet[time.Unix(p.Timestamp, 0).UTC().Year()] = true
		}
		for _, tag := range p.Tags {
			if tag = strings.TrimSpace(tag); tag != "" {
				slug := blog.NormalizeTag(tag)
				if _, ok := tagNames[slug]; !ok {
					tagNames[slug] = tag
				}
			}
		}
		if l := strings.TrimSpace(p.Layout()); l != "" {
			if _, ok := used[l]; !ok {
				used[l] = strings.ToUpper(l[:1]) + l[1:]
			}
		}
	}
	for y := range yearSet {
		d.Years = append(d.Years, y)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(d.Years)))
	for slug, name := range tagNames {
		d.Tags = append(d.Tags, tagCount{Slug: slug, Name: name})
	}
	sort.Slice(d.Tags, func(i, j int) bool { return d.Tags[i].Slug < d.Tags[j].Slug })
	for _, l := range layouts {
		if _, ok := used[l.Name]; ok {
			used[l.Name] = l.Label
		}
	}
	for name, label := range used {
		d.UsedLayouts = append(d.UsedLayouts, Layout{Name: name, Label: label})
	}
	sort.Slice(d.UsedLayouts, func(i, j int) bool { return d.UsedLayouts[i].Name < d.UsedLayouts[j].Name })
	if _, ok := used[d.FilterLayout]; d.FilterLayout != "" && !ok {
		d.FilterLayout = ""
	}

	words := blog.QueryWords(d.Search)
	tagFilter := blog.NormalizeTag(d.FilterTag)
	var filtered []*blog.Post
	for _, p := range all {
		if len(words) > 0 && !blog.MatchesQuery(p.SearchText(), words) {
			continue
		}
		if d.FilterYear > 0 || d.FilterMonth > 0 {
			if p.Timestamp == 0 {
				continue
			}
			pt := time.Unix(p.Timestamp, 0).UTC()
			if d.FilterYear > 0 && pt.Year() != d.FilterYear || d.FilterMonth > 0 && int(pt.Month()) != d.FilterMonth {
				continue
			}
		}
		if d.FilterTag != "" {
			match := false
			for _, tag := range p.Tags {
				if blog.NormalizeTag(tag) == tagFilter {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		if d.FilterStatus != "" && p.Status != d.FilterStatus {
			continue
		}
		if d.FilterSince > 0 && p.Timestamp < d.FilterSince {
			continue
		}
		if d.FilterLayout != "" && strings.TrimSpace(p.Layout()) != d.FilterLayout {
			continue
		}
		filtered = append(filtered, p)
	}

	anyFilter := d.FilterYear > 0 || d.FilterMonth > 0 || d.FilterTag != "" || d.FilterStatus != "" || d.FilterSince > 0 || d.FilterLayout != ""
	if anyFilter {
		var parts []string
		switch {
		case d.FilterYear > 0 && d.FilterMonth > 0:
			parts = append(parts, t.T("date.months."+strconv.Itoa(d.FilterMonth-1))+" "+strconv.Itoa(d.FilterYear))
		case d.FilterYear > 0:
			parts = append(parts, strconv.Itoa(d.FilterYear))
		case d.FilterMonth > 0:
			parts = append(parts, t.T("date.months."+strconv.Itoa(d.FilterMonth-1)))
		}
		if d.FilterTag != "" {
			name := tagNames[d.FilterTag]
			if name == "" {
				name = d.FilterTag
			}
			parts = append(parts, name)
		}
		if d.FilterStatus != "" {
			parts = append(parts, t.T("admin.editor.status_"+d.FilterStatus))
		}
		if d.FilterLayout != "" {
			parts = append(parts, used[d.FilterLayout])
		}
		if d.FilterSince > 0 && d.FilterYear == 0 {
			parts = append(parts, t.T("admin.content.filter_recent"))
		}
		d.FilterLabel = strings.Join(parts, ", ")
	}
	d.DetailsOpen = anyFilter || d.Search != ""

	const perPage = 20
	totalPages := 1
	if len(filtered) > 0 {
		totalPages = (len(filtered) + perPage - 1) / perPage
	}
	start := (page - 1) * perPage
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + perPage
	if end > len(filtered) {
		end = len(filtered)
	}
	d.Posts = filtered[start:end]
	if totalPages > 1 {
		params := url.Values{"tab": {"posts"}}
		if d.FilterYear > 0 {
			params.Set("year", strconv.Itoa(d.FilterYear))
		}
		if d.FilterMonth > 0 {
			params.Set("month", strconv.Itoa(d.FilterMonth))
		}
		if d.FilterTag != "" {
			params.Set("tag", d.FilterTag)
		}
		if d.FilterStatus != "" {
			params.Set("status", d.FilterStatus)
		}
		if d.FilterLayout != "" {
			params.Set("layout", d.FilterLayout)
		}
		if d.FilterSince > 0 {
			params.Set("since", strconv.FormatInt(d.FilterSince, 10))
		}
		if d.Search != "" {
			params.Set("q", d.Search)
		}
		link := func(n int) string {
			params.Set("page", strconv.Itoa(n))
			return c.basePath + "/admin/content?" + params.Encode()
		}
		if page > 1 {
			d.PrevURL = link(page - 1)
		}
		if page < totalPages {
			d.NextURL = link(page + 1)
		}
	}
	d.PostDateFormat = strings.TrimSpace(cfg.DateFormat)
	if d.PostDateFormat == "" {
		d.PostDateFormat = config.DefaultDateFormat
	}
	if !blog.LayoutHasTime(d.PostDateFormat) {
		d.PostDateFormat += " @ 15:04"
	}

	pages := s.site.Pages(true)
	sort.SliceStable(pages, func(i, j int) bool {
		if pages[i].Status != pages[j].Status {
			return pages[i].Status == "draft"
		}
		return pages[i].Title < pages[j].Title
	})
	home := strings.TrimSpace(cfg.HomepageSlug)
	blogSlug := strings.TrimSpace(cfg.BlogPageSlug)
	searchSlug := strings.TrimSpace(cfg.SearchPageSlug)
	for _, pg := range pages {
		row := pageRow{Page: pg}
		row.IsHome = home != "" && pg.Slug == home
		row.IsBlog = blogSlug != "__hidden__" && blogSlug != "" && pg.Slug == blogSlug
		row.IsSearch = searchSlug != "" && pg.Slug == searchSlug
		switch {
		case pg.Status != "published":
			row.ViewURL = c.basePath + "/admin/preview?type=page&slug=" + url.QueryEscape(pg.Slug)
		case row.IsHome:
			row.ViewURL = c.basePath + "/"
		default:
			row.ViewURL = c.basePath + "/" + url.PathEscape(pg.Slug)
		}
		d.Pages = append(d.Pages, row)
	}

	title := t.T("admin.content.page_title")
	p := c.adminPage(title)
	p.Tab = d.Tab
	p.Data = d
	c.renderAdmin("content", p)
}
