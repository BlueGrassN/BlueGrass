package update

import (
	"context"
	"crypto/subtle"
	"errors"
	"os"
)

type artifactFile = os.File

// TrustedPublisher validates Authenticode with WinVerifyTrust and returns the
// SHA-256 of the actual signer certificate DER as 64 lowercase hex characters.
// It does not validate IPC PID, token, executable location, or directory ACLs.
func TrustedPublisher(path string) (string, error) {
	return trustedPublisher(path, false)
}

// TrustedPublisherOffline requires the same Authenticode and revocation policy
// using cached URL material only. Missing cached trust fails closed; it never
// falls back to an online check. Intended for offline startup policy reads.
func TrustedPublisherOffline(path string) (string, error) {
	return trustedPublisher(path, true)
}

func trustedPublisher(path string, offline bool) (string, error) {
	f, err := openArtifact(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return trustedFilePublisherMode(f, offline)
}

// VerifyPublisher compares the verified signer with a fingerprint previously
// obtained from a trusted product executable, never a publisher display name.
func VerifyPublisher(path, expected string) error {
	if !validHash(expected) {
		return errors.New("invalid expected publisher fingerprint")
	}
	actual, err := TrustedPublisher(path)
	if err != nil {
		return err
	}
	return comparePublisher(actual, expected)
}

func comparePublisher(actual, expected string) error {
	if !validHash(expected) || subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
		return errors.New("publisher certificate mismatch")
	}
	return nil
}

func verifyArtifactPublisher(ctx context.Context, f *artifactFile, expected string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	actual, err := trustedFilePublisher(f)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return comparePublisher(actual, expected)
}
