package blog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jvanrhyn/pureblog-go/internal/config"
)

// Palette is one colour scheme of a theme.
type Palette struct {
	Bg       string `json:"bg"`
	Text     string `json:"text"`
	Accent   string `json:"accent"`
	Border   string `json:"border"`
	AccentBg string `json:"accent_bg"`
}

// ThemeInfo describes a colour theme available in the theme picker.
type ThemeInfo struct {
	ID       string
	Name     string
	Source   string
	Filename string
	IsCustom bool
	HasLight bool
	HasDark  bool
	Light    Palette
	Dark     Palette
}

func str(m map[string]any, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				return strings.TrimSpace(s), true
			}
		}
	}
	return "", false
}

func strDefault(m map[string]any, def string, keys ...string) string {
	if s, ok := str(m, keys...); ok {
		return s
	}
	return strings.TrimSpace(def)
}

func defaultLight(d config.Theme) Palette {
	return Palette{d.BackgroundColor, d.TextColor, d.AccentColor, d.BorderColor, d.AccentBgColor}
}

func defaultDark(d config.Theme) Palette {
	return Palette{d.BackgroundColorDk, d.TextColorDk, d.AccentColorDk, d.BorderColorDk, d.AccentBgColorDk}
}

func normalizeTheme(raw map[string]any, source, filename string, index int) *ThemeInfo {
	name, _ := str(raw, "name")
	if name == "" {
		return nil
	}
	base := strings.TrimSuffix(filename, ".json")
	var id string
	if source == "core" {
		id = "core:"
		if filename != "" {
			id += base + ":"
		}
		if index > 0 {
			id += fmt.Sprint(index) + ":"
		}
		id += Slugify(name)
	} else if filename != "" {
		id = "user:" + base
	} else {
		id = "user:" + Slugify(name)
	}

	d := config.Default().Theme
	th := &ThemeInfo{ID: id, Name: name, Source: source, Filename: filename, IsCustom: source == "user"}
	light, hasLight := raw["light"].(map[string]any)
	dark, hasDark := raw["dark"].(map[string]any)
	if hasLight || hasDark {
		if light == nil {
			light = map[string]any{}
		}
		if dark == nil {
			dark = map[string]any{}
		}
		th.HasLight, th.HasDark = hasLight, hasDark
		th.Light = Palette{
			Bg:       strDefault(light, d.BackgroundColor, "bg", "background_color"),
			Text:     strDefault(light, d.TextColor, "text", "text_color"),
			Accent:   strDefault(light, d.AccentColor, "accent", "accent_color"),
			Border:   strDefault(light, d.BorderColor, "border", "border_color"),
			AccentBg: strDefault(light, d.AccentBgColor, "accent_bg", "accent_bg_color"),
		}
		th.Dark = Palette{
			Bg:       strDefault(dark, d.BackgroundColorDk, "bg", "background_color", "background_color_dark"),
			Text:     strDefault(dark, d.TextColorDk, "text", "text_color", "text_color_dark"),
			Accent:   strDefault(dark, d.AccentColorDk, "accent", "accent_color", "accent_color_dark"),
			Border:   strDefault(dark, d.BorderColorDk, "border", "border_color", "border_color_dark"),
			AccentBg: strDefault(dark, d.AccentBgColorDk, "accent_bg", "accent_bg_color", "accent_bg_color_dark"),
		}
		return th
	}

	mode, _ := str(raw, "mode")
	isDark := mode == "dark" || strings.Contains(strings.ToLower(name), "dark")
	def := defaultLight(d)
	if isDark {
		def = defaultDark(d)
	}
	palette := Palette{
		Bg:       strDefault(raw, def.Bg, "bg", "background_color"),
		Text:     strDefault(raw, def.Text, "text", "text_color"),
		Accent:   strDefault(raw, def.Accent, "accent", "accent_color"),
		Border:   strDefault(raw, def.Border, "border", "border_color"),
		AccentBg: strDefault(raw, def.AccentBg, "accent_bg", "accent_bg_color"),
	}
	if isDark {
		th.HasDark, th.Light, th.Dark = true, defaultLight(d), palette
	} else {
		th.HasLight, th.Light, th.Dark = true, palette, defaultDark(d)
	}
	return th
}

