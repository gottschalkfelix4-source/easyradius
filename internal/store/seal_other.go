//go:build !windows

package store

import "os"

// Unter Linux schützen Dateirechte (0600, Verzeichnis 0700) den Schlüssel.
func protectKey(key []byte) ([]byte, error)    { return append([]byte(nil), key...), nil }
func unprotectKey(blob []byte) ([]byte, error) { return blob, nil }

// syncDir sorgt dafür, dass das Umbenennen auch nach einem Stromausfall erhalten bleibt.
func syncDir(dir string) {
	if f, err := os.Open(dir); err == nil {
		f.Sync()
		f.Close()
	}
}
