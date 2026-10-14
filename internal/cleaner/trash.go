package cleaner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// trashMover moves items to ~/.Trash by rename when possible and falls back to
// a platform helper otherwise.
type trashMover struct {
	home string
}

func newTrasher(home string) Trasher { return &trashMover{home: home} }

// Trash moves path to the Trash. It never follows symlinks: we only ever pass
// paths that revalidation confirmed are not symlinks.
func (t *trashMover) Trash(ctx context.Context, path string) error {
	if err := moveToTrashDir(t.home, path); err == nil {
		return nil
	} else if fallbackErr := fallbackTrash(ctx, path); fallbackErr != nil {
		return fmt.Errorf("cleaner: trash %s: %w", path, err)
	}
	return nil
}

// moveToTrashDir renames path into ~/.Trash.
func moveToTrashDir(home, path string) error {
	dir := filepath.Join(home, ".Trash")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	dest := uniqueDest(dir, filepath.Base(path))
	return os.Rename(path, dest)
}

// uniqueDest returns a non-existing destination inside dir.
func uniqueDest(dir, base string) string {
	dest := filepath.Join(dir, base)
	if _, err := os.Lstat(dest); os.IsNotExist(err) {
		return dest
	}
	for i := 2; ; i++ {
		candidate := filepath.Join(dir, base+" "+strconv.Itoa(i))
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}
