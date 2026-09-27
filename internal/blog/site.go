// Package blog implements PureBlog-Go's content model: posts and pages stored
// as Markdown files with front matter, search and tag indexes, themes and
// the page cache.
package blog

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jvanrhyn/pureblog-go/internal/config"
	"github.com/jvanrhyn/pureblog-go/internal/i18n"
	"github.com/jvanrhyn/pureblog-go/internal/markdown"
)

// Site is a PureBlog-Go installation rooted at Dir.
type Site struct {
	Dir         string
	ContentDir  string
	PostsDir    string
	PagesDir    string
	ImagesDir   string
	CSSDir      string
	ThemesDir   string
	LayoutsDir  string
	IncludesDir string
	AutosaveDir string
	DataDir     string
	CacheDir    string
	ConfigDir   string

	Config  *config.Store
	Store   *Store
	I18n    *i18n.Bundle
	MD      *markdown.Renderer
	Version string

	coreThemes fs.FS
}

// Open prepares a site rooted at dir. embedded provides the bundled themes
// (themes/*.json) and translations (lang/*.json).
func Open(dir string, embedded fs.FS, version string) (*Site, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	s := &Site{
		Dir:         abs,
		ContentDir:  filepath.Join(abs, "content"),
		PostsDir:    filepath.Join(abs, "content", "posts"),
		PagesDir:    filepath.Join(abs, "content", "pages"),
		ImagesDir:   filepath.Join(abs, "content", "images"),
		CSSDir:      filepath.Join(abs, "content", "css"),
		ThemesDir:   filepath.Join(abs, "content", "themes"),
		LayoutsDir:  filepath.Join(abs, "content", "layouts"),
		IncludesDir: filepath.Join(abs, "content", "includes"),
		AutosaveDir: filepath.Join(abs, "content", "autosaves"),
		DataDir:     filepath.Join(abs, "data"),
		CacheDir:    filepath.Join(abs, "cache"),
		ConfigDir:   filepath.Join(abs, "config"),
		Version:     version,
	}
	s.coreThemes, err = fs.Sub(embedded, "themes")
	if err != nil {
		return nil, err
	}
	if s.I18n, err = i18n.Load(embedded, "lang"); err != nil {
		return nil, err
	}
	if s.Config, err = config.Open(s.ConfigDir); err != nil {
		return nil, err
	}
	s.Store = NewStore(s.ContentDir)
	s.MD = markdown.New(s.baseHost)
	return s, nil
}

