package cms

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/argon2"
)

const passwordMemoryKiB = 19456

func validatePassword(password string) error {
	if !utf8.ValidString(password) || len(password) > 512 || utf8.RuneCountInString(password) < 15 || utf8.RuneCountInString(password) > 128 {
		return fmt.Errorf("password must contain 15–128 Unicode code points and at most 512 UTF-8 bytes")
	}
	// Exact whole-password comparison deliberately adds no composition rules.
	switch strings.ToLower(password) {
	case "passwordpassword", "passwordpasswordpassword", "123456789012345", "1234567890123456", "qwertyuiopasdfgh", "letmeinletmeinletmein", "correct horse battery staple":
		return fmt.Errorf("password is blocklisted")
	}
	return nil
}

func hashPassword(password string) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, 2, passwordMemoryKiB, 1, 32)
	return fmt.Sprintf("$argon2id$v=19$m=19456,t=2,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func verifyPassword(password, phc string) bool {
	if len(password) > 512 || !utf8.ValidString(password) {
		return false
	}
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=19456,t=2,p=1" {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) != 32 {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, 2, passwordMemoryKiB, 1, 32)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

func newSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}
func recoveryCodes() ([]string, [][]byte, error) {
	codes := make([]string, 10)
	digests := make([][]byte, 10)
	for i := range codes {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, err
		}
		codes[i] = hex.EncodeToString(b)
		d := sha256.Sum256([]byte(codes[i]))
		digests[i] = d[:]
	}
	return codes, digests, nil
}

func seedAD(site, user string, version int) []byte {
	return []byte("stepolonia/totp/" + strconv.Itoa(len(site)) + ":" + site + "/" + strconv.Itoa(version) + "/" + user)
}
func encryptSeed(key []byte, site, user string, version int, secret string) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, []byte(secret), seedAD(site, user, version)), nil
}
func decryptSeed(key []byte, site, user string, version int, data []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < aead.NonceSize() {
		return "", fmt.Errorf("invalid encrypted seed")
	}
	plain, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], seedAD(site, user, version))
	if err != nil {
		return "", fmt.Errorf("TOTP encryption key unavailable or seed invalid")
	}
	return string(plain), nil
}

// acceptedCounter uses database time and returns the highest matching unused step.
func acceptedCounter(secret, code string, now time.Time, last int64) (int64, error) {
	if len(code) != 6 {
		return 0, fmt.Errorf("%w: invalid TOTP", errBootstrapDenied)
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%w: invalid TOTP", errBootstrapDenied)
		}
	}
	current := now.Unix() / 30
	for counter := current + 1; counter >= current-1; counter-- {
		if counter < 0 || counter <= last {
			continue
		}
		expected, err := totp.GenerateCodeCustom(secret, time.Unix(counter*30, 0), totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err != nil {
			return 0, fmt.Errorf("invalid TOTP secret")
		}
		if subtle.ConstantTimeCompare([]byte(code), []byte(expected)) == 1 {
			return counter, nil
		}
	}
	return 0, fmt.Errorf("%w: invalid or replayed TOTP", errBootstrapDenied)
}
