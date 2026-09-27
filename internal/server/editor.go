package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jvanrhyn/pureblog-go/internal/auth"
	"github.com/jvanrhyn/pureblog-go/internal/blog"
)

// ImageItem is an image attached to a post or page.
type ImageItem struct {
	Slug      string
	Filename  string
	URL       string
	RawPath   string
	Markdown  string
	IsFeature bool
	ModTime   time.Time
	UsedIn    []imageUsage
}

type imageUsage struct {
	Title string
	Slug  string
	Type  string
}

var imageExts = map[string]bool{"jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true, "avif": true}

func (s *Server) imagesFor(basePath, slug, feature string) []ImageItem {
	if slug == "" || !blog.IsSafeImageSlug(slug) {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(s.site.ImagesDir, slug))
	if err != nil {
		return nil
	}
	var out []ImageItem
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := e.Name()
		alt := strings.TrimSuffix(name, filepath.Ext(name))
		if alt == "" {
			alt = "image"
		}
		u := basePath + "/content/images/" + slug + "/" + name
		raw := "/content/images/" + slug + "/" + name
		out = append(out, ImageItem{Slug: slug, Filename: name, URL: u, RawPath: raw, Markdown: "![" + alt + "](" + u + ")", IsFeature: feature == raw})
	}
	return out
}

// editorData is shared by the post and page editors.
type editorData struct {
	Type          string // "post" or "page"
	FormID        string
	IsEditing     bool
	OriginalSlug  string
	Title         string
	Slug          string
	Date          string
	Status        string
	Tags          string
	Description   string
	Content       string
	IncludeInNav  bool
	FeatureImage  string
	Layout        string
	LayoutFields  []editorField
	CustomTitle   bool
	CustomContent bool
	Images        []ImageItem
	Saved         bool
	Uploaded      bool
	UploadError   string
	Autosave      any
	Strings       map[string]string
	TagIndexVer   string
}

type editorField struct {
	Name      string
	Label     string
	Type      string
	ID        string
	InputName string
	Value     string
	Options   []string
}

func (c *ctx) editorStrings() map[string]string {
	t := c.t
	keys := map[string]string{
		"autosaving": "admin.editor.js_autosaving", "autosaved": "admin.editor.js_autosaved",
		"autosave_failed": "admin.editor.js_autosave_failed", "autosave_banner": "admin.editor.js_autosave_banner",
		"view": "admin.editor.js_view", "hide": "admin.editor.js_hide", "restore": "admin.editor.js_restore",
		"discard": "admin.editor.js_discard", "title_label": "admin.editor.js_title_label",
		"save_failed": "admin.editor.js_save_failed", "save_before_upload": "admin.editor.js_save_before_upload",
		"copied": "admin.editor.js_copied", "copy": "admin.editor.copy", "copy_failed": "admin.editor.js_copy_failed",
		"save_post_first": "admin.editor.js_save_post_first", "save_page_first": "admin.editor.js_save_page_first",
		"upload_failed": "admin.editor.js_upload_failed", "feature_image_confirm": "admin.editor.js_feature_image_confirm",
		"feature_image_failed": "admin.editor.js_feature_image_failed",
	}
	out := map[string]string{}
	for k, v := range keys {
		out[k] = t.T(v)
	}
	return out
}

