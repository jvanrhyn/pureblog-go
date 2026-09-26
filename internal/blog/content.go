package blog

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Post is a blog post stored in content/posts.
type Post struct {
	Title       string
	Slug        string
	Date        string
	Timestamp   int64
	Status      string
	Tags        []string
	Description string
	Content     string
	Path        string
	ModTime     time.Time
	// Extra holds any other front-matter fields (layout, feature_image,
	// custom layout fields, ...).
	Extra map[string]string
}

// Field returns an extra front-matter value.
func (p *Post) Field(name string) string { return p.Extra[name] }

// Layout returns the custom layout name, if any.
func (p *Post) Layout() string { return p.Extra["layout"] }

// FeatureImage returns the feature image path, if any.
func (p *Post) FeatureImage() string { return p.Extra["feature_image"] }

// Page is a static page stored in content/pages.
type Page struct {
	Title        string
	Slug         string
	Status       string
	Description  string
	IncludeInNav bool
	Content      string
	Path         string
	ModTime      time.Time
	Extra        map[string]string
}

// Field returns an extra front-matter value.
func (p *Page) Field(name string) string { return p.Extra[name] }

// FeatureImage returns the feature image path, if any.
func (p *Page) FeatureImage() string { return p.Extra["feature_image"] }

// ---------------------------------------------------------------------------
// Front matter
// ---------------------------------------------------------------------------

// FrontMatter is an ordered set of front-matter values. Values are string,
// []string (tags/categories) or bool (include_in_nav).
type FrontMatter struct {
	Keys   []string
	Values map[string]any
}

func newFrontMatter() *FrontMatter {
	return &FrontMatter{Values: map[string]any{}}
}

func (f *FrontMatter) set(key string, v any) {
	if _, ok := f.Values[key]; !ok {
		f.Keys = append(f.Keys, key)
	}
	f.Values[key] = v
}

// String returns a string value, or "" when missing or not a string.
func (f *FrontMatter) String(key string) (string, bool) {
	v, ok := f.Values[key]
	if !ok {
		return "", false
	}
	s, isStr := v.(string)
	return s, isStr
}

func (f *FrontMatter) list(key string) []string {
	l, _ := f.Values[key].([]string)
	return l
}

var (
	blockLineRe = regexp.MustCompile(`^(\s+)(.*)$`)
	listItemRe  = regexp.MustCompile(`^\s*-\s*(.+)$`)
)

// parseFrontMatterLines mirrors _parse_front_matter_lines().
func parseFrontMatterLines(lines []string, loc *time.Location) *FrontMatter {
	fm := newFrontMatter()
	listKey := ""
	blockKey := ""
	var blockLines []string
	blockIndent := 0

	flushBlock := func() {
		fm.set(blockKey, strings.Join(blockLines, "\n"))
		blockKey, blockLines, blockIndent = "", nil, 0
	}

	for _, line := range lines {
		if blockKey != "" {
			if strings.TrimRight(line, " \t\n\r\x00\x0B") == "" {
				blockLines = append(blockLines, "")
				continue
			}
			if m := blockLineRe.FindStringSubmatch(line); m != nil {
				indent := len(m[1])
				if blockIndent == 0 {
					blockIndent = indent
				}
				if indent >= blockIndent {
					blockLines = append(blockLines, line[blockIndent:])
					continue
				}
			}
			flushBlock()
		}

		trimmed := strings.TrimRight(line, " \t\n\r\x00\x0B")
		if trimmed == "" {
			continue
		}

		if listKey != "" {
			if m := listItemRe.FindStringSubmatch(trimmed); m != nil {
				item := strings.Trim(m[1], " \t\"'")
				if item != "" {
					fm.set(listKey, append(fm.list(listKey), item))
				}
				continue
			}
			listKey = ""
		}

		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key, value = phpTrim(key), phpTrim(value)
		if key == "" {
			continue
		}

		if value == "|" {
			blockKey, blockLines, blockIndent = key, nil, 0
			continue
		}

		if value == "" {
			if key == "tags" || key == "categories" {
				listKey = key
				if _, exists := fm.Values[key]; !exists {
					fm.set(key, []string{})
				}
				continue
			}
			fm.set(key, "")
			continue
		}

		switch key {
		case "date":
			value = strings.Trim(value, "\"'")
			fm.set(key, normalizeDateValue(value, loc))
		case "tags", "categories":
			value = strings.Trim(value, "[] ")
			var tags []string
			if value != "" {
				for _, t := range strings.Split(value, ",") {
					if t = phpTrim(t); t != "" {
						tags = append(tags, t)
					}
				}
			}
			if tags == nil {
				tags = []string{}
			}
			fm.set(key, tags)
		case "include_in_nav":
			switch strings.ToLower(value) {
			case "1", "true", "yes", "on":
				fm.set(key, true)
			default:
				fm.set(key, false)
			}
		default:
			fm.set(key, value)
		}
	}
	if blockKey != "" {
		flushBlock()
	}

	if cats := fm.list("categories"); len(cats) > 0 {
		merged := append([]string(nil), fm.list("tags")...)
		seen := map[string]bool{}
		for _, t := range merged {
			seen[t] = true
		}
		for _, c := range cats {
			if !seen[c] {
				seen[c] = true
				merged = append(merged, c)
			}
		}
		fm.set("tags", merged)
	}
	return fm
}

