package blog

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/johanvanrhyn/pureblog-go/internal/config"
)

// StorageLayout is the date format written to front matter ("Y-m-d H:i").
const StorageLayout = "2006-01-02 15:04"

// PHPDate formats t using a PHP date() format string, so date formats from
// an existing Pure Blog configuration keep working.
func PHPDate(format string, t time.Time) string {
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		c := format[i]
		switch c {
		case '\\':
			if i+1 < len(format) {
				i++
				b.WriteByte(format[i])
			}
		case 'd':
			fmt.Fprintf(&b, "%02d", t.Day())
		case 'D':
			b.WriteString(t.Weekday().String()[:3])
		case 'j':
			b.WriteString(strconv.Itoa(t.Day()))
		case 'l':
			b.WriteString(t.Weekday().String())
		case 'N':
			wd := int(t.Weekday())
			if wd == 0 {
				wd = 7
			}
			b.WriteString(strconv.Itoa(wd))
		case 'S':
			b.WriteString(ordinalSuffix(t.Day()))
		case 'w':
			b.WriteString(strconv.Itoa(int(t.Weekday())))
		case 'z':
			b.WriteString(strconv.Itoa(t.YearDay() - 1))
		case 'W':
			_, w := t.ISOWeek()
			fmt.Fprintf(&b, "%02d", w)
		case 'F':
			b.WriteString(t.Month().String())
		case 'm':
			fmt.Fprintf(&b, "%02d", int(t.Month()))
		case 'M':
			b.WriteString(t.Month().String()[:3])
		case 'n':
			b.WriteString(strconv.Itoa(int(t.Month())))
		case 't':
			b.WriteString(strconv.Itoa(daysIn(t.Month(), t.Year())))
		case 'L':
			if daysIn(time.February, t.Year()) == 29 {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		case 'o':
			y, _ := t.ISOWeek()
			b.WriteString(strconv.Itoa(y))
		case 'Y':
			b.WriteString(strconv.Itoa(t.Year()))
		case 'y':
			fmt.Fprintf(&b, "%02d", t.Year()%100)
		case 'a':
			if t.Hour() < 12 {
				b.WriteString("am")
			} else {
				b.WriteString("pm")
			}
		case 'A':
			if t.Hour() < 12 {
				b.WriteString("AM")
			} else {
				b.WriteString("PM")
			}
		case 'g':
			b.WriteString(strconv.Itoa(hour12(t.Hour())))
		case 'G':
			b.WriteString(strconv.Itoa(t.Hour()))
		case 'h':
			fmt.Fprintf(&b, "%02d", hour12(t.Hour()))
		case 'H':
			fmt.Fprintf(&b, "%02d", t.Hour())
		case 'i':
			fmt.Fprintf(&b, "%02d", t.Minute())
		case 's':
			fmt.Fprintf(&b, "%02d", t.Second())
		case 'u':
			fmt.Fprintf(&b, "%06d", t.Nanosecond()/1000)
		case 'v':
			fmt.Fprintf(&b, "%03d", t.Nanosecond()/1_000_000)
		case 'e':
			b.WriteString(t.Location().String())
		case 'I':
			if t.IsDST() {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		case 'O':
			b.WriteString(t.Format("-0700"))
		case 'P':
			b.WriteString(t.Format("-07:00"))
		case 'p':
			if _, off := t.Zone(); off == 0 {
				b.WriteByte('Z')
			} else {
				b.WriteString(t.Format("-07:00"))
			}
		case 'T':
			b.WriteString(t.Format("MST"))
		case 'Z':
			_, off := t.Zone()
			b.WriteString(strconv.Itoa(off))
		case 'c':
			b.WriteString(t.Format("2006-01-02T15:04:05-07:00"))
		case 'r':
			b.WriteString(t.Format("Mon, 02 Jan 2006 15:04:05 -0700"))
		case 'U':
			b.WriteString(strconv.FormatInt(t.Unix(), 10))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func ordinalSuffix(day int) string {
	if day%100 >= 11 && day%100 <= 13 {
		return "th"
	}
	switch day % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	}
	return "th"
}

func hour12(h int) int {
	h %= 12
	if h == 0 {
		return 12
	}
	return h
}

func daysIn(m time.Month, year int) int {
	return time.Date(year, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
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
// rewritten as "Y-m-d H:i", anything else is returned unchanged.
func normalizeDateValue(value string, loc *time.Location) string {
	if value == "" {
		return value
	}
	if t, ok := parseRaw(value, loc); ok {
		return t.Format(StorageLayout)
	}
	return value
}
