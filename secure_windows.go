package main

import (
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// secureDir beschränkt das Datenverzeichnis auf SYSTEM und Administratoren,
// denn dort liegen Shared Secrets und Passwort-Hashes.
func secureDir(path string) error {
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// openBrowser öffnet die Weboberfläche als eigenes App-Fenster (Microsoft Edge im
// App-Modus: ohne Adressleiste und Tabs). Ohne Edge wird der Standardbrowser genutzt.
func openBrowser(url string) error {
	if edge := edgePath(); edge != "" {
		profile := filepath.Join(os.Getenv("LOCALAPPDATA"), "EasyRadius", "Fenster")
		err := exec.Command(edge, "--app="+url, "--window-size=1280,860",
			"--user-data-dir="+profile, "--no-first-run", "--no-default-browser-check").Start()
		if err == nil {
			return nil
		}
	}
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

func edgePath() string {
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		k, err := registry.OpenKey(root, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		p, _, err := k.GetStringValue("")
		k.Close()
		if err == nil {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	for _, p := range []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(os.Getenv("ProgramFiles"), `Microsoft\Edge\Application\msedge.exe`),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
