//go:build !windows && !plan9 && !linux && !openbsd
// +build !windows,!plan9,!linux,!openbsd

package bolt

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

// fsyncRecorder replaces fsyncFD for one test: each call records the
// descriptor and returns the next scripted result, then fsyncs for real.
func fsyncRecorder(t *testing.T, results ...error) *[]int {
	original := fsyncFD
	t.Cleanup(func() { fsyncFD = original })
	synced := &[]int{}
	fsyncFD = func(fd int) error {
		*synced = append(*synced, fd)
		if len(results) > 0 {
			result := results[0]
			results = results[1:]
			return result
		}
		return original(fd)
	}
	return synced
}

func openSyncTestDB(t *testing.T) *DB {
	dir, err := ioutil.TempDir("", "bolt-sync-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	db, err := Open(filepath.Join(dir, "db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func putOne(db *DB) error {
	return db.Update(func(tx *Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("b"))
		if err != nil {
			return err
		}
		return b.Put([]byte("k"), []byte("v"))
	})
}

// Every sync a commit makes goes through fsync(2) on the database's own
// descriptor: the file growing under it, its data pages, then its meta page. A
// File.Sync here would be the full drive-cache flush it is on darwin.
func TestCommitSyncsWithFsync(t *testing.T) {
	if reflect.ValueOf(fsyncFD).Pointer() != reflect.ValueOf(syscall.Fsync).Pointer() {
		t.Fatal("a commit's sync is not syscall.Fsync")
	}
	db := openSyncTestDB(t)
	synced := fsyncRecorder(t)
	if err := putOne(db); err != nil {
		t.Fatal(err)
	}
	fd := int(db.file.Fd())
	if len(*synced) != 3 || (*synced)[0] != fd || (*synced)[1] != fd || (*synced)[2] != fd {
		t.Fatalf("a commit that grew the file made fsync calls %v, want three on the database's descriptor %d: the growth, the data, the meta", *synced, fd)
	}
	*synced = nil
	if err := putOne(db); err != nil {
		t.Fatal(err)
	}
	if len(*synced) != 2 || (*synced)[0] != fd || (*synced)[1] != fd {
		t.Fatalf("a commit inside the file made fsync calls %v, want two on the database's descriptor %d: the data, the meta", *synced, fd)
	}
}

// A signal that interrupts fsync is not a failed commit: the sync retries
// until fsync answers, as File.Sync does, and a real failure fails the commit
// worded as File.Sync words it.
func TestCommitSyncRetriesAnInterruptedFsync(t *testing.T) {
	db := openSyncTestDB(t)
	synced := fsyncRecorder(t, syscall.EINTR, syscall.EINTR)
	if err := putOne(db); err != nil {
		t.Fatalf("an interrupted fsync failed the commit: %v", err)
	}
	if len(*synced) != 5 {
		t.Fatalf("the commit made %d fsync call(s), want two interrupted and three answered", len(*synced))
	}
	fsyncRecorder(t, syscall.EIO)
	err := putOne(db)
	pathErr, ok := err.(*os.PathError)
	if !ok || pathErr.Op != "sync" || pathErr.Path != db.file.Name() || pathErr.Err != syscall.EIO {
		t.Fatalf("a failed fsync came back as %v, want sync %s: EIO", err, db.file.Name())
	}
}

// BenchmarkCommitSync measures one small commit, its two syncs included, as
// it syncs now and as it synced before, with File.Sync, which on darwin is
// F_FULLFSYNC, a full drive-cache flush:
// go test -run '^$' -bench BenchmarkCommitSync .
func BenchmarkCommitSync(b *testing.B) {
	b.Run("fsync", func(b *testing.B) { benchmarkCommit(b, nil) })
	b.Run("File.Sync", func(b *testing.B) { benchmarkCommit(b, (*os.File).Sync) })
}

func benchmarkCommit(b *testing.B, sync func(*os.File) error) {
	dir, err := ioutil.TempDir("", "bolt-sync-bench-")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(dir)
	db, err := Open(filepath.Join(dir, "db"), 0600, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	if sync != nil {
		original := fsyncFD
		defer func() { fsyncFD = original }()
		fsyncFD = func(int) error { return sync(db.file) }
	}
	if err := putOne(db); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := putOne(db); err != nil {
			b.Fatal(err)
		}
	}
}
