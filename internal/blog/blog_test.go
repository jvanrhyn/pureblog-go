package blog

import (
	"reflect"
	"testing"
	"time"
)

func TestPHPDate(t *testing.T) {
	tm := time.Date(2026, 3, 1, 14, 5, 9, 0, time.UTC)
	cases := map[string]string{
		"F j, Y":      "March 1, 2026",
		"Y-m-d":       "2026-03-01",
		"D, d M Y":    "Sun, 01 Mar 2026",
		"jS \\o\\f F": "1st of March",
		"g:i a":       "2:05 pm",
		"c":           "2026-03-01T14:05:09+00:00",
		"N l":         "7 Sunday",
	}
	for f, want := range cases {
		if got := PHPDate(f, tm); got != want {
			t.Errorf("PHPDate(%q) = %q, want %q", f, got, want)
		}
	}
}

func TestFrontMatter(t *testing.T) {
	doc := "---\ntitle: Hello: World\nslug: hello\ndate: 2026-01-02T10:00:00Z\nstatus: published\ntags: [Go, Web Dev]\ncategories:\n  - Misc\ndescription: |\n  Line one\n  Line two\ninclude_in_nav: yes\nlayout: photo\n---\n\nBody text\n"
	fm, body := parseDocument(doc, time.UTC)
	if body != "Body text\n" {
		t.Errorf("body = %q", body)
	}
	if v, _ := fm.String("title"); v != "Hello: World" {
		t.Errorf("title = %q", v)
	}
	if v, _ := fm.String("date"); v != "2026-01-02 10:00" {
		t.Errorf("date = %q", v)
	}
	if got := fm.list("tags"); !reflect.DeepEqual(got, []string{"Go", "Web Dev", "Misc"}) {
		t.Errorf("tags = %#v", got)
	}
	if v, _ := fm.String("description"); v != "Line one\nLine two" {
		t.Errorf("description = %q", v)
	}
	if v, _ := fm.Values["include_in_nav"].(bool); !v {
		t.Errorf("include_in_nav = %v", fm.Values["include_in_nav"])
	}
	out := buildFrontMatter([]string{"title", "tags", "description"}, map[string]any{
		"title": "T", "tags": []string{"a", "b"}, "description": "x\ny",
	})
	want := "---\ntitle: T\ntags: [a, b]\ndescription: |\n  x\n  y\n---"
	if out != want {
		t.Errorf("buildFrontMatter = %q", out)
	}
}

func TestSlugs(t *testing.T) {
	if got := Slugify("  Über Straße & Café! "); got != "ueber-strasse-cafe" {
		t.Errorf("Slugify = %q", got)
	}
	if got := NormalizeTag("📚 Reading_List"); got != "📚-reading-list" {
		t.Errorf("NormalizeTag = %q", got)
	}
}

func TestExcerpt(t *testing.T) {
	md := "# Title\n\nSome **bold** [link](http://x) text.\n\n<!--more-->\nHidden"
	if got := Excerpt(md, 200); got != "Title Some bold link text." {
		t.Errorf("Excerpt = %q", got)
	}
	if got := Excerpt("one two three four", 7); got != "one two..." {
		t.Errorf("Excerpt truncated = %q", got)
	}
}
