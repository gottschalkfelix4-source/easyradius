//go:build !windows

package main

import (
	"os"
	"os/exec"
)

// Unter Linux läuft EasyRadius als systemd-Dienst im Vordergrund.
func isService() bool { return false }

func runService(string) error { return nil }

// secureDir beschränkt das Datenverzeichnis auf den Dienstbenutzer.
func secureDir(path string) error { return os.Chmod(path, 0o700) }

func openBrowser(url string) error { return exec.Command("xdg-open", url).Start() }
