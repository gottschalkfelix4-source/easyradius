package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Eine config.json aus Version 1.6 muss ohne Verlust übernommen werden.
func TestMigrateFrom16(t *testing.T) {
	dir := t.TempDir()
	old := `{
  "settings": {"authPort": 1812, "acctPort": 1813, "webAddr": ":8088", "serverName": "Alt",
    "adminUser": "admin", "adminHash": "$2a$10$abcdefghijklmnopqrstuuJ5Rb2Yk7b8jv6YqZ0x6hG7cYV6Gk2", "sessionKey": "k",
    "logRetention": 5000, "ad": {"mode": "off"}, "acme": {"credentials": {"CF_DNS_API_TOKEN": "cf-geheim-123"}}},
  "users": [{"id": "u1", "username": "max", "ntHash": "0123456789abcdef0123456789abcdef", "enabled": true}],
  "clients": [{"id": "c1", "name": "AP", "address": "10.0.0.1", "secret": "ap-geheim-123"}],
  "groups": []
}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.NeedsSetup() {
		t.Fatal("Administrator nicht übernommen")
	}
	a, ok := s.FindAdmin("admin")
	if !ok || a.Role != RoleAdmin || !strings.HasPrefix(a.Hash, "$2a$") {
		t.Fatalf("Admin: %+v", a)
	}
	c := s.Clients()[0]
	if c.Secret != "ap-geheim-123" || c.RequireMA {
		t.Fatalf("Gerät: %+v (bestehende Geräte behalten ihr Verhalten)", c)
	}
	st := s.Settings()
	if st.HTTPSAddr != ":8443" || !st.Lockout.Enabled || !st.EAP.TTLS || st.AdminHash != "" {
		t.Fatalf("Einstellungen: %+v", st)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	for _, secret := range []string{"ap-geheim-123", "cf-geheim-123", "0123456789abcdef0123456789abcdef"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("%s steht nach der Übernahme im Klartext in config.json", secret)
		}
	}
	// Erneut öffnen: alles wieder lesbar, Sicherung vorhanden.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Settings().ACME.Credentials["CF_DNS_API_TOKEN"] != "cf-geheim-123" {
		t.Fatal("Zugangsdaten nach erneutem Laden falsch")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json.bak")); err != nil {
		t.Fatal("keine Rückfallkopie")
	}
}

// Ist config.json beschädigt, wird die Rückfallkopie genommen.
func TestFallbackToBackup(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.SaveClient(Client{Name: "AP", Address: "10.0.0.2", Secret: "geheim-12345"})
	s.SaveClient(Client{Name: "AP2", Address: "10.0.0.3", Secret: "geheim-12345"})
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{kaputt"), 0o600)
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.Clients()) == 0 {
		t.Fatal("Rückfallkopie nicht verwendet")
	}
}

func TestNormalizeMAC(t *testing.T) {
	for in, want := range map[string]string{
		"AA:BB:CC:DD:EE:FF": "aa-bb-cc-dd-ee-ff",
		"aabb.ccdd.eeff":    "aa-bb-cc-dd-ee-ff",
		"aabbccddeeff":      "aa-bb-cc-dd-ee-ff",
		"aa-bb-cc-dd-ee":    "",
		"max":               "",
	} {
		if got := NormalizeMAC(in); got != want {
			t.Errorf("%q → %q, erwartet %q", in, got, want)
		}
	}
}

func TestLastAdminProtected(t *testing.T) {
	s, _ := Open(t.TempDir())
	a, _ := s.CreateFirstAdmin("chef", "$2a$10$x")
	if err := s.DeleteAdmin(a.ID); err == nil {
		t.Fatal("letzter Admin gelöscht")
	}
	a.Role = RoleViewer
	if _, err := s.SaveAdmin(a); err == nil {
		t.Fatal("letzter Admin herabgestuft")
	}
}
