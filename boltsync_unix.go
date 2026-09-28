//go:build !windows && !plan9 && !linux && !openbsd
// +build !windows,!plan9,!linux,!openbsd

package bolt

import (
	"os"
	"syscall"
)

// fsyncFD is fsync(2); tests replace it to see a commit's syncs reach it.
var fsyncFD = syscall.Fsync

// fdatasync flushes written data to a file descriptor with fsync(2); see
// fileSync.
func fdatasync(db *DB) error {
	return fileSync(db)
}

// fileSync flushes the database file with fsync(2), for a commit and for the
// file growing under it alike.
//
// Bolt was written when File.Sync was fsync(2) here. Since Go 1.12 File.Sync
// on darwin is fcntl(F_FULLFSYNC), a flush of the drive's own write cache,
// which costs milliseconds per call, and a commit syncs twice. fsync(2) still
// puts the data pages, then the meta page, on the drive before the commit
// returns, so a process crash or a kernel panic loses nothing; only a sudden
// power loss can drop what the drive itself still caches.
//
// The descriptor is held open for the call, a signal that interrupts fsync is
// retried as File.Sync retries it, and an error is worded as File.Sync words
// it.
func fileSync(db *DB) error {
	raw, err := db.file.SyscallConn()
	if err != nil {
		return err
	}
	var syncErr error
	if err := raw.Control(func(fd uintptr) {
		for {
			if syncErr = fsyncFD(int(fd)); syncErr != syscall.EINTR {
				return
			}
		}
	}); err != nil {
		syncErr = err
	}
	if syncErr != nil {
		return &os.PathError{Op: "sync", Path: db.file.Name(), Err: syncErr}
	}
	return nil
}
