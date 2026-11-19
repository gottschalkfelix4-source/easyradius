package letsencrypt

import (
	"os"
	"testing"
)

// Jeder Anbieter muss sich mit seinen Feldern aufbauen lassen; danach dürfen
// keine Zugangsdaten in der Umgebung zurückbleiben.
func TestProvidersBuild(t *testing.T) {
	if len(Providers()) < 40 {
		t.Fatalf("nur %d Anbieter", len(Providers()))
	}
	for _, p := range Providers() {
		creds := map[string]string{}
		for _, f := range p.Fields {
			creds[f.Env] = "test-1234"
		}
		c := Config{Method: MethodDNS, Provider: p.Code, Credentials: creds}
		// Fehler sind erlaubt (z. B. Formatprüfungen), Abstürze nicht.
		c.provider()
		for k := range creds {
			if os.Getenv(k) != "" {
				t.Fatalf("%s: Umgebungsvariable %s nicht aufgeräumt", p.Code, k)
			}
		}
	}
	c := Config{Method: MethodACMEDNS, DataDir: t.TempDir()}
	if _, err := c.provider(); err != nil {
		t.Fatalf("acme-dns: %v", err)
	}
	if _, err := (&Config{Method: MethodDNS, Provider: "gibtsnicht"}).provider(); err == nil {
		t.Fatal("unbekannter Anbieter akzeptiert")
	}
}