func themesFromJSON(data []byte, source, filename string) []*ThemeInfo {
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err == nil {
		if _, ok := obj["name"]; ok {
			if th := normalizeTheme(obj, source, filename, 0); th != nil {
				return []*ThemeInfo{th}
			}
			return nil
		}
		// A JSON object keyed by index behaves like a list.
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var out []*ThemeInfo
		for i, k := range keys {
			if m, ok := obj[k].(map[string]any); ok {
				if th := normalizeTheme(m, source, filename, i+1); th != nil {
					out = append(out, th)
				}
			}
		}
		return out
	}
	var list []any
	if err := json.Unmarshal(data, &list); err != nil {
		return nil
	}
	var out []*ThemeInfo
	for i, item := range list {
		if m, ok := item.(map[string]any); ok {
			if th := normalizeTheme(m, source, filename, i+1); th != nil {
				out = append(out, th)
			}
		}
	}
	return out
}

// Themes returns the bundled themes followed by user themes from
// content/themes. Later themes with the same ID replace earlier ones.
func (s *Site) Themes() []*ThemeInfo {
	var ordered []*ThemeInfo
	index := map[string]int{}
	add := func(list []*ThemeInfo) {
		for _, th := range list {
			if i, ok := index[th.ID]; ok {
				ordered[i] = th
				continue
			}
			index[th.ID] = len(ordered)
			ordered = append(ordered, th)
		}
	}
	coreFiles, _ := fs.Glob(s.coreThemes, "*.json")
	sort.Strings(coreFiles)
	for _, f := range coreFiles {
		if data, err := fs.ReadFile(s.coreThemes, f); err == nil {
			add(themesFromJSON(data, "core", path.Base(f)))
		}
	}
	userFiles, _ := filepath.Glob(filepath.Join(s.ThemesDir, "*.json"))
	sort.Strings(userFiles)
	for _, f := range userFiles {
		if data, err := os.ReadFile(f); err == nil && len(data) > 0 {
			add(themesFromJSON(data, "user", filepath.Base(f)))
		}
	}
	return ordered
}

// ThemeByID looks up a theme.
func (s *Site) ThemeByID(id string) *ThemeInfo {
	for _, th := range s.Themes() {
		if th.ID == id {
			return th
		}
	}
	return nil
}

