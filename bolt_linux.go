package bolt

import (
	"syscall"
)

// fdatasync flushes written data to a file descriptor.
func fdatasync(db *DB) error {
	return syscall.Fdatasync(int(db.file.Fd()))
}

// fileSync flushes the whole database file, size included, as the file grows.
func fileSync(db *DB) error {
	return db.file.Sync()
}
