package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIsLayout(t *testing.T) {
	for s, want := range map[string]bool{
		"January 2, 2006": true,
		"02/01/06":        true,
		"15:04":           true,
		"F j, Y":          false,
		"Y-m-d H:i":       false,
		"":                false,
	} {
		if got := IsLayout(s); got != want {
			t.Errorf("IsLayout(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestLayoutFromPHP(t *testing.T) {
	tm := time.Date(2026, 3, 1, 14, 5, 9, 0, time.UTC)
	cases := []struct {
		php, layout, formatted string
		dropped                []string
	}{
		{"F j, Y", "January 2, 2006", "March 1, 2026", nil},
		{"Y-m-d", "2006-01-02", "2026-03-01", nil},
		{"D, d M Y", "Mon, 02 Jan 2006", "Sun, 01 Mar 2026", nil},
		{"j F Y @ H:i", "2 January 2006 @ 15:04", "1 March 2026 @ 14:05", nil},
		{"g:i a", "3:04 pm", "2:05 pm", nil},
		{"jS \\o\\f F", "2 of January", "1 of March", []string{"S"}},
		{"N l", "Monday", "Sunday", []string{"N"}},
		{"H:i:s.u", "15:04:05.000000", "14:05:09.000000", nil},
	}
	for _, c := range cases {
		layout, dropped := LayoutFromPHP(c.php)
		if layout != c.layout || !reflect.DeepEqual(dropped, c.dropped) {
			t.Errorf("LayoutFromPHP(%q) = %q, %v; want %q, %v", c.php, layout, dropped, c.layout, c.dropped)
			continue
		}
		if got := tm.Format(layout); got != c.formatted {
			t.Errorf("format %q = %q, want %q", layout, got, c.formatted)
		}
	}
}

func TestOpenMigratesPHPDateFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"site_title":"Old","date_format":"j M Y"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Get().DateFormat; got != "2 Jan 2006" {
		t.Fatalf("DateFormat = %q, want %q", got, "2 Jan 2006")
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"date_format": "2 Jan 2006"`) {
		t.Errorf("converted format not saved:\n%s", raw)
	}

	// A Go layout is left alone.
	if err := os.WriteFile(path, []byte(`{"date_format":"Monday 2 Jan"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(dir); err != nil || s.Get().DateFormat != "Monday 2 Jan" {
		t.Fatalf("Go layout changed: %q, %v", s.Get().DateFormat, err)
	}
}

func TestImportPHPConvertsDateFormat(t *testing.T) {
	dir := t.TempDir()
	php := "<?php\nreturn [\n    'site_title' => 'PHP Blog',\n    'date_format' => 'l, F jS Y',\n];\n"
	if err := os.WriteFile(filepath.Join(dir, "config.php"), []byte(php), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Get().DateFormat; got != "Monday, January 2 2006" {
		t.Errorf("DateFormat = %q", got)
	}
}
