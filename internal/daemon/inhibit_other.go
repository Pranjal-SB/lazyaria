//go:build !linux

package daemon

type noopInhibitor struct{}

func (noopInhibitor) Hold(string) error { return nil }
func (noopInhibitor) Release() error    { return nil }

// NewInhibitor is a no-op where there is no logind.
func NewInhibitor(mode string) Inhibitor { return noopInhibitor{} }
