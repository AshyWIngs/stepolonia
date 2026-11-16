package cms

import (
	"context"
	"encoding/base32"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const testSite = "example.test"
const testUser = "user-a"

func TestPasswordPolicyAndPHC(t *testing.T) {
	for _, password := range []string{"short", strings.Repeat("a", 129), strings.Repeat("界", 129), "123456789012345", string([]byte{255})} {
		if validatePassword(password) == nil {
			t.Errorf("accepted invalid password length %d", len(password))
		}
	}
	password := "  long password with spaces  "
	hash, err := hashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") || !verifyPassword(password, hash) {
		t.Fatal("PHC round trip")
	}
	if verifyPassword(strings.TrimSpace(password), hash) || verifyPassword(password, strings.Replace(hash, "m=19456", "m=999999999", 1)) {
		t.Fatal("password or unbounded PHC accepted")
	}
	if err := validatePassword(strings.Repeat("界", 128)); err != nil {
		t.Fatal(err)
	}
}
func TestSeedEncryptionBinding(t *testing.T) {
	key := make([]byte, 32)
	secret, err := newSecret()
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := encryptSeed(key, testSite, testUser, 1, secret)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := decryptSeed(key, testSite, testUser, 1, encrypted)
	if err != nil || actual != secret {
		t.Fatal("AEAD round trip")
	}
	for _, c := range []struct {
		site, user string
		version    int
	}{{"other.test", testUser, 1}, {testSite, "user-b", 1}, {testSite, testUser, 2}} {
		if _, err := decryptSeed(key, c.site, c.user, c.version, encrypted); err == nil {
			t.Fatal("associated data substitution accepted")
		}
	}
	key[0] = 1
	if _, err := decryptSeed(key, testSite, testUser, 1, encrypted); err == nil {
		t.Fatal("wrong key accepted")
	}
}
func TestTOTPWindowAndReplay(t *testing.T) {
	secret := base32.StdEncoding.EncodeToString([]byte("12345678901234567890"))
	now := time.Unix(1234567890, 0)
	current := now.Unix() / 30
	for _, delta := range []int64{-2, -1, 0, 1, 2} {
		counter := current + delta
		code, err := totp.GenerateCodeCustom(secret, time.Unix(counter*30, 0), totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err != nil {
			t.Fatal(err)
		}
		got, err := acceptedCounter(secret, code, now, -1)
		checkCounterWindow(t, secret, code, now, current, counter, got, err)
	}
	if _, err := acceptedCounter(secret, "00000x", now, -1); err == nil {
		t.Fatal("invalid code accepted")
	}
	// RFC 6238 SHA-1 vector, reduced to the configured six digits.
	code, err := totp.GenerateCodeCustom(secret, time.Unix(59, 0), totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil || code != "287082" {
		t.Fatal("RFC 6238 vector failed")
	}
}
func TestRecoveryCodeEntropyAndDigests(t *testing.T) {
	codes, digests, err := recoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	if len(codes) != 10 || len(digests) != 10 {
		t.Fatal("code count")
	}
	for i, code := range codes {
		if len(code) != 32 || len(digests[i]) != 32 || seen[code] {
			t.Fatal("code shape or collision")
		}
		seen[code] = true
	}
}
func TestBootstrapDomainValidation(t *testing.T) {
	for _, login := range []string{"", " leading", "trailing ", "line\nbreak", "\x00"} {
		if _, err := normalizeLogin(login); err == nil {
			t.Fatal("invalid login accepted")
		}
	}
	a, _ := normalizeLogin("CAFÉ")
	b, _ := normalizeLogin("cafe\u0301")
	if a != b {
		t.Fatal("login canonicalization")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := bootstrapHash(ctx, "a sufficiently long password"); err == nil {
		t.Fatal("canceled password work accepted")
	}
}

func checkCounterWindow(t *testing.T, secret, code string, now time.Time, current, counter, got int64, err error) {
	t.Helper()
	if counter < current-1 || counter > current+1 {
		if err == nil {
			t.Fatal("outside window accepted")
		}
		return
	}
	if err != nil || got != counter {
		t.Fatalf("counter %d: %d %v", counter, got, err)
	}
	if _, err := acceptedCounter(secret, code, now, counter); err == nil {
		t.Fatal("replay accepted")
	}
}
