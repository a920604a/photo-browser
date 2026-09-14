package indexer

import "golang.org/x/sys/unix"

// FreeBytes reports the space available to an unprivileged writer at path.
// Bavail rather than Bfree: Bfree counts blocks reserved for root, which the
// non-root service user cannot actually use.
func FreeBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
