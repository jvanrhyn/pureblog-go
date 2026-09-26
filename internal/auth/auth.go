// Package auth implements admin sessions, CSRF protection, remember-me
// cookies, login rate limiting and TOTP two-factor authentication. Cookie
// names and the data/*.json formats match the PHP version, so an existing
// installation keeps its remember-me logins.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Flash is a one-time notice shown on the next admin page.
type Flash struct {
	OK      bool
	Message string
}

// Preview is an unsaved editor snapshot shown by the preview page.
type Preview struct {
	Markdown     string
	Title        string
	Date         string
	Tags         string
	Description  string
	Layout       string
	LayoutFields map[string]string
	EditorType   string
	Slug         string
	CreatedAt    time.Time
}

// Session is server-side state for one browser.
type Session struct {
	ID             string
	IsAdmin        bool
	CSRFToken      string
	MFAPending     bool
	MFARememberMe  bool
	MFASetupSecret string
	NewBackupCodes []string
	Flash          *Flash
	Previews       map[string]Preview
	lastSeen       time.Time
	// mu serialises requests that share a session, like PHP's session lock.
	mu sync.Mutex
}

// Lock acquires the session for the duration of a request.
func (s *Session) Lock() { s.mu.Lock() }

// Unlock releases the session.
func (s *Session) Unlock() { s.mu.Unlock() }

// TakeFlash returns and clears the pending flash message.
func (s *Session) TakeFlash() *Flash {
	f := s.Flash
	s.Flash = nil
	return f
}

// Manager stores sessions in memory and handles cookies.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	dataDir  string
	fileMu   sync.Mutex
	idle     time.Duration
}

// NewManager creates a session manager. dataDir holds remember-me.json and
// login-failures.json.
func NewManager(dataDir string) *Manager {
	m := &Manager{sessions: map[string]*Session{}, dataDir: dataDir, idle: 24 * time.Hour}
	go m.gc()
	return m
}

func (m *Manager) gc() {
	for range time.Tick(10 * time.Minute) {
		m.mu.Lock()
		for id, s := range m.sessions {
			if time.Since(s.lastSeen) > m.idle {
				delete(m.sessions, id)
			}
		}
		m.mu.Unlock()
	}
}

// RandomHex returns n random bytes hex-encoded.
func RandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// CookieNames returns the session and remember-me cookie names for a site,
// derived from its base URL like the PHP version.
func CookieNames(baseURL string) (session, remember string) {
	sum := sha256.Sum256([]byte(baseURL))
	h := hex.EncodeToString(sum[:])[:8]
	return "pb_session_" + h, "pb_remember_" + h
}

// CookieOptions describe how cookies are issued for a request.
type CookieOptions struct {
	SessionName  string
	RememberName string
	Path         string
	Secure       bool
}

// Get returns the existing session for the request, or nil.
func (m *Manager) Get(r *http.Request, opt CookieOptions) *Session {
	c, err := r.Cookie(opt.SessionName)
	if err != nil || c.Value == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[c.Value]
	if s == nil || time.Since(s.lastSeen) > m.idle {
		return nil
	}
	s.lastSeen = time.Now()
	return s
}

// Start returns the request's session, creating one (and setting the
// cookie) if needed.
func (m *Manager) Start(w http.ResponseWriter, r *http.Request, opt CookieOptions) *Session {
	if s := m.Get(r, opt); s != nil {
		return s
	}
	s := &Session{ID: RandomHex(32), lastSeen: time.Now(), Previews: map[string]Preview{}}
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
	m.setSessionCookie(w, s.ID, opt)
	return s
}

func (m *Manager) setSessionCookie(w http.ResponseWriter, id string, opt CookieOptions) {
	http.SetCookie(w, &http.Cookie{
		Name: opt.SessionName, Value: id, Path: opt.Path,
		Secure: opt.Secure, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// Regenerate gives the session a new ID (after login) to prevent fixation.
func (m *Manager) Regenerate(w http.ResponseWriter, s *Session, opt CookieOptions) {
	m.mu.Lock()
	delete(m.sessions, s.ID)
	s.ID = RandomHex(32)
	m.sessions[s.ID] = s
	m.mu.Unlock()
	m.setSessionCookie(w, s.ID, opt)
}

// Destroy removes a session and expires its cookie.
func (m *Manager) Destroy(w http.ResponseWriter, s *Session, opt CookieOptions) {
	m.mu.Lock()
	delete(m.sessions, s.ID)
	m.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: opt.SessionName, Value: "", Path: opt.Path, MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: opt.Secure})
}

// CSRF returns the session's CSRF token, creating it if needed.
func (s *Session) CSRF() string {
	if s.CSRFToken == "" {
		s.CSRFToken = RandomHex(32)
	}
	return s.CSRFToken
}

// ValidCSRF checks a submitted token against the session.
func ValidCSRF(s *Session, token string) bool {
	return s != nil && token != "" && s.CSRFToken != "" &&
		subtle.ConstantTimeCompare([]byte(s.CSRFToken), []byte(token)) == 1
}

// ---------------------------------------------------------------------------
// Passwords
// ---------------------------------------------------------------------------

// HashPassword returns a bcrypt hash compatible with PHP's password_hash().
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(h), err
}

