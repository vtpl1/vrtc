package recorder

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// CheckDiskSpace returns available and total bytes for the filesystem containing path.
func CheckDiskSpace(path string) (availableBytes, totalBytes int64, err error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}

	var freeBytesAvailable, totalNumberOfBytes uint64

	err = windows.GetDiskFreeSpaceEx(
		pathPtr,
		(*uint64)(unsafe.Pointer(&freeBytesAvailable)),
		(*uint64)(unsafe.Pointer(&totalNumberOfBytes)),
		nil,
	)
	if err != nil {
		return 0, 0, err
	}

	return int64(freeBytesAvailable), int64(totalNumberOfBytes), nil
}
