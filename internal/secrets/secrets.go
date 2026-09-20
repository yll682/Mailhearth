// Package secrets implements the small amount of cryptography Mailhearth
// needs: sealing credentials at rest, hashing passwords and minting tokens.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Box seals and opens secrets with AES-256-GCM using a key derived from the
// installation master key.
type Box struct {
	aead cipher.AEAD
}

// LoadMasterKey returns the 32-byte master key. It is read from
// MAILHEARTH_MASTER_KEY (base64 or hex) when set, otherwise from
// <dataDir>/master.key which is generated on first start. The bool reports
// whether a new key was generated.
func LoadMasterKey(dataDir string) ([]byte, bool, error) {
	if v := strings.TrimSpace(os.Getenv("MAILHEARTH_MASTER_KEY")); v != "" {
		key, err := decodeKey(v)
		if err != nil {
			return nil, false, fmt.Errorf("MAILHEARTH_MASTER_KEY: %w", err)
		}
		return key, false, nil
	}
	path := filepath.Join(dataDir, "master.key")
	if b, err := os.ReadFile(path); err == nil {
		key, err := decodeKey(strings.TrimSpace(string(b)))
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", path, err)
		}
		return key, false, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, false, err
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0o600); err != nil {
		return nil, false, err
	}
	return key, true, nil
}

func decodeKey(v string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(v); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(v); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := hex.DecodeString(v); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, errors.New("master key must be 32 bytes encoded as base64 or hex")
}

// NewBox derives a purpose-specific key from the master key.
func NewBox(master []byte, purpose string) (*Box, error) {
	if len(master) != 32 {
		return nil, errors.New("master key must be 32 bytes")
	}
	key, err := hkdf.Key(sha256.New, master, nil, "mailhearth/"+purpose, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext; the output is safe to store in a TEXT column.
func (b *Box) Seal(plain string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := b.aead.Seal(nil, nonce, []byte(plain), nil)
	return "v1:" + base64.RawStdEncoding.EncodeToString(append(nonce, ct...)), nil
}

// Open decrypts a value produced by Seal.
func (b *Box) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	if !strings.HasPrefix(sealed, "v1:") {
		return "", errors.New("unknown sealed format")
	}
	raw, err := base64.RawStdEncoding.DecodeString(sealed[3:])
	if err != nil {
		return "", err
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("sealed value too short")
	}
	pt, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", errors.New("could not decrypt secret (master key changed?)")
	}
	return string(pt), nil
}

// Argon2id parameters follow the OWASP minimum recommendation; they are
// intentionally modest so logins stay cheap on small hosts.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
)

// HashPassword returns a PHC-formatted argon2id hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	h := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(h)), nil
}

// VerifyPassword checks password against a PHC argon2id hash.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// RandomToken returns n random bytes encoded as URL-safe base64.
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// RandomPassword returns a strong password suitable for Purelymail users.
func RandomPassword() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789-_.!"
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	out := make([]byte, len(b))
	for i, v := range b {
		out[i] = alphabet[int(v)%len(alphabet)]
	}
	return string(out)
}

// HashToken returns the hex SHA-256 of a token for storage/lookup.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Hint returns a masked preview of a secret for display.
func Hint(secret string) string {
	if len(secret) <= 6 {
		return "****"
	}
	return secret[:3] + "..." + secret[len(secret)-2:]
}