// phpTrim trims the characters PHP's trim() removes by default.
func phpTrim(s string) string {
	return strings.Trim(s, " \t\n\r\x00\x0B")
}

// buildFrontMatter mirrors _build_front_matter_lines().
func buildFrontMatter(keys []string, values map[string]any) string {
	lines := []string{"---"}
	for _, k := range keys {
		switch v := values[k].(type) {
		case []string:
			lines = append(lines, k+": ["+strings.Join(v, ", ")+"]")
		case string:
			if strings.Contains(v, "\n") {
				lines = append(lines, k+": |")
				for _, l := range strings.Split(strings.ReplaceAll(v, "\r", ""), "\n") {
					lines = append(lines, "  "+l)
				}
			} else {
				lines = append(lines, k+": "+v)
			}
		case bool:
			if v {
				lines = append(lines, k+": 1")
			} else {
				lines = append(lines, k+": ")
			}
		}
	}
	lines = append(lines, "---")
	return strings.Join(lines, "\n")
}

// ParseFile reads a Markdown file with front matter (parse_post_file).
func ParseFile(path string, loc *time.Location) (*FrontMatter, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return newFrontMatter(), "", err
	}
	fm, body := parseDocument(string(raw), loc)
	return fm, body, nil
}

func parseDocument(raw string, loc *time.Location) (*FrontMatter, string) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	fm := newFrontMatter()
	content := raw
	if strings.HasPrefix(raw, "---\n") {
		if head, body, ok := strings.Cut(raw, "\n---\n"); ok {
			fm = parseFrontMatterLines(strings.Split(strings.Trim(head, "-\n"), "\n"), loc)
			content = body
		}
	}
	return fm, strings.TrimLeft(content, " \t\n\r\x00\x0B")
}

// parseMetaOnly reads only the front matter block (parse_post_meta_only).
func parseMetaOnly(path string, loc *time.Location) *FrontMatter {
	f, err := os.Open(path)
	if err != nil {
		return newFrontMatter()
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	if !sc.Scan() || strings.TrimRight(sc.Text(), " \t\r\n") != "---" {
		return newFrontMatter()
	}
	var lines []string
	for sc.Scan() {
		t := strings.TrimRight(sc.Text(), " \t\n\r\x00\x0B")
		if t == "---" {
			break
		}
		lines = append(lines, t)
	}
	return parseFrontMatterLines(lines, loc)
}

// ---------------------------------------------------------------------------
// Content store
// ---------------------------------------------------------------------------

type cachedFile struct {
	mod     time.Time
	size    int64
	tz      string
	fm      *FrontMatter
	content string
}

// Store reads posts and pages from disk, re-parsing only files whose
// modification time or size changed since the last read.
type Store struct {
	mu       sync.Mutex
	postsDir string
	pagesDir string
	files    map[string]*cachedFile
	// writeMu serialises write operations (save, delete, rename).
	writeMu sync.Mutex
}

// NewStore creates a store for the given content directory.
func NewStore(contentDir string) *Store {
	return &Store{
		postsDir: filepath.Join(contentDir, "posts"),
		pagesDir: filepath.Join(contentDir, "pages"),
		files:    map[string]*cachedFile{},
	}
}

func (s *Store) load(dir string, loc *time.Location) []*cachedFileWithPath {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	sort.Strings(matches)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*cachedFileWithPath, 0, len(matches))
	seen := map[string]bool{}
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		seen[path] = true
		cf := s.files[path]
		if cf == nil || !cf.mod.Equal(info.ModTime()) || cf.size != info.Size() || cf.tz != loc.String() {
			fm, body, err := ParseFile(path, loc)
			if err != nil {
				continue
			}
			cf = &cachedFile{mod: info.ModTime(), size: info.Size(), tz: loc.String(), fm: fm, content: body}
			s.files[path] = cf
		}
		out = append(out, &cachedFileWithPath{cachedFile: cf, path: path})
	}
	for path := range s.files {
		if filepath.Dir(path) == dir && !seen[path] {
			delete(s.files, path)
		}
	}
	return out
}

