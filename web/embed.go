// Package web embeds the static assets, templates, translations, bundled
// themes and demo content that ship inside the pureblog-go binary.
package web

import "embed"

//go:embed static templates lang themes demo
var FS embed.FS
