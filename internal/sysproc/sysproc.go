// Package sysproc keeps a scheduled run polite and tidy: it lowers the CPU, disk
// and memory priority of the program, which the programs it starts inherit, and
// ties them to this one so that none outlives it. The per-OS code sits in
// windows.go and other.go.
package sysproc

// Group is the set of processes of this program and the ones it starts.
type Group struct {
	handle uintptr
}

// PeakMemory returns the most memory, in bytes, the group's processes held
// together at any time. It is zero where the OS cannot say.
func (g *Group) PeakMemory() uint64 {
	if g == nil {
		return 0
	}
	return g.peak()
}
