//go:build !windows

package winproxy

// New is intentionally unavailable off Windows. Inject fakes to test the
// platform-independent transaction logic with NewWithDependencies.
func New(statePath string) (*Manager, error) { return nil, ErrUnsupported }
