package winprotect

import (
	"errors"
	"strings"
	"testing"
)

func TestTransactionFaultsNeverCommitPartialReplacement(t *testing.T) {
	for _, tc := range []struct{ fail, want string }{
		{"", "begin,body,commit"}, {"begin", "begin"}, {"body", "begin,body,abort"}, {"commit", "begin,body,commit,abort"},
	} {
		t.Run(tc.fail+"transaction", func(t *testing.T) {
			var calls []string
			step := func(name string) func() error {
				return func() error {
					calls = append(calls, name)
					if name == tc.fail {
						return errors.New(name)
					}
					return nil
				}
			}
			err := transaction(step("begin"), step("commit"), step("abort"), step("body"))
			if strings.Join(calls, ",") != tc.want || (err != nil) != (tc.fail != "") {
				t.Fatalf("calls=%v err=%v", calls, err)
			}
		})
	}
	failure := errors.New("filter add failure")
	abortFailure := errors.New("abort failure")
	err := transaction(func() error { return nil }, func() error { t.Fatal("commit after failure"); return nil }, func() error { return abortFailure }, func() error { return failure })
	if !errors.Is(err, failure) || !errors.Is(err, abortFailure) {
		t.Fatal("lost transaction failure")
	}
}
