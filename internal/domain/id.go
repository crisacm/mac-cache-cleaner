package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

// ComputeEntryID derives the stable identifier of an indexed entry from its
// scan and its (absolute, symlink-resolved) path. It is deterministic so that
// plans created from one scan can be resolved in the same scan.
func ComputeEntryID(scanID, path string) string {
	sum := sha256.Sum256([]byte(scanID + "\x00" + path))
	return hex.EncodeToString(sum[:16])
}

// NewID returns a fresh random identifier (16 bytes, hex encoded).
func NewID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// HashStrings returns a stable content hash over the given strings.
func HashStrings(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
