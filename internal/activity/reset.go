package activity

import (
	"fmt"
	"os"
	"time"
)

// ResetResult reports what a reset did with the previous database.
type ResetResult struct {
	Archived    bool
	ArchivePath string
}

// companionSuffixes are the files SQLite may keep beside the main database.
var companionSuffixes = []string{"", "-wal", "-shm"}

// Reset replaces the activity database at path with a fresh empty one. The
// previous database and its WAL/SHM companions are archived under one
// timestamp; the archive is never deleted.
//
// current is the live handle (nil when the store is already degraded). It is
// closed before anything is renamed, so no open handle pins the old files.
//
// Reset is all-or-nothing: a failed rename or a fresh database that cannot open
// rolls the archive back and leaves every original file in place. The returned
// store is non-nil exactly when err is nil.
func Reset(current *DB, path string, now time.Time) (*DB, ResetResult, error) {
	if current != nil {
		if err := current.Close(); err != nil {
			return nil, ResetResult{}, fmt.Errorf("closing the activity database: %w", err)
		}
	}
	stamp := now.UTC().Format("20060102T150405.000000000Z")
	renamed, result, err := archiveCompanions(path, stamp)
	if err != nil {
		restoreCompanions(renamed)
		return nil, ResetResult{}, err
	}
	fresh, err := Open(path)
	if err != nil {
		restoreCompanions(renamed)
		return nil, ResetResult{}, fmt.Errorf("creating the fresh activity database: %w", err)
	}
	return fresh, result, nil
}

// renamedFile remembers one archive rename so a later failure can undo it.
type renamedFile struct {
	original string
	archived string
}

// archiveCompanions moves every existing database file to its timestamped
// archive name. It returns the renames performed so far, so the caller can undo
// them; on error the caller owns the rollback.
func archiveCompanions(path, stamp string) ([]renamedFile, ResetResult, error) {
	renamed := []renamedFile{}
	result := ResetResult{}
	for _, suffix := range companionSuffixes {
		source := path + suffix
		if _, err := os.Stat(source); err != nil {
			continue
		}
		target := fmt.Sprintf("%s.archive-%s%s", path, stamp, suffix)
		if err := os.Rename(source, target); err != nil {
			return renamed, ResetResult{}, fmt.Errorf("archiving %s: %w", source, err)
		}
		renamed = append(renamed, renamedFile{original: source, archived: target})
		if suffix == "" {
			result.Archived = true
			result.ArchivePath = target
		}
	}
	return renamed, result, nil
}

// restoreCompanions undoes archive renames in reverse order. Failures are
// reported through the caller's error path; there is no second-chance recovery
// because the archive is never deleted.
func restoreCompanions(renamed []renamedFile) {
	for i := len(renamed) - 1; i >= 0; i-- {
		_ = os.Rename(renamed[i].archived, renamed[i].original)
	}
}
