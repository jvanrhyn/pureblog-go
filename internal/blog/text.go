package blog

import (
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var diacritics = strings.NewReplacer(
	"ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss",
	"à", "a", "á", "a", "â", "a", "ã", "a", "å", "a",
	"æ", "ae", "ç", "c", "è", "e", "é", "e", "ê", "e",
	"ë", "e", "ì", "i", "í", "i", "î", "i", "ï", "i",
	"ñ", "n", "ò", "o", "ó", "o", "ô", "o", "õ", "o",
	"ø", "o", "ù", "u", "ú", "u", "û", "u", "ý", "y",
	"ÿ", "y",
)

// Slugify converts a title into a URL slug.
func Slugify(value string) string {
	value = diacritics.Replace(strings.ToLower(strings.TrimSpace(value)))
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsSpace(r) || r == '-' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(collapse(b.String(), func(r rune) bool { return unicode.IsSpace(r) || r == '-' }), "-")
}

// NormalizeTag converts a tag name into its URL slug. Emoji and other
// pictographs are kept.
func NormalizeTag(tag string) string {
	tag = diacritics.Replace(strings.ToLower(strings.TrimSpace(tag)))
	var b strings.Builder
	for _, r := range tag {
		if keepTagRune(r) {
			b.WriteRune(r)
		}
	}
	return strings.Trim(collapse(b.String(), func(r rune) bool { return unicode.IsSpace(r) || r == '_' || r == '-' }), "-")
}

func keepTagRune(r rune) bool {
	switch {
	case unicode.IsLetter(r), unicode.IsNumber(r), unicode.IsMark(r), unicode.Is(unicode.So, r):
		return true
	case unicode.IsSpace(r), r == '_', r == '-':
		return true
	case r >= 0x1F3FB && r <= 0x1F3FF, r == 0x200D, r == 0xFE0E, r == 0xFE0F:
		return true
	case isExtendedPictographic(r):
		return true
	}
	return false
}

func isExtendedPictographic(r rune) bool {
	return r >= 0x1F000 && r <= 0x1FAFF || r >= 0x2600 && r <= 0x27BF || r >= 0x2300 && r <= 0x23FF ||
		r >= 0x2B00 && r <= 0x2BFF || r == 0x00A9 || r == 0x00AE || r == 0x203C || r == 0x2049 ||
		r == 0x2122 || r == 0x2139 || r >= 0x2194 && r <= 0x21AA || r >= 0x1FC00 && r <= 0x1FFFD
}

// collapse replaces runs of separator runes with a single "-".
func collapse(s string, isSep func(rune) bool) string {
	var b strings.Builder
	inSep := false
	for _, r := range s {
		if isSep(r) {
			if !inSep {
				b.WriteByte('-')
			}
			inSep = true
			continue
		}
		inSep = false
		b.WriteRune(r)
	}
	return b.String()
}

var (
	tagStripRe     = regexp.MustCompile(`(?s)<!--.*?-->|<[^>]*>`)
	fencedRe       = regexp.MustCompile("(?s)```.*?```")
	inlineCodeRe   = regexp.MustCompile("`[^`]*`")
	mdImageRe      = regexp.MustCompile(`!\[[^\]]*\]\([^)]+\)`)
	mdLinkRe       = regexp.MustCompile(`\[([^\]]*)\]\([^)]+\)`)
	listMarkerRe   = regexp.MustCompile(`(?m)^[ \t]*[-*>]+[ \t]*`)
	atxHeadingRe   = regexp.MustCompile(`(?m)^[ \t]*#{1,6}[ \t]+`)
	hrRe           = regexp.MustCompile(`(?m)^[-*_]{2,}[ \t]*$`)
	emphasisRe     = regexp.MustCompile(`[*_~]+`)
	whitespaceRe   = regexp.MustCompile(`\s+`)
	h1Re           = regexp.MustCompile(`(?i)(^|\n)(#\s+[^\n]+|[^\n]+\r?\n={3,}|<\s*h1(\s+[^>]*)?>)`)
	fencedGreedyRe = regexp.MustCompile("(?s)```.*?```")
	wordRe         = regexp.MustCompile(`[A-Za-z'-]+`)
)

// StripTags removes HTML tags and comments.
func StripTags(s string) string {
	return tagStripRe.ReplaceAllString(s, "")
}

// Excerpt builds a plain-text excerpt from Markdown, stopping at
// <!--more--> if present. Mirrors get_excerpt().
func Excerpt(markdown string, length int) string {
	excerpt, _, _ := strings.Cut(markdown, "<!--more-->")
	if utf8.RuneCountInString(excerpt) > length*6 {
		excerpt = runeSubstr(excerpt, length*6)
	}
	excerpt = fencedRe.ReplaceAllString(excerpt, " ")
	excerpt = inlineCodeRe.ReplaceAllString(excerpt, " ")
	excerpt = mdImageRe.ReplaceAllString(excerpt, " ")
	excerpt = mdLinkRe.ReplaceAllString(excerpt, "$1")
	excerpt = listMarkerRe.ReplaceAllString(excerpt, " ")
	excerpt = atxHeadingRe.ReplaceAllString(excerpt, " ")
	excerpt = hrRe.ReplaceAllString(excerpt, " ")
	excerpt = emphasisRe.ReplaceAllString(excerpt, "")
	excerpt = StripTags(excerpt)
	excerpt = strings.TrimSpace(whitespaceRe.ReplaceAllString(excerpt, " "))
	if utf8.RuneCountInString(excerpt) > length {
		return strings.TrimRightFunc(runeSubstr(excerpt, length), unicode.IsSpace) + "..."
	}
	return excerpt
}

func runeSubstr(s string, n int) string {
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}

// ContentHasH1 reports whether Markdown content contains a level-one heading.
func ContentHasH1(content string) bool {
	if strings.TrimSpace(content) == "" {
		return false
	}
	return h1Re.MatchString(fencedGreedyRe.ReplaceAllString(content, ""))
}

// WordCount approximates PHP's str_word_count().
func WordCount(s string) int {
	n := 0
	for _, w := range wordRe.FindAllString(s, -1) {
		if strings.Trim(w, "-'") != "" || strings.Contains(w, "'") {
			if strings.HasPrefix(w, "-") && strings.Trim(w, "-") == "" {
				continue
			}
			n++
		}
	}
	return n
}

// DecodeEntities converts HTML entities to their characters.
func DecodeEntities(s string) string {
	return html.UnescapeString(s)
}
