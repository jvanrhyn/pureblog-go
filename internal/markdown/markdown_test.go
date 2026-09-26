package markdown

import (
	"strings"
	"testing"
)

func TestRenderExtensions(t *testing.T) {
	r := New(func() string { return "example.org" })
	cases := []struct{ in, want string }{
		{"This is a notice. {.notice .warning}", `<p class="notice warning">This is a notice.</p>`},
		{"## Heading {#custom-id}", `<h2 id="custom-id">Heading</h2>`},
		{"[x](https://example.com)", `<a href="https://example.com" rel="noopener noreferrer">x</a>`},
		{"[x](https://example.org/a)", `<a href="https://example.org/a">x</a>`},
		{"[x](/local){target=\"_blank\" .btn}", `<a href="/local" target="_blank" class="btn">x</a>`},
		{"![alt](/img.png)", `<img loading="lazy" src="/img.png" alt="alt">`},
		{"a[^1]\n\n[^1]: note", `class="footnotes"`},
		{"| a | b |\n|---|---|\n| 1 | 2 |", "<table>"},
		{"<div markdown=\"1\">\nSome **bold** text.\n\nSecond para.\n</div>", "<div>\n<p>Some <strong>bold</strong> text.</p>\n<p>Second para.</p>\n</div>"},
		{"```\n<div markdown=\"1\">\n**x**\n</div>\n```", "&lt;div markdown=&quot;1&quot;&gt;"},
		{"~~gone~~", "<del>gone</del>"},
		{"<details>\n  <summary>S</summary>\n  <p>You</p>\n</details>", "<details>"},
	}
	for _, c := range cases {
		got := r.Render(c.in)
		if !strings.Contains(got, c.want) {
			t.Errorf("Render(%q)\n got: %q\nwant substring: %q", c.in, got, c.want)
		}
	}
}
