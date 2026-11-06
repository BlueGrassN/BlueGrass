//go:build !windows

package update

import (
	"errors"
	"os"
)

func openArtifact(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("artifact must be a regular file")
	}
	return os.Open(path)
}
func trustedFilePublisher(*os.File) (string, error) {
	return "", errors.New("Authenticode verification requires Windows")
}
func trustedFilePublisherMode(f *os.File, _ bool) (string, error) {
	return trustedFilePublisher(f)
}
