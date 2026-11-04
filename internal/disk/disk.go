// Package disk reports the free space of the disk a path lives on.
package disk

// Usage is the space on one disk, in bytes. Free is what an ordinary user can
// still write, which is less than the raw free space when quotas apply.
type Usage struct {
	Free  uint64 `json:"free"`
	Total uint64 `json:"total"`
}

// Drive is a fixed local disk with a drive letter.
type Drive struct {
	// Root is the top folder, for example D:\.
	Root  string `json:"root"`
	Free  uint64 `json:"free"`
	Total uint64 `json:"total"`
	// System is true for the disk Windows is installed on.
	System bool `json:"system"`
}
