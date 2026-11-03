//go:build !windows

package syncdir

// systemRoots has nothing to report: the OS gives no list of synchronised
// folders here, and the names and markers do the work.
func systemRoots() []Match { return nil }
