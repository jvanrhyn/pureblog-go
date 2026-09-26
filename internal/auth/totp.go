package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"
)

const base32Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

func randomIndex(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		panic(err)
	}
	return int(v.Int64())
}

// GenerateSecret returns a random base32 TOTP secret.
func GenerateSecret(length int) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = base32Alphabet[randomIndex(len(base32Alphabet))]
	}
	return string(b)
}

func base32Decode(s string) []byte {
	var out []byte
	var buffer, bits uint
	for _, c := range strings.ToUpper(s) {
		idx := strings.IndexRune(base32Alphabet, c)
		if idx < 0 {
			continue
		}
		buffer = buffer<<5 | uint(idx)
		bits += 5
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(buffer>>bits))
		}
	}
	return out
}

// TOTPCode computes the 6-digit code for a secret at time t.
func TOTPCode(secret string, t time.Time) string {
	key := base32Decode(secret)
	if len(key) == 0 {
		return ""
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(t.Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0F
	code := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7FFFFFFF
	return fmt.Sprintf("%06d", code%1_000_000)
}

// VerifyTOTP checks a code, allowing one 30-second step of clock drift.
func VerifyTOTP(secret, code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	now := time.Now()
	for i := -1; i <= 1; i++ {
		expected := TOTPCode(secret, now.Add(time.Duration(i)*30*time.Second))
		if expected != "" && ConstantTimeEqual(expected, code) {
			return true
		}
	}
	return false
}

// GenerateBackupCodes returns count codes like "a1b2-c3d4".
func GenerateBackupCodes(count int) []string {
	const chars = "0123456789abcdefghijklmnopqrstuvwxyz"
	codes := make([]string, count)
	for i := range codes {
		var b strings.Builder
		for j := 0; j < 9; j++ {
			if j == 4 {
				b.WriteByte('-')
				continue
			}
			b.WriteByte(chars[randomIndex(len(chars))])
		}
		codes[i] = b.String()
	}
	return codes
}

func normaliseBackupCode(code string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(code) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// HashBackupCodes bcrypt-hashes backup codes for storage.
func HashBackupCodes(codes []string) []string {
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		if n := normaliseBackupCode(c); n != "" {
			if h, err := HashPassword(n); err == nil {
				out = append(out, h)
			}
		}
	}
	return out
}

// VerifyBackupCode returns the index of the matching hashed code, or -1.
func VerifyBackupCode(code string, hashes []string) int {
	clean := normaliseBackupCode(code)
	if len(clean) != 8 {
		return -1
	}
	for i, h := range hashes {
		if CheckPassword(h, clean) {
			return i
		}
	}
	return -1
}

// ProvisioningURI builds the otpauth:// URI for authenticator apps.
func ProvisioningURI(secret, account, issuer string) string {
	label := url.PathEscape(account)
	if issuer != "" {
		label = url.PathEscape(issuer) + ":" + url.PathEscape(account)
	}
	q := "secret=" + url.QueryEscape(secret) + "&issuer=" + strings.ReplaceAll(url.QueryEscape(issuer), "+", "%20") +
		"&algorithm=SHA1&digits=6&period=30"
	return "otpauth://totp/" + label + "?" + q
}
