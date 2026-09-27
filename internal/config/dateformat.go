package config

import (
	"fmt"
	"strings"
	"time"
)

// DefaultDateFormat is the post date layout used when none is configured.
// Date formats are Go reference-time layouts (see the time package).
const DefaultDateFormat = "January 2, 2006"

// probeTime differs from the reference time in every field, so formatting it
// with a string changes the string only if it holds a layout element.
var probeTime = time.Date(2001, time.November, 24, 21, 33, 44, 0, time.FixedZone("XST", 5*3600))

// IsLayout reports whether s is a Go time layout: it must contain at least
// one reference-time element such as 2006, Jan or 15.
func IsLayout(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && probeTime.Format(s) != s
}

// phpToGo maps PHP date() codes to Go layout elements. Codes that are not
// listed (S, N, w, z, W, t, L, o, I, Z, U, B) have no Go equivalent.
var phpToGo = map[byte]string{
	'd': "02", 'D': "Mon", 'j': "2", 'l': "Monday",
	'F': "January", 'm': "01", 'M': "Jan", 'n': "1",
	'Y': "2006", 'y': "06",
	'a': "pm", 'A': "PM", 'g': "3", 'G': "15", 'h': "03", 'H': "15",
	'i': "04", 's': "05",
	'e': "MST", 'T': "MST", 'O': "-0700", 'P': "-07:00", 'p': "Z07:00",
	'c': "2006-01-02T15:04:05-07:00", 'r': "Mon, 02 Jan 2006 15:04:05 -0700",
}

// LayoutFromPHP converts a PHP date() format, as used by the PHP version of
// Pure Blog, to the closest Go layout. PHP codes with no Go equivalent and
// literal digits (which Go would read as layout elements) are left out and
// returned in dropped.
func LayoutFromPHP(php string) (layout string, dropped []string) {
	var b strings.Builder
	for i := 0; i < len(php); i++ {
		c := php[i]
		switch {
		case c == '\\' && i+1 < len(php):
			i++
			if lit := php[i]; lit >= '0' && lit <= '9' {
				dropped = append(dropped, string(lit))
			} else {
				b.WriteByte(lit)
			}
		case c == 'u' || c == 'v':
			// Fractional seconds only work in Go after a '.' or ','.
			out := b.String()
			if n := len(out); n > 0 && (out[n-1] == '.' || out[n-1] == ',') {
				b.WriteString(map[byte]string{'u': "000000", 'v': "000"}[c])
			} else {
				dropped = append(dropped, string(c))
			}
		case phpToGo[c] != "":
			b.WriteString(phpToGo[c])
		case c >= '0' && c <= '9' || strings.IndexByte("SNwzWtLoIZUB", c) >= 0:
			dropped = append(dropped, string(c))
		default:
			b.WriteByte(c)
		}
	}
	return strings.TrimSpace(b.String()), dropped
}

// migrateDateFormat rewrites a PHP date format left over from the PHP
// version (or an older config.json) as a Go layout. It returns a note
// describing the change, or "" when nothing changed.
func migrateDateFormat(cfg *Config) string {
	old := strings.TrimSpace(cfg.DateFormat)
	if old == "" {
		cfg.DateFormat = DefaultDateFormat
		return ""
	}
	if IsLayout(old) {
		return ""
	}
	layout, dropped := LayoutFromPHP(old)
	if !IsLayout(layout) {
		layout = DefaultDateFormat
	}
	cfg.DateFormat = layout
	note := fmt.Sprintf("converted PHP date_format %q to Go layout %q", old, layout)
	if len(dropped) > 0 {
		note += " (no Go equivalent for: " + strings.Join(dropped, " ") + ")"
	}
	return note
}
