// Package config holds the site configuration, stored as config/config.json.
// Field names mirror the keys of Pure Blog's PHP config so an existing
// config/config.php can be imported without losing anything.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jvanrhyn/pureblog-go/internal/phparray"
)

type Theme struct {
	ColorMode         string `json:"color_mode"`
	FontStack         string `json:"font_stack"`
	AdminFontStack    string `json:"admin_font_stack"`
	AdminColorMode    string `json:"admin_color_mode"`
	BackgroundColor   string `json:"background_color"`
	TextColor         string `json:"text_color"`
	AccentColor       string `json:"accent_color"`
	BorderColor       string `json:"border_color"`
	AccentBgColor     string `json:"accent_bg_color"`
	BackgroundColorDk string `json:"background_color_dark"`
	TextColorDk       string `json:"text_color_dark"`
	AccentColorDk     string `json:"accent_color_dark"`
	BorderColorDk     string `json:"border_color_dark"`
	AccentBgColorDk   string `json:"accent_bg_color_dark"`
	PostListLayout    string `json:"post_list_layout"`
	ActiveThemeName   string `json:"active_theme_name,omitempty"`
}

type Assets struct {
	Favicon          string `json:"favicon"`
	AppleTouchIcon   string `json:"apple_touch_icon"`
	OGImage          string `json:"og_image"`
	OGImagePreferred string `json:"og_image_preferred"`
	SidebarLogo      string `json:"sidebar_logo"`
}

type Cache struct {
	Enabled bool `json:"enabled"`
	RSSTTL  int  `json:"rss_ttl"`
}

type Community struct {
	PurecommentsEnabled bool   `json:"purecomments_enabled"`
	PurecommentsURL     string `json:"purecomments_url"`
}

type Config struct {
	SiteTitle           string `json:"site_title"`
	SiteTagline         string `json:"site_tagline"`
	SiteDescription     string `json:"site_description"`
	SiteEmail           string `json:"site_email"`
	CustomNav           string `json:"custom_nav"`
	CustomNavOnly       bool   `json:"custom_nav_only"`
	CustomRoutes        string `json:"custom_routes"`
	HeadInjectPage      string `json:"head_inject_page"`
	HeadInjectPost      string `json:"head_inject_post"`
	FooterInjectPage    string `json:"footer_inject_page"`
	FooterInjectPost    string `json:"footer_inject_post"`
	PostsPerPage        int    `json:"posts_per_page"`
	SearchExcerptLength int    `json:"search_excerpt_length"`
	SearchIncludePages  bool   `json:"search_include_pages"`
	HomepageSlug        string `json:"homepage_slug"`
	BlogPageSlug        string `json:"blog_page_slug"`
	SearchPageSlug      string `json:"search_page_slug"`
	SearchPageNotified  bool   `json:"search_page_notified"`
	ShowReadingTime     bool   `json:"show_reading_time"`
	EnableBlogPosts     bool   `json:"enable_blog_posts"`
	AdminHomepage       string `json:"admin_homepage"`
	AdminHideDashboard  bool   `json:"admin_hide_dashboard"`

	BaseURL    string `json:"base_url"`
	Language   string `json:"language"`
	Timezone   string `json:"timezone"`
	DateFormat string `json:"date_format"`

	AdminUsername     string   `json:"admin_username"`
	AdminPasswordHash string   `json:"admin_password_hash"`
	MFAEnabled        bool     `json:"mfa_enabled"`
	MFASecret         string   `json:"mfa_secret"`
	MFABackupCodes    []string `json:"mfa_backup_codes"`

	Cache     Cache     `json:"cache"`
	Theme     Theme     `json:"theme"`
	Assets    Assets    `json:"assets"`
	Community Community `json:"community"`
}

// Default returns the configuration used before setup has run, matching
// default_config() in the PHP version.
func Default() Config {
	return Config{
		SiteTitle:           "My Blog",
		PostsPerPage:        20,
		SearchExcerptLength: 2500,
		SearchIncludePages:  true,
		SearchPageSlug:      "search",
		EnableBlogPosts:     true,
		AdminHomepage:       "dashboard",
		Language:            "en",
		Timezone:            localTimezone(),
		DateFormat:          DefaultDateFormat,
		MFABackupCodes:      []string{},
		Cache:               Cache{Enabled: true, RSSTTL: 3600},
		Theme: Theme{
			ColorMode:         "auto",
			FontStack:         "sans",
			AdminFontStack:    "mono",
			AdminColorMode:    "auto",
			BackgroundColor:   "#FAFAFA",
			TextColor:         "#212121",
			AccentColor:       "#0D47A1",
			BorderColor:       "#898EA4",
			AccentBgColor:     "#F5F7FF",
			BackgroundColorDk: "#212121",
			TextColorDk:       "#DCDCDC",
			AccentColorDk:     "#FFB300",
			BorderColorDk:     "#555",
			AccentBgColorDk:   "#2B2B2B",
			PostListLayout:    "excerpt",
		},
		Assets: Assets{OGImagePreferred: "banner"},
	}
}