// CheckPassword verifies a password against a bcrypt hash ($2y$ hashes
// from PHP are supported).
func CheckPassword(hash, password string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// ConstantTimeEqual compares two strings in constant time.
func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// ---------------------------------------------------------------------------
// JSON data files
// ---------------------------------------------------------------------------

func (m *Manager) readJSON(name string, v any) {
	raw, err := os.ReadFile(filepath.Join(m.dataDir, name))
	if err != nil {
		return
	}
	_ = json.Unmarshal(raw, v)
}

func (m *Manager) writeJSON(name string, v any) error {
	if err := os.MkdirAll(m.dataDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "    ")
	if err != nil {
		return err
	}
	path := filepath.Join(m.dataDir, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---------------------------------------------------------------------------
// Login rate limiting
// ---------------------------------------------------------------------------

type failureEntry struct {
	Failures     int   `json:"failures"`
	LockoutUntil int64 `json:"lockout_until"`
}

// LockoutUntil returns the Unix time until which ip is locked out (0 if not).
func (m *Manager) LockoutUntil(ip string) int64 {
	m.fileMu.Lock()
	defer m.fileMu.Unlock()
	data := map[string]failureEntry{}
	m.readJSON("login-failures.json", &data)
	return data[ip].LockoutUntil
}

// RecordFailure counts a failed login and returns the lockout time; five
// failures lock the IP out for five minutes.
func (m *Manager) RecordFailure(ip string) int64 {
	m.fileMu.Lock()
	defer m.fileMu.Unlock()
	data := map[string]failureEntry{}
	m.readJSON("login-failures.json", &data)
	now := time.Now().Unix()
	for k, e := range data {
		if e.LockoutUntil > 0 && e.LockoutUntil < now-3600 {
			delete(data, k)
		}
	}
	e := data[ip]
	e.Failures++
	if e.Failures >= 5 {
		e.LockoutUntil = now + 5*60
	}
	data[ip] = e
	_ = m.writeJSON("login-failures.json", data)
	return e.LockoutUntil
}

// ClearFailures resets the failure count for ip.
func (m *Manager) ClearFailures(ip string) {
	m.fileMu.Lock()
	defer m.fileMu.Unlock()
	data := map[string]failureEntry{}
	m.readJSON("login-failures.json", &data)
	if _, ok := data[ip]; !ok {
		return
	}
	delete(data, ip)
	_ = m.writeJSON("login-failures.json", data)
}

// ---------------------------------------------------------------------------
// Remember-me tokens
// ---------------------------------------------------------------------------

type rememberEntry struct {
	ValidatorHash string `json:"validator_hash"`
	Expires       int64  `json:"expires"`
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// SetRememberMe issues a 90-day remember-me cookie.
func (m *Manager) SetRememberMe(w http.ResponseWriter, opt CookieOptions) {
	selector, validator := RandomHex(16), RandomHex(32)
	expires := time.Now().Add(90 * 24 * time.Hour)

	m.fileMu.Lock()
	tokens := map[string]rememberEntry{}
	m.readJSON("remember-me.json", &tokens)
	now := time.Now().Unix()
	for k, e := range tokens {
		if e.Expires < now {
			delete(tokens, k)
		}
	}
	tokens[selector] = rememberEntry{ValidatorHash: sha256Hex(validator), Expires: expires.Unix()}
	err := m.writeJSON("remember-me.json", tokens)
	m.fileMu.Unlock()
	if err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: opt.RememberName, Value: selector + ":" + validator, Path: opt.Path,
		Expires: expires, Secure: opt.Secure, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// ClearRememberMe removes the request's remember-me token and cookie.
func (m *Manager) ClearRememberMe(w http.ResponseWriter, r *http.Request, opt CookieOptions) {
	if c, err := r.Cookie(opt.RememberName); err == nil {
		if selector, _, ok := strings.Cut(c.Value, ":"); ok {
			m.fileMu.Lock()
			tokens := map[string]rememberEntry{}
			m.readJSON("remember-me.json", &tokens)
			delete(tokens, selector)
			_ = m.writeJSON("remember-me.json", tokens)
			m.fileMu.Unlock()
		}
	}
	http.SetCookie(w, &http.Cookie{Name: opt.RememberName, Value: "", Path: opt.Path, MaxAge: -1, Secure: opt.Secure, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

// ClearAllRememberMe invalidates every remember-me token (after a password
// or 2FA change).
func (m *Manager) ClearAllRememberMe() {
	m.fileMu.Lock()
	defer m.fileMu.Unlock()
	if _, err := os.Stat(filepath.Join(m.dataDir, "remember-me.json")); err == nil {
		_ = m.writeJSON("remember-me.json", map[string]rememberEntry{})
	}
}

// RestoreFromCookie logs the session in when a valid remember-me cookie is
// present. It returns true if the session is (now) an admin session.
func (m *Manager) RestoreFromCookie(w http.ResponseWriter, r *http.Request, s *Session, opt CookieOptions) bool {
	if s.IsAdmin {
		return true
	}
	c, err := r.Cookie(opt.RememberName)
	if err != nil || c.Value == "" {
		return false
	}
	selector, validator, ok := strings.Cut(c.Value, ":")
	if !ok {
		return false
	}
	m.fileMu.Lock()
	tokens := map[string]rememberEntry{}
	m.readJSON("remember-me.json", &tokens)
	entry, exists := tokens[selector]
	if exists && entry.Expires < time.Now().Unix() {
		delete(tokens, selector)
		_ = m.writeJSON("remember-me.json", tokens)
		exists = false
	}
	m.fileMu.Unlock()
	if !exists || !ConstantTimeEqual(entry.ValidatorHash, sha256Hex(validator)) {
		return false
	}
	m.Regenerate(w, s, opt)
	s.IsAdmin = true
	return true
}

// HasCookies reports whether the request carries a session or remember-me
// cookie (used to decide whether a page may be served from cache).
func HasCookies(r *http.Request, opt CookieOptions) bool {
	for _, name := range []string{opt.SessionName, opt.RememberName} {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			return true
		}
	}
	return false
}