// ApplyTheme copies a theme's colours into the site configuration.
func (s *Site) ApplyTheme(th *ThemeInfo) error {
	cfg := s.Config.Get()
	if th.HasLight {
		cfg.Theme.BackgroundColor = orDefault(th.Light.Bg, "#FAFAFA")
		cfg.Theme.TextColor = orDefault(th.Light.Text, "#212121")
		cfg.Theme.AccentColor = orDefault(th.Light.Accent, "#0D47A1")
		cfg.Theme.BorderColor = orDefault(th.Light.Border, "#898EA4")
		cfg.Theme.AccentBgColor = orDefault(th.Light.AccentBg, "#F5F7FF")
	}
	if th.HasDark {
		cfg.Theme.BackgroundColorDk = orDefault(th.Dark.Bg, "#212121")
		cfg.Theme.TextColorDk = orDefault(th.Dark.Text, "#DCDCDC")
		cfg.Theme.AccentColorDk = orDefault(th.Dark.Accent, "#FFB300")
		cfg.Theme.BorderColorDk = orDefault(th.Dark.Border, "#555555")
		cfg.Theme.AccentBgColorDk = orDefault(th.Dark.AccentBg, "#2B2B2B")
	}
	cfg.Theme.ActiveThemeName = th.Name
	return s.Config.Save(cfg)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// IsThemeActive reports whether th matches the configured colours.
func IsThemeActive(th *ThemeInfo, t config.Theme) bool {
	if t.ActiveThemeName != "" && t.ActiveThemeName == th.Name {
		return true
	}
	hasLight, hasDark := th.HasLight, th.HasDark
	if !hasLight && !hasDark {
		hasLight, hasDark = true, true
	}
	eq := strings.EqualFold
	if hasLight && !(eq(th.Light.Bg, t.BackgroundColor) && eq(th.Light.Text, t.TextColor) && eq(th.Light.Accent, t.AccentColor)) {
		return false
	}
	if hasDark && !(eq(th.Dark.Bg, t.BackgroundColorDk) && eq(th.Dark.Text, t.TextColorDk) && eq(th.Dark.Accent, t.AccentColorDk)) {
		return false
	}
	return true
}

// SaveThemeJSON creates a user theme file. light/dark may be nil.
func (s *Site) SaveThemeJSON(name string, light, dark *Palette) (string, error) {
	t := s.T()
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New(t.T("admin.settings.theme.error_theme_name"))
	}
	if light == nil && dark == nil {
		return "", errors.New(t.T("admin.settings.theme.error_theme_empty"))
	}
	if err := os.MkdirAll(s.ThemesDir, 0o755); err != nil {
		return "", errors.New(t.T("admin.settings.theme.error_theme_folder"))
	}
	slug := Slugify(name)
	if slug == "" {
		slug = fmt.Sprintf("theme-%d", time.Now().Unix())
	}
	d := config.Default().Theme
	clean := func(p *Palette, def Palette) *Palette {
		fix := func(v, dv string) string {
			v = strings.TrimSpace(v)
			if v == "" {
				return dv
			}
			if !strings.HasPrefix(v, "#") {
				return "#" + v
			}
			return v
		}
		return &Palette{fix(p.Bg, def.Bg), fix(p.Text, def.Text), fix(p.Accent, def.Accent), fix(p.Border, def.Border), fix(p.AccentBg, def.AccentBg)}
	}
	data := struct {
		Name  string   `json:"name"`
		Light *Palette `json:"light,omitempty"`
		Dark  *Palette `json:"dark,omitempty"`
	}{Name: name}
	if light != nil {
		data.Light = clean(light, defaultLight(d))
	}
	if dark != nil {
		data.Dark = clean(dark, defaultDark(d))
	}
	out, _ := json.MarshalIndent(data, "", "    ")
	filename := slug + ".json"
	if err := os.WriteFile(filepath.Join(s.ThemesDir, filename), out, 0o644); err != nil {
		return "", errors.New(t.T("admin.settings.theme.error_theme_save"))
	}
	return filename, nil
}

// SaveUploadedTheme stores an imported theme JSON file.
func (s *Site) SaveUploadedTheme(originalName string, data []byte) error {
	t := s.T()
	var v any
	if len(data) == 0 || json.Unmarshal(data, &v) != nil {
		return errors.New(t.T("admin.settings.theme.error_theme_invalid"))
	}
	switch v.(type) {
	case map[string]any, []any:
	default:
		return errors.New(t.T("admin.settings.theme.error_theme_invalid"))
	}
	if err := os.MkdirAll(s.ThemesDir, 0o755); err != nil {
		return errors.New(t.T("admin.settings.theme.error_theme_folder"))
	}
	slug := Slugify(strings.TrimSuffix(filepath.Base(originalName), filepath.Ext(originalName)))
	if slug == "" {
		slug = fmt.Sprintf("imported-theme-%d", time.Now().Unix())
	}
	out, _ := json.MarshalIndent(v, "", "    ")
	if err := os.WriteFile(filepath.Join(s.ThemesDir, slug+".json"), out, 0o644); err != nil {
		return errors.New(t.T("admin.settings.theme.error_theme_save"))
	}
	return nil
}

// DeleteThemeFile removes a user theme file.
func (s *Site) DeleteThemeFile(filename string) bool {
	clean := filepath.Base(filename)
	if clean == "" || clean == "." || !strings.HasSuffix(strings.ToLower(clean), ".json") {
		return false
	}
	return os.Remove(filepath.Join(s.ThemesDir, clean)) == nil
}
