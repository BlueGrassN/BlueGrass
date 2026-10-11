package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
	"regexp"
	"strconv"
	"strings"
)

var argonParams = regexp.MustCompile(`^m=([0-9]+),t=([0-9]+),p=([0-9]+)$`)
var UUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func VerifyPassword(encoded, password string) (bool, error) {
	if strings.HasPrefix(encoded, "$2") {
		cost, err := bcrypt.Cost([]byte(encoded))
		if err != nil || cost < 4 || cost > 16 {
			return false, errors.New("invalid bcrypt cost")
		}
		err = bcrypt.CompareHashAndPassword([]byte(encoded), []byte(password))
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return false, nil
		}
		return err == nil, err
	}
	if len(encoded) > 1024 {
		return false, errors.New("invalid password hash")
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || (parts[1] != "argon2id" && parts[1] != "argon2i") || parts[2] != "v=19" {
		return false, errors.New("unsupported password hash")
	}
	matches := argonParams.FindStringSubmatch(parts[3])
	if matches == nil {
		return false, errors.New("invalid argon parameters")
	}
	values := make([]uint32, 3)
	for i := range values {
		v, e := strconv.ParseUint(matches[i+1], 10, 32)
		if e != nil {
			return false, errors.New("invalid argon parameters")
		}
		values[i] = uint32(v)
	}
	memory, rounds, threads := values[0], values[1], values[2]
	if threads < 1 || threads > 16 || memory < 8*threads || memory > 262144 || rounds < 1 || rounds > 10 {
		return false, errors.New("argon work factor outside allowed bounds")
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false, errors.New("invalid argon salt")
	}
	expected, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(expected) < 16 || len(expected) > 64 {
		return false, errors.New("invalid argon digest")
	}
	var actual []byte
	if parts[1] == "argon2id" {
		actual = argon2.IDKey([]byte(password), salt, rounds, memory, uint8(threads), uint32(len(expected)))
	} else {
		actual = argon2.Key([]byte(password), salt, rounds, memory, uint8(threads), uint32(len(expected)))
	}
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func Digest(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }
func Fingerprint(key []byte, passwordHash string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(passwordHash))
	return m.Sum(nil)
}
func Token(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return prefix + hex.EncodeToString(b), nil
}
func UUID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

func box(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("token key must contain 32 bytes")
	}
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(b)
}
func Seal(key, plain, aad []byte) ([]byte, error) {
	g, e := box(key)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, g.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	return g.Seal(nonce, nonce, plain, aad), nil
}
func Open(key, sealed, aad []byte) ([]byte, error) {
	g, e := box(key)
	if e != nil {
		return nil, e
	}
	if len(sealed) < g.NonceSize() {
		return nil, errors.New("invalid receipt")
	}
	return g.Open(nil, sealed[:g.NonceSize()], sealed[g.NonceSize():], aad)
}
