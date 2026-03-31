package recorder

import "syscall"

// CheckDiskSpace returns available and total bytes for the filesystem containing path.
func CheckDiskSpace(path string) (availableBytes, totalBytes int64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}

	availableBytes = int64(stat.Bavail) * stat.Bsize
	totalBytes = int64(stat.Blocks) * stat.Bsize

	return availableBytes, totalBytes, nil
}
