package secure

import (
	"encoding/base64"
	"fmt"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
	"testing"
)

func TestPHPCompatibleBcryptAndArgonHashes(t *testing.T) {
	pass := "fixture-password"
	b, _ := bcrypt.GenerateFromPassword([]byte(pass), 4)
	b[2] = 'y'
	salt := []byte("fixture-salt-1234")
	for _, kind := range []string{"argon2id", "argon2i"} {
		var hash []byte
		if kind == "argon2id" {
			hash = argon2.IDKey([]byte(pass), salt, 2, 8192, 1, 32)
		} else {
			hash = argon2.Key([]byte(pass), salt, 2, 8192, 1, 32)
		}
		encoded := fmt.Sprintf("$%s$v=19$m=8192,t=2,p=1$%s$%s", kind, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash))
		for _, e := range []string{string(b), encoded} {
			ok, err := VerifyPassword(e, pass)
			if err != nil || !ok {
				t.Fatalf("compatible hash rejected: %v", err)
			}
			ok, err = VerifyPassword(e, "wrong")
			if err != nil || ok {
				t.Fatalf("wrong password accepted: %v", err)
			}
		}
	}
}

func TestInvalidOrExcessiveHashParametersAreRejectedWithoutAllocation(t *testing.T) {
	for _, s := range []string{"garbage", "$argon2id$v=19$m=4294967295,t=1,p=1$YWJjZGVmZ2g$YWJjZGVmZ2hpamtsbW5vcA", "$argon2id$v=19$m=65536,t=9999999,p=1$YWJjZGVmZ2g$YWJjZGVmZ2hpamtsbW5vcA"} {
		ok, err := VerifyPassword(s, "x")
		if err == nil || ok {
			t.Fatal("malformed/unsafe hash accepted")
		}
	}
}
