//go:build !darwin

package cleaner

import (
	"context"
	"errors"
)

// fallbackTrash is unavailable off macOS.
func fallbackTrash(context.Context, string) error {
	return errors.New("cleaner: trash fallback is only available on macOS")
}
