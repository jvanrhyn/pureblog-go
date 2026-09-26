# PureBlog-Go

PureBlog-Go is a Go port of [Pure Blog](https://pureblog.org) by [Kev Quirk](https://kevquirk.com): a simple, flat-file blogging platform with a Markdown editor and a lightweight admin area. Posts and pages are Markdown files with front matter. There is no database.

This version is a single binary with no runtime dependencies. The admin area uses [htmx](https://htmx.org) for live filtering, autosave, image management and settings forms. The public site ships no JavaScript apart from htmx on the search page.

## Features

Pure Blog's feature set, ported:

- Posts and pages stored as Markdown with front matter in `content/`.
- Drafts, scheduled posts (published automatically every minute) and previews.
- Tags and tag archives, pagination, RSS feed (`/feed`) and sitemap (`/sitemap.xml`).
- Site search on a configurable search page.
- Special pages: use a page as the homepage, move the blog feed to a page, or hide it.
- An admin dashboard with writing statistics.
- A content list with search and filters by year, month, tag, status and layout.
- Image uploads per post or page, EXIF stripping (JPEG/PNG), feature images and an image library that shows where each image is used.
- Colour themes (bundled, custom-built or imported JSON), fonts, light/dark/auto modes, three post-list layouts and custom CSS for the site and the admin.
- Dynamic Open Graph banner images.
- Custom navigation, custom routes, head and footer injects, and PureComments integration.
- Two-factor authentication (TOTP plus backup codes), remember-me logins and login rate limiting.
- The page cache and the feed cache.
- The same 11 translations as the PHP version.
- Markdown Extra: tables, footnotes, definition lists, `{#id}` heading attributes and `markdown="1"` blocks. Pure Blog's `{.notice}` paragraph classes and `{target="_blank"}` link attributes also work.
- Shortcodes (`{{ site_email }}`, `{{ post_title }}` and so on) and `{% for site in site.data.NAME %}` loops over `data/NAME.yml`.

## Running

```sh
make build
./pureblog-go -dir /path/to/site -addr :8080
```

Open `http://localhost:8080/setup` to create the admin account. A new site gets the demo pages from the PHP version.

| Flag | Env | Default | |
|---|---|---|---|
| `-dir` | `PUREBLOG_GO_DIR` | `.` | Site directory (`content/`, `config/`, `data/`, `cache/`) |
| `-addr` | `PUREBLOG_GO_ADDR` | `:8080` | Listen address |
| `-trust-proxy` | `PUREBLOG_GO_TRUST_PROXY=1` | off | Trust `X-Forwarded-For/Proto/Host` from a reverse proxy |
| `-release-repo` | `PUREBLOG_GO_RELEASE_REPO` | empty | GitHub `owner/repo` to check for new releases |
| `-max-upload` | | `20` | Upload limit in MB |

Run it behind Caddy or nginx for TLS. When serving from a sub-path, set the base URL in *Settings → Site* (for example `https://example.com/blog`). With Docker, `docker build -t pureblog-go . && docker run -v $PWD/site:/site -p 8080:8080 pureblog-go`.

## Moving from the PHP version

Point `-dir` at your existing Pure Blog folder, or copy its `content/`, `config/` and `data/` folders into a new one:

1. On first start, `config/config.php` is converted to `config/config.json`. Your password, 2FA secret and backup codes keep working because PHP's bcrypt hashes are compatible. The PHP file is left untouched.
2. `content/posts`, `content/pages`, `content/images`, `content/css`, `content/themes` and `data/*.yml` are read as they are, and front matter is written in the same format.
3. Remember-me logins survive, because the cookie names and `data/remember-me.json` are unchanged.
4. Public URLs are unchanged: `/slug`, `/tag/x`, `/feed`, `/sitemap.xml` and `/og-image.php?...`. Admin URLs lose the `.php` suffix (`/admin/content`), but the old `.php` URLs still work.

### Differences from the PHP version

PHP-specific extension points can't run in Go, so they're replaced by Go templates:

- **PHP hooks are not supported.** `config/hooks.php`, `content/functions.php` and the `on_*` hooks and filters are ignored. So are admin action buttons defined by hooks.
- **Template overrides** go in `content/includes/*.html` as Go [`html/template`](https://pkg.go.dev/html/template) files instead of PHP files (see below).
- **Custom post layouts** are `content/layouts/NAME.html` (Go templates) plus the same optional `NAME.json` field definitions.
- **Custom routes** point at `content/includes/NAME.html`. A target written for PHP, such as `reading.php`, is looked up as `reading.html`.
- **Updates:** *Settings → Updates* only checks for a new release (when `-release-repo` is set). To upgrade, replace the binary; there is no in-place updater or backup restore.
- Sessions live in memory, so a restart logs you out unless you ticked "remember me".
- WebP uploads are stored as-is: Go has no WebP encoder, so their metadata isn't stripped.

## Templates

The public site is built from these named templates, each of which can be overridden:

| Template | Purpose |
|---|---|
| `header` | `<!DOCTYPE html>` through `<body>` |
| `masthead` | Site title, tagline and navigation |
| `footer` | Site footer (the footer inject is inserted before `</footer>`) |
| `post-list` | Post lists (called with `dict "Page" . "List" .List`) |
| `post-meta` | Tags, comments and previous/next links under a post |

To override one, create `content/includes/footer.html`:

```html
{{define "footer"}}
<footer>
    <p>&copy; {{.Year}} {{.SiteTitle}} · <a href="{{.BasePath}}/feed">RSS</a></p>
</footer>
{{end}}
```

Changes are picked up without a restart. Templates receive a `PublicPage` (see `internal/server/public.go`) with fields such as `.Config`, `.Post`, `.Page`, `.Content`, `.BasePath`, `.Previous` and `.Next`. Available functions include `t`, `date`, `dateFmt`, `isoDate`, `markdown`, `excerpt`, `tagLinks`, `field`, `postMeta`, `footer` and `dict`.

A custom layout (`content/layouts/photo.html`) replaces the default post body:

```html
<article class="photo">
    <h1>{{.Post.Title}}</h1>
    <p>Taken with: {{field .Post "camera"}}</p>
    {{.Content}}
    {{postMeta .}}
</article>
```

## Development

```sh
make test   # go vet + tests with the race detector
make run    # build and serve ./site on :8080
make lang   # regenerate web/lang/*.json from ../pureblog/lang/*.php
```

Layout:

```
cmd/pureblog-go      entry point
internal/blog       content model, front matter, indexes, themes, cache, PHP date formats
internal/markdown   goldmark with Pure Blog's Markdown extensions
internal/server     HTTP handlers, templates, htmx endpoints
internal/auth       sessions, CSRF, remember-me, TOTP, QR codes
internal/config     config.json storage and config.php import
internal/i18n       translations
internal/og         Open Graph image rendering
internal/phparray   parser for PHP array files (config.php, lang/*.php)
web/                embedded templates, static assets, translations, themes, demo content
```

## License

Pure Blog License v1.2. See [LICENSE.md](LICENSE.md). Pure Blog was created by Kev Quirk; PureBlog-Go keeps that attribution.
