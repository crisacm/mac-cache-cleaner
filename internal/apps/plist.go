package apps

import (
	"howett.net/plist"
)

// decodePlist decodes a property list from raw bytes into v.
func decodePlist(data []byte, v any) error {
	_, err := plist.Unmarshal(data, v)
	return err
}
