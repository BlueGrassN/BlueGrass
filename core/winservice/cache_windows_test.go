//go:build windows

package winservice

import (
	"errors"
	"os"
	"slices"
	"testing"
)

func TestOwnedCacheAbsenceAndErrorsPreserveReleaseBoundary(t *testing.T) {
	const path = `C:\ProgramData\BlueGrass\fakeip.db`
	denied := errors.New("strict cache ACL rejected")
	for _, tc := range []struct {
		name                       string
		statErr, clearErr, wantErr error
		wantClear                  bool
	}{
		{"absent", &os.PathError{Op: "lstat", Path: path, Err: os.ErrNotExist}, nil, nil, false},
		{"stat error", &os.PathError{Op: "lstat", Path: path, Err: os.ErrPermission}, nil, os.ErrPermission, false},
		{"present", nil, nil, nil, true},
		{"strict clear error", nil, denied, denied, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleared := false
			err := clearCacheIfPresent(path, func(actual string) (os.FileInfo, error) {
				if actual != path {
					t.Fatal("lstat targeted a different path")
				}
				return nil, tc.statErr
			}, func(actual string) error {
				if actual != path {
					t.Fatal("clear targeted a different path")
				}
				cleared = true
				return tc.clearErr
			})
			if !errors.Is(err, tc.wantErr) || cleared != tc.wantClear {
				t.Fatalf("err=%v cleared=%v", err, cleared)
			}
		})
	}
}

func TestUninstallRepairStopsBeforeWFPReleaseOnOwnedCleanupFailure(t *testing.T) {
	for _, failure := range []string{"adapter", "cache", "wfp", ""} {
		var events []string
		failed := errors.New("owned repair failed")
		step := func(name string) func() error {
			return func() error {
				events = append(events, name)
				if name == failure {
					return failed
				}
				return nil
			}
		}
		err := repairOwnedNetwork(step("adapter"), step("cache"), step("wfp"))
		want := []string{"adapter", "cache", "wfp"}
		if failure != "" {
			want = want[:slices.Index(want, failure)+1]
			if !errors.Is(err, failed) {
				t.Fatal("repair error discarded", failure, err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(events, want) {
			t.Fatal("unsafe repair/release order", events, want)
		}
	}
}