func localTimezone() string {
	if tz := os.Getenv("TZ"); tz != "" {
		if _, err := time.LoadLocation(tz); err == nil {
			return tz
		}
	}
	if name := time.Local.String(); name != "" && name != "Local" {
		return name
	}
	return "UTC"
}

// Store loads and saves the config file, keeping an in-memory copy.
type Store struct {
	mu        sync.RWMutex
	path      string
	cfg       Config
	installed bool
}

// Open loads config.json from dir, importing config.php first if only the
// PHP version exists.
func Open(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, "config.json"), cfg: Default()}
	if _, err := os.Stat(s.path); errors.Is(err, os.ErrNotExist) {
		phpPath := filepath.Join(dir, "config.php")
		if _, err := os.Stat(phpPath); err == nil {
			cfg, err := ImportPHP(phpPath)
			if err != nil {
				return nil, fmt.Errorf("importing %s: %w", phpPath, err)
			}
			if err := s.Save(cfg); err != nil {
				return nil, err
			}
			return s, nil
		}
		return s, nil
	}
	note, err := s.reload()
	if err != nil {
		return nil, err
	}
	if note != "" {
		log.Printf("config: %s", note)
		if err := s.Save(s.Get()); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// reload reads config.json, converting a PHP date format if one is found.
// It returns a note describing any conversion, which the caller persists.
func (s *Store) reload() (string, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return "", err
	}
	cfg, err := decode(raw)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", s.path, err)
	}
	note := migrateDateFormat(&cfg)
	s.mu.Lock()
	s.cfg = cfg
	s.installed = cfg.AdminPasswordHash != ""
	s.mu.Unlock()
	return note, nil
}

// decode unmarshals JSON over the defaults so missing keys keep their
// default values. Type mismatches on individual fields are tolerated.
func decode(raw []byte) (Config, error) {
	cfg := Default()
	if err := json.Unmarshal(raw, &cfg); err != nil {
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) {
			return cfg, err
		}
	}
	if cfg.MFABackupCodes == nil {
		cfg.MFABackupCodes = []string{}
	}
	return cfg, nil
}

// Get returns a copy of the current configuration.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cfg
	c.MFABackupCodes = append([]string(nil), s.cfg.MFABackupCodes...)
	return c
}

// Installed reports whether setup has completed (an admin password exists).
func (s *Store) Installed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.installed
}

// Save writes the configuration atomically and updates the in-memory copy.
func (s *Store) Save(cfg Config) error {
	if cfg.MFABackupCodes == nil {
		cfg.MFABackupCodes = []string{}
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.mu.Lock()
	s.cfg = cfg
	s.installed = cfg.AdminPasswordHash != ""
	s.mu.Unlock()
	return nil
}

// ImportPHP converts a Pure Blog config/config.php file into a Config.
func ImportPHP(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	v, err := phparray.Parse(string(raw))
	if err != nil {
		return Config{}, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return Config{}, errors.New("config.php does not return an associative array")
	}
	normalizeLists(m)
	data, err := json.Marshal(m)
	if err != nil {
		return Config{}, err
	}
	cfg, err := decode(data)
	if err != nil {
		return cfg, err
	}
	if note := migrateDateFormat(&cfg); note != "" {
		log.Printf("config: %s", note)
	}
	return cfg, nil
}

// normalizeLists turns empty PHP arrays that should be objects back into
// maps, and empty lists that should be lists into slices, so JSON decoding
// into the struct works.
func normalizeLists(m map[string]any) {
	for _, key := range []string{"cache", "theme", "assets", "community"} {
		if l, ok := m[key].([]any); ok && len(l) == 0 {
			m[key] = map[string]any{}
		}
	}
	if codes, ok := m["mfa_backup_codes"].(map[string]any); ok {
		list := make([]any, 0, len(codes))
		for _, k := range phparray.Keys(codes) {
			list = append(list, codes[k])
		}
		m["mfa_backup_codes"] = list
	}
}
