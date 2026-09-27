package blog

import (
	"strconv"
	"strings"
	"time"

	"github.com/johanvanrhyn/pureblog-go/internal/config"
)

// StorageLayout is the date format written to front matter.
const StorageLayout = "2006-01-02 15:04"

// LayoutHasTime reports whether a Go time layout shows the time of day.
func LayoutHasTime(layout string) bool {
	midnight := time.Date(2001, time.November, 24, 0, 0, 0, 0, time.UTC)
	return midnight.Format(layout) != midnight.Add(13*time.Hour+47*time.Minute).Format(layout)
}

// Location returns the site's configured time zone, falling back to UTC.
func Location(cfg config.Config) *time.Location {
	tz := strings.TrimSpace(cfg.Timezone)
	if tz == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Local
	}
	return loc
}

// ValidTimezone reports whether name is a loadable IANA time zone.
func ValidTimezone(name string) bool {
	if name == "" || name == "Local" {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

// strict formats tried first, matching the PHP createFromFormat list.
var strictLayouts = []string{
	"2006-01-02 15:04",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05.999999Z",
	"2006-01-02T15:04:05-07:00",
	"2006-01-02T15:04:05.999999-07:00",
}

// fallback formats approximating strtotime() for common inputs.
var looseLayouts = []string{
	time.RFC3339,
	time.RFC3339Nano,
	"2006-01-02T15:04",
	"2006-01-02T15:04:05",
	"2006-01-02",
	"2006/01/02",
	"2006/01/02 15:04",
	"01/02/2006",
	"January 2, 2006",
	"January 2 2006",
	"Jan 2, 2006",
	"Jan 2 2006",
	"2 January 2006",
	"2 Jan 2006",
	"January 2, 2006 15:04",
	time.RFC1123,
	time.RFC1123Z,
	time.RFC822,
	time.RFC822Z,
	time.RFC850,
	time.ANSIC,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"2006-01-02 15:04:05 -0700",
	"2006-01-02 15:04:05 MST",
}

// ParsePostTime parses a front-matter date in the site's time zone and
// returns it in that zone.
func ParsePostTime(value string, loc *time.Location) (time.Time, bool) {
	t, ok := parseRaw(value, loc)
	if !ok {
		return t, false
	}
	return t.In(loc), true
}

// parseRaw parses a date, keeping any explicit offset from the input.
func parseRaw(value string, loc *time.Location) (time.Time, bool) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range strictLayouts {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return t, true
		}
	}
	for _, layout := range looseLayouts {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return t, true
		}
	}
	if strings.HasPrefix(raw, "@") {
		if n, err := strconv.ParseInt(raw[1:], 10, 64); err == nil {
			return time.Unix(n, 0).In(loc), true
		}
	}
	return time.Time{}, false
}

// normalizeDateValue mirrors normalize_date_value: recognised dates are
// rewritten in StorageLayout, anything else is returned unchanged.
func normalizeDateValue(value string, loc *time.Location) string {
	if value == "" {
		return value
	}
	if t, ok := parseRaw(value, loc); ok {
		return t.Format(StorageLayout)
	}
	return value
}
