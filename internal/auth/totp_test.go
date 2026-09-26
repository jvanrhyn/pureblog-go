package auth

import (
	"testing"
	"time"
)

// RFC 6238 test vector: secret "12345678901234567890" at T=59 → 287082 (6 digits).
func TestTOTPCode(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	if got := TOTPCode(secret, time.Unix(59, 0)); got != "287082" {
		t.Fatalf("TOTPCode = %s, want 287082", got)
	}
}

func TestBackupCodes(t *testing.T) {
	codes := GenerateBackupCodes(3)
	hashes := HashBackupCodes(codes)
	if i := VerifyBackupCode(codes[1], hashes); i != 1 {
		t.Fatalf("VerifyBackupCode = %d, want 1", i)
	}
	if i := VerifyBackupCode("zzzz-zzzz", hashes); i != -1 {
		t.Fatalf("unexpected match %d", i)
	}
}

func TestPHPBcrypt(t *testing.T) {
	// PHP's password_hash() emits $2y$ hashes; bcrypt treats them like $2a$.
	h, _ := HashPassword("secret")
	if !CheckPassword("$2y"+h[3:], "secret") {
		t.Fatal("$2y$ prefix not accepted")
	}
}
