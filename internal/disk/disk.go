// Package disk reports the free space of the disk a path lives on.
package disk

// Usage is the space on one disk, in bytes. Free is what an ordinary user can
// still write, which is less than the raw free space when quotas apply.
type Usage struct {
	Free  uint64 `json:"free"`
	Total uint64 `json:"total"`
}