func (s *Server) loadAutosave(kind, slug string, title, content string) any {
	if slug == "" {
		return nil
	}
	path := filepath.Join(s.site.AutosaveDir, kind+"-"+slug+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	if fmt.Sprint(m["content"]) != content || fmt.Sprint(m["title"]) != title {
		return m
	}
	_ = os.Remove(path)
	return nil
}

func (s *Server) removeAutosaves(kind string, slugs ...string) {
	for _, slug := range slugs {
		if slug != "" && blog.IsSafeImageSlug(slug) {
			_ = os.Remove(filepath.Join(s.site.AutosaveDir, kind+"-"+slug+".json"))
		}
	}
}

func splitTags(s string) []string {
	tags := []string{}
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

func (s *Server) tagIndexVersion() string {
	if info, err := os.Stat(filepath.Join(s.site.ContentDir, "tag-index.json")); err == nil {
		return fmt.Sprint(info.ModTime().Unix())
	}
	return s.site.Version
}

// ---------------------------------------------------------------------------
// Post editor
// ---------------------------------------------------------------------------

func (s *Server) handleEditPost(c *ctx) {
	cfg := c.cfg
	if !cfg.EnableBlogPosts {
		c.redirect(c.basePath + "/admin/content")
		return
	}
	t := c.t
	var errs []string
	slugParam := c.query("slug")
	d := &editorData{Type: "post", FormID: "editor-form", Date: s.site.NowForStorage(), Status: "draft", IsEditing: slugParam != ""}
	var existing *blog.Post
	if d.IsEditing {
		if existing = s.site.PostBySlug(slugParam, true); existing != nil {
			d.Title, d.Slug, d.Date, d.Status = existing.Title, existing.Slug, existing.Date, existing.Status
			if d.Date == "" {
				d.Date = s.site.NowForStorage()
			}
			d.Tags = strings.Join(existing.Tags, ", ")
			d.Description, d.Content = existing.Description, existing.Content
			d.Layout, d.FeatureImage = existing.Layout(), existing.FeatureImage()
			d.OriginalSlug = existing.Slug
		} else {
			errs = append(errs, t.T("admin.editor.error_not_found_post"))
			d.IsEditing = false
		}
	}
	if c.query("action") == "new" {
		d.IsEditing = false
		d.OriginalSlug = ""
		d.Layout = c.query("layout")
	}
	if d.IsEditing {
		d.Autosave = s.loadAutosave("post", d.OriginalSlug, d.Title, d.Content)
	}

	var postedFields map[string]string
	if c.r.Method == http.MethodPost {
		in := blog.PostInput{
			Title:        c.formTrim("title"),
			Slug:         c.formTrim("slug"),
			Date:         c.formTrim("date"),
			Status:       c.formTrim("status"),
			Content:      strings.TrimSpace(c.form("content")),
			Description:  c.formTrim("description"),
			Layout:       c.formTrim("post_layout"),
			FeatureImage: c.formTrim("feature_image"),
			Tags:         splitTags(c.form("tags")),
			LayoutFields: map[string]string{},
		}
		if in.Status == "" {
			in.Status = "draft"
		}
		originalSlug := c.formTrim("original_slug")
		originalStatus := c.formTrim("original_status")
		// Collect layout_field__* values in a stable order; a checkbox posts
		// a hidden "" followed by "1", so the last value wins.
		var names []string
		for key, vals := range c.r.PostForm {
			if name, ok := strings.CutPrefix(key, "layout_field__"); ok {
				names = append(names, name)
				in.LayoutFields[name] = strings.TrimSpace(vals[len(vals)-1])
			}
		}
		sort.Strings(names)
		in.LayoutFieldOrder = s.orderLayoutFields(in.Layout, names)
		postedFields = in.LayoutFields

		if in.Title == "" {
			errs = append(errs, t.T("admin.editor.error_title_required"))
		} else if in.Slug == "" && blog.Slugify(in.Title) == "" {
			errs = append(errs, t.T("admin.editor.error_empty_slug"))
		}
		if in.Status != "draft" && in.Status != "published" && in.Status != "scheduled" {
			errs = append(errs, t.T("admin.editor.error_invalid_status"))
		}
		if len(errs) == 0 {
			if in.Status == "published" && originalStatus != "published" {
				in.Date = s.site.NowForStorage()
			}
			slug, err := s.site.SavePost(in, originalSlug)
			if err == nil {
				s.removeAutosaves("post", originalSlug, slug)
				c.redirect(c.basePath + "/admin/edit-post?slug=" + url.QueryEscape(slug) + "&saved=1")
				return
			}
			errs = append(errs, err.Error())
		}
		d.Title, d.Slug, d.Date, d.Status = in.Title, in.Slug, in.Date, in.Status
		d.Tags, d.Description, d.Content = strings.Join(in.Tags, ", "), in.Description, in.Content
		d.Layout, d.FeatureImage, d.OriginalSlug = in.Layout, in.FeatureImage, originalSlug
	}

	// Layout fields.
	for _, l := range s.Layouts() {
		if l.Name != d.Layout {
			continue
		}
		for _, f := range l.Fields {
			if f.Name == "" {
				continue
			}
			ef := editorField{Name: f.Name, Label: f.Label, Type: f.Type, Options: f.Options}
			if ef.Label == "" {
				ef.Label = f.Name
			}
			switch f.Name {
			case "title":
				d.CustomTitle = true
				ef.ID, ef.InputName, ef.Value = "title", "title", d.Title
				if ef.Type == "" {
					ef.Type = "text"
				}
			case "content":
				d.CustomContent = true
				ef.ID, ef.InputName, ef.Value = "content", "content", d.Content
				if ef.Type == "" {
					ef.Type = "markdown"
				}
			default:
				ef.ID, ef.InputName = "layout_field_"+f.Name, "layout_field__"+f.Name
				if existing != nil {
					ef.Value = existing.Field(f.Name)
				}
				if v, ok := postedFields[f.Name]; ok && ef.Value == "" {
					ef.Value = v
				}
				if ef.Type == "" {
					ef.Type = "text"
				}
			}
			d.LayoutFields = append(d.LayoutFields, ef)
		}
	}

	q := c.r.URL.Query()
	d.Saved = q.Get("saved") != ""
	d.Uploaded = q.Get("uploaded") != ""
	d.UploadError = q.Get("upload_error")
	d.Images = s.imagesFor(c.basePath, d.Slug, d.FeatureImage)
	d.Strings = c.editorStrings()
	d.TagIndexVer = s.tagIndexVersion()

	title := t.T("admin.post_editor.new_title")
	if d.IsEditing {
		title = t.T("admin.post_editor.edit_title")
	}
	p := c.adminPage(title)
	p.CodeEditor = "markdown"
	p.Errors = errs
	p.Data = d
	c.renderAdmin("edit-post", p)
}

// orderLayoutFields orders custom fields as declared in the layout JSON,
// followed by any other submitted fields.
func (s *Server) orderLayoutFields(layout string, names []string) []string {
	var ordered []string
	seen := map[string]bool{}
	for _, l := range s.Layouts() {
		if l.Name != layout {
			continue
		}
		for _, f := range l.Fields {
			for _, n := range names {
				if n == f.Name && !seen[n] {
					ordered = append(ordered, n)
					seen[n] = true
				}
			}
		}
	}
	for _, n := range names {
		if !seen[n] {
			ordered = append(ordered, n)
		}
	}
	return ordered
}

// ---------------------------------------------------------------------------
// Page editor
// ---------------------------------------------------------------------------

func (s *Server) handleEditPage(c *ctx) {
	t := c.t
	var errs []string
	slugParam := c.query("slug")
	d := &editorData{Type: "page", FormID: "page-form", Status: "draft", IsEditing: slugParam != ""}
	if d.IsEditing {
		if existing := s.site.PageBySlug(slugParam, true); existing != nil {
			d.Title, d.Slug, d.Status = existing.Title, existing.Slug, existing.Status
			d.Description, d.IncludeInNav, d.Content = existing.Description, existing.IncludeInNav, existing.Content
			d.FeatureImage = existing.FeatureImage()
			d.OriginalSlug = existing.Slug
		} else {
			errs = append(errs, t.T("admin.editor.error_not_found_page"))
			d.IsEditing = false
		}
	}
	if c.query("action") == "new" {
		d.IsEditing = false
		d.OriginalSlug = ""
	}
	if d.IsEditing {
		d.Autosave = s.loadAutosave("page", d.OriginalSlug, d.Title, d.Content)
	}

	if c.r.Method == http.MethodPost {
		nav := c.form("include_in_nav")
		if nav == "" {
			nav = "yes"
		}
		in := blog.PageInput{
			Title:        c.formTrim("title"),
			Slug:         c.formTrim("slug"),
			Status:       c.formTrim("status"),
			Description:  c.formTrim("description"),
			IncludeInNav: nav == "yes" || nav == "1",
			Content:      strings.TrimSpace(c.form("content")),
			FeatureImage: c.formTrim("feature_image"),
		}
		if in.Status == "" {
			in.Status = "draft"
		}
		originalSlug := c.formTrim("original_slug")
		if in.Title == "" {
			errs = append(errs, t.T("admin.editor.error_title_required"))
		} else if in.Slug == "" && blog.Slugify(in.Title) == "" {
			errs = append(errs, t.T("admin.editor.error_empty_slug"))
		}
		if in.Status != "draft" && in.Status != "published" {
			errs = append(errs, t.T("admin.editor.error_invalid_status"))
		}
		if len(errs) == 0 {
			slug, err := s.site.SavePage(in, originalSlug)
			if err == nil {
				s.removeAutosaves("page", originalSlug, slug)
				c.redirect(c.basePath + "/admin/edit-page?slug=" + url.QueryEscape(slug) + "&saved=1")
				return
			}
			errs = append(errs, err.Error())
		}
		d.Title, d.Slug, d.Status, d.Description = in.Title, in.Slug, in.Status, in.Description
		d.IncludeInNav, d.Content, d.FeatureImage, d.OriginalSlug = in.IncludeInNav, in.Content, in.FeatureImage, originalSlug
	}

	q := c.r.URL.Query()
	d.Saved = q.Get("saved") != ""
	d.Uploaded = q.Get("uploaded") != ""
	d.UploadError = q.Get("upload_error")
	d.Images = s.imagesFor(c.basePath, d.Slug, d.FeatureImage)
	d.Strings = c.editorStrings()

	title := t.T("admin.page_editor.new_title")
	if d.IsEditing {
		title = t.T("admin.page_editor.edit_title")
	}
	p := c.adminPage(title)
	p.CodeEditor = "markdown"
	p.Errors = errs
	p.Data = d
	c.renderAdmin("edit-page", p)
}

// ---------------------------------------------------------------------------
// Delete content
// ---------------------------------------------------------------------------

type deleteData struct {
	Type  string
	Slug  string
	Title string
	Found bool
}

func (s *Server) handleDeleteContent(c *ctx) {
	kind := c.formTrim("type")
	if kind != "post" && kind != "page" {
		http.Error(c.w, "", http.StatusBadRequest)
		return
	}
	t := c.t
	slug := c.formTrim("slug")
	lookup := func() (string, bool) {
		if slug == "" {
			return "", false
		}
		if kind == "post" {
			if p := s.site.PostBySlug(slug, true); p != nil {
				return p.Title, true
			}
		} else if p := s.site.PageBySlug(slug, true); p != nil {
			return p.Title, true
		}
		return "", false
	}
	var errs []string
	title, found := lookup()
	if c.r.Method == http.MethodPost {
		switch {
		case slug == "":
			errs = append(errs, t.T("admin.delete_"+kind+".error_missing"))
		case !found:
			errs = append(errs, t.T("admin.delete_"+kind+".error_not_found"))
		default:
			ok := false
			if kind == "post" {
				ok = s.site.DeletePost(slug)
			} else {
				ok = s.site.DeletePage(slug)
			}
			if ok {
				s.removeAutosaves(kind, slug)
				tab := "pages"
				if kind == "post" {
					tab = "posts"
				}
				c.redirect(c.basePath + "/admin/content?tab=" + tab + "&deleted=1")
				return
			}
			errs = append(errs, t.T("admin.delete_"+kind+".error_delete"))
		}
		title, found = lookup()
	}
	p := c.adminPage(t.T("admin.delete_" + kind + ".page_title"))
	p.Errors = errs
	p.Data = deleteData{Type: kind, Slug: slug, Title: title, Found: found}
	c.renderAdmin("delete-content", p)
}

// ---------------------------------------------------------------------------
// Preview
// ---------------------------------------------------------------------------

func (s *Server) handlePreview(c *ctx) {
	if c.r.Method == http.MethodPost {
		kind := c.formTrim("editor_type")
		if kind == "" {
			kind = "post"
		}
		slug := c.formTrim("slug")
		fields := map[string]string{}
		for key, vals := range c.r.PostForm {
			if name, ok := strings.CutPrefix(key, "layout_field__"); ok {
				fields[name] = strings.TrimSpace(vals[len(vals)-1])
			}
		}
		key := kind + ":" + slug
		if slug == "" {
			key = kind + ":new"
		}
		c.sess.Previews[key] = auth.Preview{
			Markdown: c.form("markdown"), Title: c.formTrim("title"), Date: c.formTrim("date"),
			Tags: c.formTrim("tags"), Description: c.formTrim("description"), Layout: c.formTrim("layout"),
			LayoutFields: fields, EditorType: kind, Slug: slug, CreatedAt: time.Now(),
		}
		to := c.basePath + "/admin/preview?type=" + url.QueryEscape(kind)
		if slug != "" {
			to += "&slug=" + url.QueryEscape(slug)
		}
		c.redirect(to)
		return
	}

	kind := c.query("type")
	if kind == "" {
		kind = "post"
	}
	slug := c.query("slug")
	key := kind + ":" + slug
	if slug == "" {
		key = kind + ":new"
	}
	preview, hasPreview := c.sess.Previews[key]
	useSnapshot := hasPreview
	var markdown, title, date, tags, description, layout string
	extra := map[string]string{}
	var livePost *blog.Post
	var livePage *blog.Page
	if slug != "" {
		var mod time.Time
		if kind == "page" {
			if livePage = s.site.PageBySlug(slug, true); livePage != nil {
				mod = livePage.ModTime
			}
		} else if livePost = s.site.PostBySlug(slug, true); livePost != nil {
			mod = livePost.ModTime
		}
		if (livePost != nil || livePage != nil) && !preview.CreatedAt.After(mod) {
			useSnapshot = false
			if livePage != nil {
				title, description, markdown = livePage.Title, livePage.Description, livePage.Content
			} else {
				title, date, description, markdown = livePost.Title, livePost.Date, livePost.Description, livePost.Content
				tags = strings.Join(livePost.Tags, ", ")
				layout = livePost.Layout()
				for k, v := range livePost.Extra {
					if k != "layout" {
						extra[k] = v
					}
				}
			}
		}
	}
	if useSnapshot {
		markdown, title, date, tags = preview.Markdown, preview.Title, preview.Date, preview.Tags
		description, layout, kind = preview.Description, preview.Layout, preview.EditorType
		for k, v := range preview.LayoutFields {
			extra[k] = v
		}
	}
	if title == "" {
		title = c.t.T("admin.editor.preview_title")
	}
	c.w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	c.w.Header().Set("Pragma", "no-cache")
	set, err := s.publicTemplates()
	if set == nil {
		http.Error(c.w, err.Error(), http.StatusInternalServerError)
		return
	}
	if kind == "page" {
		page := &blog.Page{Title: title, Slug: slug, Status: "draft", Description: description, Content: markdown, Extra: map[string]string{}}
		s.renderPage(c, set, page)
		return
	}
	if layout != "" {
		extra["layout"] = layout
	}
	post := &blog.Post{Title: title, Slug: slug, Date: date, Status: "draft", Tags: splitTags(tags), Description: description, Content: markdown, Extra: extra}
	s.renderPost(c, set, post)
}

// ---------------------------------------------------------------------------
// Autosave (HTMX)
// ---------------------------------------------------------------------------

func (s *Server) handleAutosave(c *ctx) {
	t := c.t
	kind := c.formTrim("editor_type")
	if kind == "" {
		kind = "post"
	}
	slug := c.formTrim("slug")
	respond := func(ok bool, msg string) {
		if c.isHTMX() {
			if ok {
				now := time.Now().In(s.site.Loc()).Format("15:04")
				fmt.Fprint(c.w, template.HTMLEscapeString(c.t.T("admin.editor.js_autosaved")+" "+now))
			} else {
				fmt.Fprint(c.w, template.HTMLEscapeString(c.t.T("admin.editor.js_autosave_failed")))
			}
			return
		}
		c.w.Header().Set("Content-Type", "application/json")
		out := map[string]any{"success": ok}
		if !ok {
			out["error"] = msg
		}
		_ = json.NewEncoder(c.w).Encode(out)
	}
	if slug == "" || (kind != "post" && kind != "page") || !blog.IsSafeImageSlug(slug) {
		respond(false, t.T("admin.editor.error_autosave_slug"))
		return
	}
	file := filepath.Join(s.site.AutosaveDir, kind+"-"+slug+".json")
	if c.form("action") == "discard" {
		_ = os.Remove(file)
		if c.isHTMX() {
			return
		}
		respond(true, "")
		return
	}
	if err := os.MkdirAll(s.site.AutosaveDir, 0o755); err != nil {
		respond(false, t.T("admin.editor.error_autosave_write"))
		return
	}
	data := map[string]any{
		"timestamp":   time.Now().Unix(),
		"title":       c.formTrim("title"),
		"content":     strings.TrimSpace(c.form("content")),
		"description": c.formTrim("description"),
		"status":      orStr(c.formTrim("status"), "draft"),
	}
	if kind == "page" {
		data["include_in_nav"] = orStr(c.formTrim("include_in_nav"), "yes")
	} else {
		data["tags"] = c.formTrim("tags")
		data["date"] = c.formTrim("date")
		data["layout"] = c.formTrim("post_layout")
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(data)
	if err := os.WriteFile(file, bytes.TrimSpace(buf.Bytes()), 0o644); err != nil {
		respond(false, t.T("admin.editor.error_autosave_write"))
		return
	}
	respond(true, "")
}

func orStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// ---------------------------------------------------------------------------
// Images
// ---------------------------------------------------------------------------

var allowedImageTypes = map[string]string{
	"image/jpeg": "jpg", "image/png": "png", "image/gif": "gif", "image/webp": "webp", "image/avif": "avif",
}

// sniffImage detects an image's MIME type from its content.
func sniffImage(head []byte) string {
	if len(head) >= 12 && string(head[4:8]) == "ftyp" {
		brand := string(head[8:12])
		if brand == "avif" || brand == "avis" {
			return "image/avif"
		}
	}
	return http.DetectContentType(head)
}

var (
	unsafeFilenameRe = regexp.MustCompile(`[^a-zA-Z0-9._-]`)
	dashesRe         = regexp.MustCompile(`-+`)
)

// stripImageMetadata re-encodes JPEG and PNG files to drop EXIF data.
func stripImageMetadata(path, mime string) {
	if mime != "image/jpeg" && mime != "image/png" {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	img, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		return
	}
	var buf bytes.Buffer
	if mime == "image/jpeg" {
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	} else {
		err = png.Encode(&buf, img)
	}
	if err == nil {
		_ = os.WriteFile(path, buf.Bytes(), 0o644)
	}
}

func (s *Server) handleUploadImage(c *ctx) {
	t := c.t
	slug := c.formTrim("slug")
	kind := c.formTrim("editor_type")
	var message, errMsg string
	var mime string

	file, header, ferr := c.r.FormFile("image")
	switch {
	case slug == "":
		errMsg = t.T("admin.editor.error_upload_no_slug")
	case ferr != nil:
		errMsg = t.T("admin.editor.error_upload_no_file")
	}
	var head []byte
	if errMsg == "" {
		defer file.Close()
		head = make([]byte, 512)
		n, _ := io.ReadFull(file, head)
		head = head[:n]
		mime = sniffImage(head)
		if _, ok := allowedImageTypes[mime]; !ok {
			errMsg = t.T("admin.editor.error_upload_type")
		}
	}
	if errMsg == "" && !blog.IsSafeImageSlug(slug) {
		errMsg = t.T("admin.editor.error_upload_invalid_slug")
	}
	uploadDir := filepath.Join(s.site.ImagesDir, slug)
	if errMsg == "" {
		if err := os.MkdirAll(uploadDir, 0o755); err != nil {
			errMsg = t.T("admin.editor.error_upload_folder_create")
		} else if !blog.WithinDir(s.site.ImagesDir, uploadDir) {
			errMsg = t.T("admin.editor.error_image_invalid_path")
		}
	}
	if errMsg == "" {
		name := filepath.Base(header.Filename)
		name = strings.Trim(dashesRe.ReplaceAllString(unsafeFilenameRe.ReplaceAllString(name, "-"), "-"), "-")
		base := strings.ReplaceAll(strings.TrimSuffix(name, filepath.Ext(name)), ".", "-")
		filename := base + "." + allowedImageTypes[mime]
		dest := filepath.Join(uploadDir, filename)
		switch {
		case base == "":
			errMsg = t.T("admin.editor.error_upload_invalid_name")
		case blog.IsFile(dest):
			errMsg = t.T("admin.editor.error_upload_duplicate", "filename", filename)
		default:
			out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err == nil {
				_, err = io.Copy(out, io.MultiReader(bytes.NewReader(head), file))
				if cerr := out.Close(); err == nil {
					err = cerr
				}
			}
			if err != nil {
				_ = os.Remove(dest)
				errMsg = t.T("admin.editor.error_upload_save")
			} else {
				stripImageMetadata(dest, mime)
				u := c.basePath + "/content/images/" + slug + "/" + filename
				alt := strings.TrimSuffix(filename, filepath.Ext(filename))
				message = "![" + alt + "](" + u + ")"
			}
		}
	}

	to := c.basePath + "/admin/edit-post?slug=" + url.QueryEscape(slug)
	if kind == "page" {
		to = c.basePath + "/admin/edit-page?slug=" + url.QueryEscape(slug)
	}
	if message != "" {
		to += "&uploaded=" + url.QueryEscape(message)
	} else if errMsg != "" {
		to += "&upload_error=" + url.QueryEscape(errMsg)
	}
	http.Redirect(c.w, c.r, to, http.StatusSeeOther)
}

// imageListData is the editor sidebar's image list, re-rendered by HTMX.
type imageListData struct {
	Type         string
	Slug         string
	Date         string
	FeatureImage string
	Images       []ImageItem
	CSRF         string
	BasePath     string
	Error        string
	OOB          bool
}

func (s *Server) renderImageList(c *ctx, kind, slug, date, feature, errMsg string) {
	d := imageListData{Type: kind, Slug: slug, Date: date, FeatureImage: feature, CSRF: c.sess.CSRF(), BasePath: c.basePath, Error: errMsg, OOB: true}
	d.Images = s.imagesFor(c.basePath, slug, feature)
	c.render(s.adminSet, "image-list", d, 0)
}

func (s *Server) currentFeature(kind, slug string) string {
	if kind == "page" {
		if p := s.site.PageBySlug(slug, true); p != nil {
			return p.FeatureImage()
		}
		return ""
	}
	if p := s.site.PostBySlug(slug, true); p != nil {
		return p.FeatureImage()
	}
	return ""
}

func (s *Server) deleteImageFile(slug, filename string) string {
	t := s.site.T()
	if slug == "" || filename == "" {
		return t.T("admin.editor.error_delete_missing_data")
	}
	if !blog.IsSafeImageSlug(slug) {
		return t.T("admin.editor.error_image_invalid_path")
	}
	dir := filepath.Join(s.site.ImagesDir, slug)
	target := filepath.Join(dir, filepath.Base(filename))
	if !blog.WithinDir(s.site.ImagesDir, dir) || !blog.WithinDir(s.site.ImagesDir, target) {
		if !blog.IsFile(target) {
			return t.T("admin.editor.error_image_not_found")
		}
		return t.T("admin.editor.error_image_invalid_path")
	}
	if !blog.IsFile(target) {
		return t.T("admin.editor.error_image_not_found")
	}
	if err := os.Remove(target); err != nil {
		return t.T("admin.editor.error_delete_failed")
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		_ = os.Remove(dir)
	}
	return ""
}

func (s *Server) handleDeleteImage(c *ctx) {
	slug := c.formTrim("slug")
	kind := c.formTrim("editor_type")
	if kind != "page" {
		kind = "post"
	}
	errMsg := s.deleteImageFile(slug, c.formTrim("filename"))
	if c.isHTMX() {
		s.renderImageList(c, kind, slug, c.formTrim("date"), s.currentFeature(kind, slug), errMsg)
		return
	}
	to := c.basePath + "/admin/edit-" + kind + "?slug=" + url.QueryEscape(slug)
	if errMsg != "" {
		to += "&upload_error=" + url.QueryEscape(errMsg)
	}
	c.redirect(to)
}

func (s *Server) handleDeleteImageLibrary(c *ctx) {
	errMsg := s.deleteImageFile(c.formTrim("slug"), c.formTrim("filename"))
	back := c.formTrim("return")
	if !strings.HasPrefix(back, c.basePath+"/admin/images") {
		back = c.basePath + "/admin/images"
	}
	sep := "?"
	if strings.Contains(back, "?") {
		sep = "&"
	}
	if errMsg != "" {
		c.redirect(back + sep + "delete_error=" + url.QueryEscape(errMsg))
		return
	}
	c.redirect(back + sep + "deleted=1")
}

func (s *Server) handleSetFeatureImage(c *ctx) {
	t := c.t
	slug := c.formTrim("slug")
	kind := c.formTrim("editor_type")
	filename := c.formTrim("filename")
	fail := func(msg string) {
		if c.isHTMX() {
			s.renderImageList(c, kind, slug, c.formTrim("date"), s.currentFeature(kind, slug), msg)
			return
		}
		c.w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(c.w).Encode(map[string]any{"success": false, "error": msg})
	}
	if slug == "" || (kind != "post" && kind != "page") {
		fail(t.T("admin.editor.error_autosave_slug"))
		return
	}
	if !blog.IsSafeImageSlug(slug) {
		fail(t.T("admin.editor.error_image_invalid_path"))
		return
	}
	var path string
	if kind == "post" {
		if p := s.site.PostBySlug(slug, true); p != nil {
			path = p.Path
		}
	} else if candidate := filepath.Join(s.site.PagesDir, slug+".md"); blog.IsFile(candidate) {
		path = candidate
	}
	if path == "" {
		fail(t.T("admin.editor.error_not_found_" + kind))
		return
	}
	value := ""
	if filename != "" {
		clean := filepath.Base(filename)
		if !imageExts[strings.ToLower(strings.TrimPrefix(filepath.Ext(clean), "."))] {
			fail(t.T("admin.editor.error_image_invalid_path"))
			return
		}
		imgPath := filepath.Join(s.site.ImagesDir, slug, clean)
		if !blog.IsFile(imgPath) || !blog.WithinDir(s.site.ImagesDir, imgPath) {
			fail(t.T("admin.editor.error_image_not_found"))
			return
		}
		value = "/content/images/" + slug + "/" + clean
	}
	if err := s.site.UpdateFrontMatterField(path, "feature_image", value); err != nil {
		fail(t.T("admin.editor.js_feature_image_failed"))
		return
	}
	if c.isHTMX() {
		s.renderImageList(c, kind, slug, c.formTrim("date"), value, "")
		return
	}
	c.w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(c.w).Encode(map[string]any{"success": true})
}

// ---------------------------------------------------------------------------
// Image library
// ---------------------------------------------------------------------------

type imagesData struct {
	Search      string
	Deleted     bool
	DeleteError string
	Total       int
	Images      []ImageItem
	PrevURL     string
	NextURL     string
	ReturnURL   string
}

var imageRefRe = regexp.MustCompile(`/content/images/([^/\s\)'"]+)/([^\s\)'"]+)`)

func (s *Server) handleImages(c *ctx) {
	q := c.r.URL.Query()
	d := imagesData{Search: strings.TrimSpace(q.Get("q")), Deleted: q.Has("deleted"), DeleteError: strings.TrimSpace(q.Get("delete_error"))}
	var all []ImageItem
	folders, _ := os.ReadDir(s.site.ImagesDir)
	for _, folder := range folders {
		if !folder.IsDir() || !blog.IsSafeImageSlug(folder.Name()) {
			continue
		}
		for _, img := range s.imagesFor(c.basePath, folder.Name(), "") {
			if !imageExts[strings.ToLower(strings.TrimPrefix(filepath.Ext(img.Filename), "."))] {
				continue
			}
			if d.Search != "" && !strings.Contains(strings.ToLower(img.Filename), strings.ToLower(d.Search)) {
				continue
			}
			if info, err := os.Stat(filepath.Join(s.site.ImagesDir, img.Slug, img.Filename)); err == nil {
				img.ModTime = info.ModTime()
			}
			all = append(all, img)
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].ModTime.After(all[j].ModTime) })
	d.Total = len(all)

	const perPage = 32
	page := 1
	fmt.Sscan(q.Get("page"), &page)
	totalPages := 1
	if len(all) > 0 {
		totalPages = (len(all) + perPage - 1) / perPage
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * perPage
	end := start + perPage
	if end > len(all) {
		end = len(all)
	}
	d.Images = all[start:end]

	// Which posts/pages reference each image on this page?
	usage := map[string][]imageUsage{}
	for _, src := range []struct{ dir, kind string }{{s.site.PostsDir, "post"}, {s.site.PagesDir, "page"}} {
		files, _ := filepath.Glob(filepath.Join(src.dir, "*.md"))
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			matches := imageRefRe.FindAllStringSubmatch(string(raw), -1)
			if len(matches) == 0 {
				continue
			}
			fm, _, _ := blog.ParseFile(f, s.site.Loc())
			title, ok := fm.String("title")
			if !ok || title == "" {
				title = filepath.Base(f)
			}
			slug, _ := fm.String("slug")
			seen := map[string]bool{}
			for _, m := range matches {
				key := m[1] + "/" + m[2]
				if !seen[key] {
					seen[key] = true
					usage[key] = append(usage[key], imageUsage{Title: title, Slug: slug, Type: src.kind})
				}
			}
		}
	}
	for i := range d.Images {
		d.Images[i].UsedIn = usage[d.Images[i].Slug+"/"+d.Images[i].Filename]
	}
	link := func(n int) string {
		v := url.Values{}
		if d.Search != "" {
			v.Set("q", d.Search)
		}
		v.Set("page", fmt.Sprint(n))
		return c.basePath + "/admin/images?" + v.Encode()
	}
	if totalPages > 1 {
		if page > 1 {
			d.PrevURL = link(page - 1)
		}
		if page < totalPages {
			d.NextURL = link(page + 1)
		}
	}
	d.ReturnURL = c.basePath + "/admin/images"
	if d.Search != "" || page > 1 {
		d.ReturnURL = link(page)
	}
	p := c.adminPage(c.t.T("admin.images.page_title"))
	p.Data = d
	c.renderAdmin("images", p)
}
