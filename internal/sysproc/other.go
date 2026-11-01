//go:build !windows

package sysproc

// Lower does nothing here: the other platforms get their own implementation
// together with their schedulers.
func Lower() error { return nil }

// Contain does nothing here.
func Contain() (*Group, error) { return &Group{}, nil }

func (g *Group) peak() uint64 { return 0 }

// Close does nothing here.
func (g *Group) Close() {}
