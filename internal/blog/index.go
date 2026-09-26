package blog

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// SearchEntry is one record in content/search-index.json.
type SearchEntry struct {
	Title       string   `json:"title"`
	Slug        string   `json:"slug"`
	Date        string   `json:"date"`
	Tags        []string `json:"tags"`
	Description string   `json:"description"`
	Excerpt     string   `json:"excerpt"`
	IsPage      bool     `json:"is_page"`
}

// TagEntry is one record in content/tag-index.json.
type TagEntry struct {
	Name  string   `json:"name"`
	Posts []string `json:"posts"`
}

func writeJSON(path string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, bytes.TrimRight(buf.Bytes(), "\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SearchIndex builds the search entries for published posts (and pages,
// when enabled).
func (s *Site) SearchIndex() []SearchEntry {
	cfg := s.Config.Get()
	length := cfg.SearchExcerptLength
	excerpt := func(content string) string {
		if length == 0 {
			return content
		}
		return Excerpt(content, length)
	}
	var out []SearchEntry
	for _, p := range s.Posts(false) {
		out = append(out, SearchEntry{
			Title: p.Title, Slug: p.Slug, Date: p.Date, Tags: p.Tags,
			Description: p.Description, Excerpt: excerpt(p.Content),
		})
	}
	if cfg.SearchIncludePages {
		for _, p := range s.Pages(false) {
			out = append(out, SearchEntry{
				Title: p.Title, Slug: p.Slug, Tags: []string{},
				Description: p.Description, Excerpt: excerpt(p.Content), IsPage: true,
			})
		}
	}
	if out == nil {
		out = []SearchEntry{}
	}
	return out
}

// BuildSearchIndex writes content/search-index.json.
func (s *Site) BuildSearchIndex() error {
	return writeJSON(filepath.Join(s.ContentDir, "search-index.json"), s.SearchIndex())
}

// orderedTags keeps tag-index.json keys in first-seen order, like PHP.
type orderedTags struct {
	keys []string
	m    map[string]*TagEntry
}

func (o orderedTags) MarshalJSON() ([]byte, error) {
	if len(o.keys) == 0 {
		return []byte("[]"), nil
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := marshalNoEscape(k)
		vb, _ := marshalNoEscape(o.m[k])
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	err := enc.Encode(v)
	return bytes.TrimRight(buf.Bytes(), "\n"), err
}

// BuildTagIndex writes content/tag-index.json.
func (s *Site) BuildTagIndex() error {
	idx := orderedTags{m: map[string]*TagEntry{}}
	for _, p := range s.Posts(false) {
		if p.Slug == "" {
			continue
		}
		for _, tag := range p.Tags {
			tag = phpTrim(tag)
			slug := NormalizeTag(tag)
			if slug == "" {
				continue
			}
			e, ok := idx.m[slug]
			if !ok {
				e = &TagEntry{Name: tag}
				idx.m[slug] = e
				idx.keys = append(idx.keys, slug)
			}
			e.Posts = append(e.Posts, p.Slug)
		}
	}
	return writeJSON(filepath.Join(s.ContentDir, "tag-index.json"), idx)
}

// LoadSearchIndex reads content/search-index.json, if present.
func (s *Site) LoadSearchIndex() ([]SearchEntry, bool) {
	raw, err := os.ReadFile(filepath.Join(s.ContentDir, "search-index.json"))
	if err != nil {
		return nil, false
	}
	var out []SearchEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false
	}
	return out, true
}

// Searchable is anything that can be matched by a search query.
type Searchable interface {
	SearchText() string
}

// SearchText implements Searchable.
func (e SearchEntry) SearchText() string {
	return strings.Join([]string{e.Title, e.Description, e.Excerpt, strings.Join(e.Tags, " ")}, " ")
}

// SearchText implements Searchable.
func (p *Post) SearchText() string {
	return strings.Join([]string{p.Title, p.Description, strings.Join(p.Tags, " ")}, " ")
}

// MatchesQuery reports whether text contains every word of query,
// ignoring case and symbols such as emoji (filter_posts_by_query).
func MatchesQuery(text string, words []string) bool {
	haystack := strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsPunct(r) || unicode.IsSpace(r) {
			return r
		}
		return ' '
	}, text))
	for _, w := range words {
		if !strings.Contains(haystack, w) {
			return false
		}
	}
	return true
}

// QueryWords splits a search query into lower-case words.
func QueryWords(query string) []string {
	return strings.Fields(strings.ToLower(strings.TrimSpace(query)))
}

// ---------------------------------------------------------------------------
// Page cache
// ---------------------------------------------------------------------------

func (s *Site) cachePath(key, ext string) string {
	sum := md5.Sum([]byte(key))
	return filepath.Join(s.CacheDir, hex.EncodeToString(sum[:])+"."+ext)
}

// CacheRead returns cached content for key, or false. A ttl of 0 means the
// entry never expires.
func (s *Site) CacheRead(key string, ttl time.Duration, ext string) ([]byte, bool) {
	path := s.cachePath(key, ext)
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	if ttl > 0 && time.Since(info.ModTime()) > ttl {
		_ = os.Remove(path)
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

// CacheWrite stores content for key, appending a "Cached at" comment.
func (s *Site) CacheWrite(key string, content []byte, ext string) {
	if err := os.MkdirAll(s.CacheDir, 0o755); err != nil {
		return
	}
	stamp := time.Now().UTC().Format("2006-01-02 15:04:05") + " UTC"
	var out []byte
	if ext == "xml" {
		out = bytes.Replace(content, []byte("</rss>"), []byte("<!-- Cached at "+stamp+" -->\n</rss>"), 1)
	} else {
		out = append(append([]byte{}, content...), []byte("\n<!-- Cached at "+stamp+" -->")...)
	}
	tmp := s.cachePath(key, ext) + ".tmp"
	if os.WriteFile(tmp, out, 0o644) == nil {
		_ = os.Rename(tmp, s.cachePath(key, ext))
	}
}

// CacheClear removes all cached pages, feeds and OG images.
func (s *Site) CacheClear() {
	for _, dir := range []string{s.CacheDir, filepath.Join(s.CacheDir, "og")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
}

func isLetterOrNumber(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}