type cachedFileWithPath struct {
	*cachedFile
	path string
}

var postKnownKeys = map[string]bool{"title": true, "slug": true, "date": true, "status": true, "tags": true, "description": true, "categories": true}
var pageKnownKeys = map[string]bool{"title": true, "slug": true, "status": true, "description": true, "include_in_nav": true, "categories": true}

func extras(fm *FrontMatter, known map[string]bool) map[string]string {
	out := map[string]string{}
	for _, k := range fm.Keys {
		if known[k] {
			continue
		}
		switch v := fm.Values[k].(type) {
		case string:
			out[k] = v
		case bool:
			if v {
				out[k] = "1"
			} else {
				out[k] = ""
			}
		case []string:
			out[k] = strings.Join(v, ", ")
		}
	}
	return out
}

func strOr(fm *FrontMatter, key, def string) string {
	if v, ok := fm.Values[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		return def
	}
	return def
}

// Posts returns posts sorted newest first. Drafts and scheduled posts are
// included only when includeDrafts is true.
func (s *Store) Posts(loc *time.Location, includeDrafts bool) []*Post {
	files := s.load(s.postsDir, loc)
	posts := make([]*Post, 0, len(files))
	for _, f := range files {
		fm := f.fm
		status := strOr(fm, "status", "draft")
		if !includeDrafts && status != "published" {
			continue
		}
		date := strOr(fm, "date", "")
		var ts int64
		if t, ok := ParsePostTime(date, loc); ok {
			ts = t.Unix()
		}
		tags := fm.list("tags")
		if tags == nil {
			tags = []string{}
		}
		posts = append(posts, &Post{
			Title:       strOr(fm, "title", "Untitled"),
			Slug:        strOr(fm, "slug", ""),
			Date:        date,
			Timestamp:   ts,
			Status:      status,
			Tags:        tags,
			Description: strOr(fm, "description", ""),
			Content:     f.content,
			Path:        f.path,
			ModTime:     f.mod,
			Extra:       extras(fm, postKnownKeys),
		})
	}
	sort.SliceStable(posts, func(i, j int) bool { return posts[i].Timestamp > posts[j].Timestamp })
	return posts
}

// Pages returns pages sorted by title.
func (s *Store) Pages(loc *time.Location, includeDrafts bool) []*Page {
	files := s.load(s.pagesDir, loc)
	pages := make([]*Page, 0, len(files))
	for _, f := range files {
		fm := f.fm
		status := strOr(fm, "status", "draft")
		if !includeDrafts && status != "published" {
			continue
		}
		nav := true
		if v, ok := fm.Values["include_in_nav"].(bool); ok {
			nav = v
		}
		pages = append(pages, &Page{
			Title:        strOr(fm, "title", "Untitled"),
			Slug:         strOr(fm, "slug", ""),
			Status:       status,
			Description:  strOr(fm, "description", ""),
			IncludeInNav: nav,
			Content:      f.content,
			Path:         f.path,
			ModTime:      f.mod,
			Extra:        extras(fm, pageKnownKeys),
		})
	}
	sort.SliceStable(pages, func(i, j int) bool { return pages[i].Title < pages[j].Title })
	return pages
}

// PostBySlug finds a post by slug.
func (s *Store) PostBySlug(loc *time.Location, slug string, includeDrafts bool) *Post {
	for _, p := range s.Posts(loc, includeDrafts) {
		if p.Slug == slug {
			return p
		}
	}
	return nil
}

// PageBySlug finds a page by slug.
func (s *Store) PageBySlug(loc *time.Location, slug string, includeDrafts bool) *Page {
	for _, p := range s.Pages(loc, includeDrafts) {
		if p.Slug == slug {
			return p
		}
	}
	return nil
}

// Adjacent returns the previous (older) and next (newer) published posts.
func (s *Store) Adjacent(loc *time.Location, slug string) (prev, next *Post) {
	posts := s.Posts(loc, false)
	for i, p := range posts {
		if p.Slug != slug {
			continue
		}
		if i+1 < len(posts) {
			prev = posts[i+1]
		}
		if i > 0 {
			next = posts[i-1]
		}
		return prev, next
	}
	return nil, nil
}
