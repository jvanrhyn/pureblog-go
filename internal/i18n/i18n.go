// Package i18n loads the translation files and translates dot-notation keys.
package i18n

import (
	"encoding/json"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Language is an available translation.
type Language struct {
	Code string
	Name string
}

// Bundle holds all translation files.
type Bundle struct {
	raw   map[string]map[string]any
	mu    sync.Mutex
	cache map[string]*Translator
}

// Load reads every *.json file in dir of fsys.
func Load(fsys fs.FS, dir string) (*Bundle, error) {
	b := &Bundle{raw: map[string]map[string]any{}, cache: map[string]*Translator{}}
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, err
		}
		b.raw[strings.TrimSuffix(e.Name(), ".json")] = m
	}
	return b, nil
}

// Available lists the languages sorted by native name.
func (b *Bundle) Available() []Language {
	var out []Language
	for code, m := range b.raw {
		if name, ok := m["name"].(string); ok {
			out = append(out, Language{Code: code, Name: name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Has reports whether a language code has a translation file.
func (b *Bundle) Has(code string) bool {
	_, ok := b.raw[code]
	return ok
}

var (
	unsafeLang = regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	nonAlnum   = regexp.MustCompile(`[^a-zA-Z0-9]`)
)

// resolve mirrors _lang_load_file: exact → lowercase → base → 'en'.
func (b *Bundle) resolve(lang string) map[string]any {
	safe := unsafeLang.ReplaceAllString(lang, "")
	base := nonAlnum.ReplaceAllString(strings.SplitN(safe, "-", 2)[0], "")
	for _, c := range []string{safe, strings.ToLower(safe), base, strings.ToLower(base), "en"} {
		if c == "" {
			continue
		}
		if m, ok := b.raw[c]; ok {
			return m
		}
	}
	return map[string]any{}
}

// Translator returns the (cached) translator for a language, with English
// used for any missing keys.
func (b *Bundle) Translator(lang string) *Translator {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t, ok := b.cache[lang]; ok {
		return t
	}
	strs := b.resolve("en")
	if lang != "en" && lang != "" {
		strs = mergeRecursive(strs, b.resolve(lang))
	}
	t := &Translator{strings: strs}
	t.buildDateMap()
	b.cache[lang] = t
	return t
}

func mergeRecursive(base, over map[string]any) map[string]any {
	out := make(map[string]any, len(base))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		if bm, ok := out[k].(map[string]any); ok {
			if om, ok := v.(map[string]any); ok {
				out[k] = mergeRecursive(bm, om)
				continue
			}
		}
		if bl, ok := out[k].([]any); ok {
			if ol, ok := v.([]any); ok {
				merged := append([]any(nil), bl...)
				for i, item := range ol {
					if i < len(merged) {
						merged[i] = item
					} else {
						merged = append(merged, item)
					}
				}
				out[k] = merged
				continue
			}
		}
		out[k] = v
	}
	return out
}

// Translator translates keys for a single language.
type Translator struct {
	strings  map[string]any
	dateRepl *strings.Replacer
}

// T translates a dot-notation key. Replacements are given as alternating
// placeholder names and values: T("key", "n", 3). Missing keys return the
// key itself so they degrade gracefully.
func (t *Translator) T(key string, repl ...any) string {
	var cur any = t.strings
	for _, part := range strings.Split(key, ".") {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[part]
			if !ok {
				return key
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return key
			}
			cur = node[i]
		default:
			return key
		}
	}
	s, ok := cur.(string)
	if !ok {
		return key
	}
	for i := 0; i+1 < len(repl); i += 2 {
		s = strings.ReplaceAll(s, "{"+toString(repl[i])+"}", toString(repl[i+1]))
	}
	return s
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case interface{ String() string }:
		return x.String()
	}
	return ""
}

func (t *Translator) list(key string) []string {
	date, _ := t.strings["date"].(map[string]any)
	raw, _ := date[key].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

var (
	enMonths      = []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
	enMonthsShort = []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	enDays        = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	enDaysShort   = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
)

// buildDateMap prepares the replacer used by TranslateDate. Longer English
// names are matched before shorter ones (like PHP's strtr).
func (t *Translator) buildDateMap() {
	var pairs []string
	seen := map[string]bool{}
	add := func(en, local []string, n int) {
		if len(local) != n {
			return
		}
		for i := range en {
			if !seen[en[i]] {
				seen[en[i]] = true
				pairs = append(pairs, en[i], local[i])
			}
		}
	}
	add(enMonths, t.list("months"), 12)
	add(enMonthsShort, t.list("months_short"), 12)
	add(enDays, t.list("days"), 7)
	add(enDaysShort, t.list("days_short"), 7)
	if len(pairs) == 0 {
		return
	}
	// strings.Replacer picks the first matching old string in argument order
	// at each position; sort so longer keys come first.
	type kv struct{ k, v string }
	kvs := make([]kv, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		kvs = append(kvs, kv{pairs[i], pairs[i+1]})
	}
	sort.SliceStable(kvs, func(i, j int) bool { return len(kvs[i].k) > len(kvs[j].k) })
	ordered := make([]string, 0, len(pairs))
	for _, p := range kvs {
		ordered = append(ordered, p.k, p.v)
	}
	t.dateRepl = strings.NewReplacer(ordered...)
}

// TranslateDate substitutes localised month and day names into a date
// string formatted with English names.
func (t *Translator) TranslateDate(formatted string) string {
	if t.dateRepl == nil {
		return formatted
	}
	return t.dateRepl.Replace(formatted)
}