func (s *Site) baseHost() string {
	u, err := url.Parse(strings.TrimSpace(s.Config.Get().BaseURL))
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// Loc returns the site's time zone.
func (s *Site) Loc() *time.Location { return Location(s.Config.Get()) }

// T returns the translator for the configured language.
func (s *Site) T() *i18n.Translator { return s.I18n.Translator(s.Config.Get().Language) }

// Posts returns posts, newest first.
func (s *Site) Posts(includeDrafts bool) []*Post { return s.Store.Posts(s.Loc(), includeDrafts) }

// Pages returns pages sorted by title.
func (s *Site) Pages(includeDrafts bool) []*Page { return s.Store.Pages(s.Loc(), includeDrafts) }

// PostBySlug finds a post.
func (s *Site) PostBySlug(slug string, includeDrafts bool) *Post {
	return s.Store.PostBySlug(s.Loc(), slug, includeDrafts)
}

// PageBySlug finds a page.
func (s *Site) PageBySlug(slug string, includeDrafts bool) *Page {
	return s.Store.PageBySlug(s.Loc(), slug, includeDrafts)
}

// BasePath returns the URL path prefix from the configured base URL.
func (s *Site) BasePath() string {
	base := strings.TrimSpace(s.Config.Get().BaseURL)
	if base == "" {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return strings.TrimRight(u.Path, "/")
}

// NowForStorage returns the current time formatted for front matter.
func (s *Site) NowForStorage() string {
	return time.Now().In(s.Loc()).Format(StorageLayout)
}

// FormatDate formats a stored date with a Go time layout (or the site's
// date format when layout is empty), translating month and day names.
func (s *Site) FormatDate(value, layout string) string {
	cfg := s.Config.Get()
	t, ok := ParsePostTime(value, Location(cfg))
	if !ok {
		return ""
	}
	if strings.TrimSpace(layout) == "" {
		layout = strings.TrimSpace(cfg.DateFormat)
		if layout == "" {
			layout = config.DefaultDateFormat
		}
	}
	return s.I18n.Translator(cfg.Language).TranslateDate(t.Format(layout))
}

// ReadingTime returns the localised "N min read" label.
func (s *Site) ReadingTime(content string) string {
	words := WordCount(StripTags(content))
	minutes := (words + 199) / 200
	if minutes < 1 {
		minutes = 1
	}
	return s.T().T("frontend.reading_time", "n", minutes)
}

// RelativeTime returns a localised "N minutes ago" style label.
func (s *Site) RelativeTime(ts int64) string {
	t := s.T()
	diff := time.Now().Unix() - ts
	if diff < 0 {
		diff = 0
	}
	switch {
	case diff < 60:
		return t.T("admin.dashboard.time_just_now")
	case diff < 3600:
		n := diff / 60
		if n == 1 {
			return t.T("admin.dashboard.time_minute_ago", "n", n)
		}
		return t.T("admin.dashboard.time_minutes_ago", "n", n)
	case diff < 86400:
		n := diff / 3600
		if n == 1 {
			return t.T("admin.dashboard.time_hour_ago", "n", n)
		}
		return t.T("admin.dashboard.time_hours_ago", "n", n)
	}
	n := diff / 86400
	if n == 1 {
		return t.T("admin.dashboard.time_day_ago", "n", n)
	}
	return t.T("admin.dashboard.time_days_ago", "n", n)
}

// ---------------------------------------------------------------------------
// Saving and deleting content
// ---------------------------------------------------------------------------

// PostInput is the data submitted from the post editor.
type PostInput struct {
	Title        string
	Slug         string
	Date         string
	Status       string
	Tags         []string
	Description  string
	Content      string
	Layout       string
	FeatureImage string
	LayoutFields map[string]string
	// LayoutFieldOrder keeps custom fields in a stable order.
	LayoutFieldOrder []string
}

// PageInput is the data submitted from the page editor.
type PageInput struct {
	Title        string
	Slug         string
	Status       string
	Description  string
	IncludeInNav bool
	Content      string
	FeatureImage string
}

func (s *Site) slugInUse(slug, kind, original string) bool {
	loc := s.Loc()
	if kind == "post" {
		if p := s.Store.PostBySlug(loc, slug, true); p != nil && original != slug {
			return true
		}
		return s.Store.PageBySlug(loc, slug, true) != nil
	}
	if p := s.Store.PageBySlug(loc, slug, true); p != nil && original != slug {
		return true
	}
	return s.Store.PostBySlug(loc, slug, true) != nil
}

func (s *Site) uniqueSlug(slug, kind, original string) string {
	if slug == "" {
		return slug
	}
	base := slug
	for n := 2; s.slugInUse(slug, kind, original); n++ {
		slug = fmt.Sprintf("%s-%d", base, n)
	}
	return slug
}

// moveImages renames content/images/<old> to <new> and rewrites references.
func (s *Site) moveImages(oldSlug, newSlug string, content, featureImage *string) {
	if oldSlug == "" || oldSlug == newSlug {
		return
	}
	oldDir := filepath.Join(s.ImagesDir, oldSlug)
	if info, err := os.Stat(oldDir); err == nil && info.IsDir() {
		_ = os.Rename(oldDir, filepath.Join(s.ImagesDir, newSlug))
	}
	from := "/content/images/" + oldSlug + "/"
	to := "/content/images/" + newSlug + "/"
	*content = strings.ReplaceAll(*content, from, to)
	if *featureImage != "" {
		*featureImage = strings.ReplaceAll(*featureImage, from, to)
	}
}

// SavePost writes a post, returning the final slug. originalSlug is empty
// for new posts.
func (s *Site) SavePost(in PostInput, originalSlug string) (string, error) {
	s.Store.writeMu.Lock()
	defer s.Store.writeMu.Unlock()

	title := phpTrim(in.Title)
	slug := phpTrim(in.Slug)
	date := phpTrim(in.Date)
	status := phpTrim(in.Status)
	if status == "" {
		status = "draft"
	}
	content := strings.ReplaceAll(in.Content, "\r", "")
	description := phpTrim(in.Description)
	featureImage := phpTrim(in.FeatureImage)

	if slug == "" {
		slug = title
	}
	slug = s.uniqueSlug(Slugify(slug), "post", originalSlug)
	s.moveImages(originalSlug, slug, &content, &featureImage)

	if date == "" {
		date = s.NowForStorage()
	}
	prefix := s.FormatDate(date, time.DateOnly)
	if prefix == "" {
		prefix = s.FormatDate(s.NowForStorage(), time.DateOnly)
	}
	path := filepath.Join(s.PostsDir, prefix+"-"+slug+".md")

	tags := in.Tags
	if tags == nil {
		tags = []string{}
	}
	keys := []string{"title", "slug", "date", "status", "tags", "description"}
	values := map[string]any{
		"title": title, "slug": slug, "date": date, "status": status, "tags": tags, "description": description,
	}
	if layout := phpTrim(in.Layout); layout != "" {
		keys = append(keys, "layout")
		values["layout"] = layout
	}
	if featureImage != "" {
		keys = append(keys, "feature_image")
		values["feature_image"] = featureImage
	}
	for _, name := range in.LayoutFieldOrder {
		name = phpTrim(name)
		if name == "" {
			continue
		}
		if _, exists := values[name]; !exists {
			keys = append(keys, name)
		}
		values[name] = phpTrim(in.LayoutFields[name])
	}
	body := buildFrontMatter(keys, values) + "\n\n" + strings.TrimLeft(content, " \t\n\r\x00\x0B") + "\n"

	if err := os.MkdirAll(s.PostsDir, 0o755); err != nil {
		return slug, err
	}

	loc := s.Loc()
	var existingPath, renameFrom string
	if originalSlug != "" {
		if old := s.Store.PostBySlug(loc, originalSlug, true); old != nil {
			if originalSlug != slug {
				existingPath = old.Path
			} else if old.Path != path {
				renameFrom = old.Path
			}
		}
	}
	if renameFrom != "" {
		if err := os.Rename(renameFrom, path); err != nil {
			return slug, errors.New("Unable to rename post file after date change.")
		}
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return slug, errors.New("Unable to write post file.")
	}
	if existingPath != "" && existingPath != path {
		if err := os.Remove(existingPath); err != nil {
			return slug, errors.New("Post saved, but could not remove the old file. Check permissions.")
		}
	}
	s.afterContentChange(true)
	return slug, nil
}

// SavePage writes a page, returning the final slug.
func (s *Site) SavePage(in PageInput, originalSlug string) (string, error) {
	s.Store.writeMu.Lock()
	defer s.Store.writeMu.Unlock()

	title := phpTrim(in.Title)
	slug := phpTrim(in.Slug)
	status := phpTrim(in.Status)
	if status == "" {
		status = "draft"
	}
	description := phpTrim(in.Description)
	content := strings.ReplaceAll(in.Content, "\r", "")
	featureImage := phpTrim(in.FeatureImage)

	if slug == "" {
		slug = title
	}
	slug = s.uniqueSlug(Slugify(slug), "page", originalSlug)
	s.moveImages(originalSlug, slug, &content, &featureImage)

	path := filepath.Join(s.PagesDir, slug+".md")
	nav := "false"
	if in.IncludeInNav {
		nav = "true"
	}
	keys := []string{"title", "slug", "status", "description", "include_in_nav"}
	values := map[string]any{
		"title": title, "slug": slug, "status": status, "description": description, "include_in_nav": nav,
	}
	if featureImage != "" {
		keys = append(keys, "feature_image")
		values["feature_image"] = featureImage
	}
	body := buildFrontMatter(keys, values) + "\n\n" + strings.TrimLeft(content, " \t\n\r\x00\x0B") + "\n"

	if err := os.MkdirAll(s.PagesDir, 0o755); err != nil {
		return slug, err
	}
	existingPath := ""
	if originalSlug != "" && originalSlug != slug {
		candidate := filepath.Join(s.PagesDir, originalSlug+".md")
		if _, err := os.Stat(candidate); err == nil {
			existingPath = candidate
		}
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return slug, errors.New("Unable to write page file.")
	}
	if existingPath != "" && existingPath != path {
		if err := os.Remove(existingPath); err != nil {
			return slug, errors.New("Page saved, but could not remove the old file. Check permissions.")
		}
	}
	s.afterContentChange(false)
	return slug, nil
}

func (s *Site) removeImageDir(slug string) error {
	if !IsSafeImageSlug(slug) {
		return nil
	}
	dir := filepath.Join(s.ImagesDir, slug)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return os.Remove(dir)
}

// DeletePost removes a post and its images.
func (s *Site) DeletePost(slug string) bool {
	s.Store.writeMu.Lock()
	defer s.Store.writeMu.Unlock()
	p := s.Store.PostBySlug(s.Loc(), slug, true)
	if p == nil {
		return false
	}
	if err := os.Remove(p.Path); err != nil {
		return false
	}
	if err := s.removeImageDir(slug); err != nil {
		return false
	}
	s.afterContentChange(true)
	return true
}

// DeletePage removes a page and its images.
func (s *Site) DeletePage(slug string) bool {
	s.Store.writeMu.Lock()
	defer s.Store.writeMu.Unlock()
	path := filepath.Join(s.PagesDir, slug+".md")
	if !isFile(path) || !strings.HasPrefix(filepath.Clean(path), s.PagesDir+string(filepath.Separator)) {
		return false
	}
	if err := os.Remove(path); err != nil {
		return false
	}
	if err := s.removeImageDir(slug); err != nil {
		return false
	}
	s.afterContentChange(false)
	return true
}

func (s *Site) afterContentChange(tags bool) {
	_ = s.BuildSearchIndex()
	if tags {
		_ = s.BuildTagIndex()
	}
	s.CacheClear()
}

// UpdateFrontMatterField sets (or removes, when value is empty) a single
// front-matter field in place.
func (s *Site) UpdateFrontMatterField(path, field, value string) error {
	s.Store.writeMu.Lock()
	defer s.Store.writeMu.Unlock()
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return errors.New("no front matter")
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return errors.New("no front matter")
	}
	front := text[:4+end+5]
	body := text[len(front):]
	lines := strings.Split(strings.TrimSuffix(front, "\n"), "\n")
	var out []string
	found := false
	for _, l := range lines[:len(lines)-1] {
		if strings.HasPrefix(l, field+":") {
			found = true
			if value != "" {
				out = append(out, field+": "+value)
			}
			continue
		}
		out = append(out, l)
	}
	if !found && value != "" {
		out = append(out, field+": "+value)
	}
	out = append(out, "---")
	return os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"+body), 0o644)
}

// PublishScheduled publishes scheduled posts whose date has passed.
func (s *Site) PublishScheduled() int {
	loc := s.Loc()
	now := time.Now().In(loc)
	count := 0
	for _, p := range s.Store.Posts(loc, true) {
		if p.Status != "scheduled" {
			continue
		}
		t, ok := ParsePostTime(p.Date, loc)
		if !ok || t.After(now) {
			continue
		}
		in := PostInput{
			Title:        p.Title,
			Slug:         p.Slug,
			Date:         p.Date,
			Status:       "published",
			Tags:         p.Tags,
			Description:  p.Description,
			Content:      p.Content,
			Layout:       p.Layout(),
			FeatureImage: p.FeatureImage(),
			LayoutFields: map[string]string{},
		}
		for k, v := range p.Extra {
			if k == "layout" || k == "feature_image" {
				continue
			}
			in.LayoutFields[k] = v
			in.LayoutFieldOrder = append(in.LayoutFieldOrder, k)
		}
		if _, err := s.SavePost(in, p.Slug); err == nil {
			count++
		}
	}
	return count
}

// ---------------------------------------------------------------------------
// Image helpers
// ---------------------------------------------------------------------------

// IsSafeImageSlug reports whether slug can be used as an image folder name.
func IsSafeImageSlug(slug string) bool {
	if slug == "" {
		return false
	}
	for _, r := range slug {
		if !(isLetterOrNumber(r) || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// WithinDir reports whether target resolves to a path inside base.
func WithinDir(base, target string) bool {
	b, err := filepath.EvalSymlinks(base)
	if err != nil {
		return false
	}
	t, err := filepath.EvalSymlinks(target)
	if err != nil {
		return false
	}
	return strings.HasPrefix(t, b+string(filepath.Separator))
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// IsFile reports whether path exists and is a regular file.
func IsFile(path string) bool { return isFile(path) }
