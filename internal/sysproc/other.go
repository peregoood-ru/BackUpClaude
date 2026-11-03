//go:build !windows

package sysproc

// Lower does nothing here: the other platforms get their own implementation
// together with their schedulers.
func Lower() error { return nil }

// Contain does nothing here.
func Contain() (*Group, error) { return &Group{}, nil }

func (g *Group) peak() uint64 { return 0 }

// Close forgets the handle, which is never set here.
func (g *Group) Close() {
	if g != nil {
		g.handle = 0
	}
}
