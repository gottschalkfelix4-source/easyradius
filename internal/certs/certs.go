// Package certs erzeugt und lädt die Zertifikate für PEAP (WLAN-Enterprise).
package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/idna"
)

const (
	caCertFile     = "ca.crt"
	caKeyFile      = "ca.key"
	serverCertFile = "server.crt"
	serverKeyFile  = "server.key"
)

type Manager struct {
	dir  string
	mu   sync.RWMutex
	cert *tls.Certificate
}

// Info beschreibt das aktive Serverzertifikat für die GUI.
type Info struct {
	Subject     string    `json:"subject"`
	Issuer      string    `json:"issuer"`
	NotBefore   time.Time `json:"notBefore"`
	NotAfter    time.Time `json:"notAfter"`
	Fingerprint string    `json:"fingerprint"`
	SelfSigned  bool      `json:"selfSigned"` // von der eigenen CA ausgestellt
	HasCA       bool      `json:"hasCa"`
	Public      bool      `json:"public"` // von einer öffentlichen Stelle (z. B. Let's Encrypt)
}

// issuedByOwnCA prüft, ob das Zertifikat von der EasyRadius-CA stammt.
func (m *Manager) issuedByOwnCA(leaf *x509.Certificate) bool {
	b, err := os.ReadFile(m.path(caCertFile))
	if err != nil {
		return false
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return false
	}
	ca, err := x509.ParseCertificate(blk.Bytes)
	return err == nil && leaf.CheckSignatureFrom(ca) == nil
}

// isPubliclyTrusted prüft die Kette gegen die Windows-Zertifikatsspeicher.
func isPubliclyTrusted(c *tls.Certificate) bool {
	inter := x509.NewCertPool()
	for _, d := range c.Certificate[1:] {
		if ic, err := x509.ParseCertificate(d); err == nil {
			inter.AddCert(ic)
		}
	}
	_, err := c.Leaf.Verify(x509.VerifyOptions{Intermediates: inter})
	return err == nil
}

// CACertPEM liefert das CA-Zertifikat nur, wenn das aktive Zertifikat von ihr stammt.
func (m *Manager) ActiveCACertPEM() ([]byte, error) {
	if !m.issuedByOwnCA(m.Certificate().Leaf) {
		return nil, os.ErrNotExist
	}
	return m.CACertPEM()
}

func NewManager(dir string) (*Manager, error) {
	dir = filepath.Join(dir, "certs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	m := &Manager{dir: dir}
	if _, err := os.Stat(filepath.Join(dir, serverCertFile)); errors.Is(err, os.ErrNotExist) {
		if err := m.Generate("EasyRadius"); err != nil {
			return nil, err
		}
	}
	return m, m.load()
}

func (m *Manager) path(f string) string { return filepath.Join(m.dir, f) }

func (m *Manager) load() error {
	c, err := tls.LoadX509KeyPair(m.path(serverCertFile), m.path(serverKeyFile))
	if err != nil {
		return fmt.Errorf("Serverzertifikat laden: %w", err)
	}
	c.Leaf, _ = x509.ParseCertificate(c.Certificate[0])
	m.mu.Lock()
	m.cert = &c
	m.mu.Unlock()
	return nil
}

func (m *Manager) Certificate() *tls.Certificate {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cert
}

// Generate erzeugt eine eigene Zertifizierungsstelle und ein Serverzertifikat.
func (m *Manager) Generate(name string) error {
	if name == "" {
		name = "EasyRadius"
	}
	now := time.Now().Add(-time.Hour)

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: name + " CA", Organization: []string{"EasyRadius"}},
		NotBefore:             now,
		NotAfter:              now.AddDate(20, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	caCert, _ := x509.ParseCertificate(caDER)

	// RSA für das Serverzertifikat: maximal kompatibel mit älteren Clients.
	srvKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	srvTmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: name, Organization: []string{"EasyRadius"}},
		DNSNames:     dnsNames(name),
		NotBefore:    now,
		// Apple-Geräte akzeptieren Serverzertifikate mit max. ~825 Tagen Laufzeit.
		NotAfter:    now.AddDate(0, 0, 820),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTmpl, caCert, &srvKey.PublicKey, caKey)
	if err != nil {
		return err
	}

	caKeyDER, _ := x509.MarshalPKCS8PrivateKey(caKey)
	srvKeyDER, _ := x509.MarshalPKCS8PrivateKey(srvKey)
	files := map[string][]byte{
		caCertFile:     pemBlock("CERTIFICATE", caDER),
		caKeyFile:      pemBlock("PRIVATE KEY", caKeyDER),
		serverCertFile: append(pemBlock("CERTIFICATE", srvDER), pemBlock("CERTIFICATE", caDER)...),
		serverKeyFile:  pemBlock("PRIVATE KEY", srvKeyDER),
	}
	if err := m.writeFiles(files); err != nil {
		return err
	}
	return m.load()
}

// writeFiles schreibt mehrere Dateien möglichst atomar: erst alle als .tmp,
// dann umbenennen. Ein Fehler beim Schreiben lässt die alten Dateien unberührt.
func (m *Manager) writeFiles(files map[string][]byte) error {
	for f, b := range files {
		if err := os.WriteFile(m.path(f)+".tmp", b, 0o600); err != nil {
			for g := range files {
				os.Remove(m.path(g) + ".tmp")
			}
			return err
		}
	}
	m.mu.Lock() // kein load() zwischen den Umbenennungen
	defer m.mu.Unlock()
	for f := range files {
		if err := os.Rename(m.path(f)+".tmp", m.path(f)); err != nil {
			return err
		}
	}
	return nil
}

// dnsNames macht aus dem Servernamen einen gültigen DNS-Namen für das Zertifikat
// ("Radius Büro" → "radius-xn--bro-hoa"). Der Anzeigename bleibt im CN erhalten.
func dnsNames(name string) []string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.Map(func(r rune) rune {
		switch {
		case r == ' ' || r == '_':
			return '-'
		case r < 0x80 && !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.'):
			return -1
		}
		return r
	}, n)
	n = strings.Trim(n, "-.")
	a, err := idna.Lookup.ToASCII(n)
	if err != nil || a == "" || len(a) > 253 {
		return nil
	}
	return []string{a}
}

// Import übernimmt ein eigenes Zertifikat (PEM, ggf. mit Kette) und Schlüssel.
func (m *Manager) Import(certPEM, keyPEM []byte) error {
	c, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("Zertifikat und Schlüssel passen nicht zusammen: %w", err)
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return err
	}
	if err := checkServerCert(leaf); err != nil {
		return err
	}
	// Die eigene CA bleibt erhalten, damit man jederzeit zurückwechseln kann.
	if err := m.writeFiles(map[string][]byte{serverCertFile: certPEM, serverKeyFile: keyPEM}); err != nil {
		return err
	}
	return m.load()
}

// checkServerCert prüft, ob sich ein Zertifikat als RADIUS-/Webserverzertifikat eignet.
func checkServerCert(leaf *x509.Certificate) error {
	now := time.Now()
	switch {
	case leaf.IsCA:
		return errors.New("Das ist ein CA-Zertifikat – bitte das Serverzertifikat auswählen")
	case now.After(leaf.NotAfter):
		return fmt.Errorf("Das Zertifikat ist am %s abgelaufen", leaf.NotAfter.Local().Format("02.01.2006"))
	case leaf.NotBefore.After(now.Add(24 * time.Hour)):
		return fmt.Errorf("Das Zertifikat ist erst ab %s gültig", leaf.NotBefore.Local().Format("02.01.2006"))
	}
	if len(leaf.ExtKeyUsage) > 0 {
		ok := false
		for _, u := range leaf.ExtKeyUsage {
			if u == x509.ExtKeyUsageServerAuth || u == x509.ExtKeyUsageAny {
				ok = true
			}
		}
		if !ok {
			return errors.New("Dem Zertifikat fehlt der Verwendungszweck „Serverauthentifizierung“ – WLAN-Geräte lehnen es ab")
		}
	}
	return nil
}

// Files liefert alle Zertifikatsdateien (für die Sicherung).
func (m *Manager) Files() map[string][]byte {
	out := map[string][]byte{}
	entries, _ := os.ReadDir(m.dir)
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		if b, err := os.ReadFile(m.path(e.Name())); err == nil {
			out[e.Name()] = b
		}
	}
	return out
}

// Restore ersetzt die Zertifikatsdateien (aus einer Sicherung).
func (m *Manager) Restore(files map[string][]byte) error {
	clean := map[string][]byte{}
	for name, b := range files {
		if name != filepath.Base(name) || strings.HasPrefix(name, ".") {
			return errors.New("ungültiger Dateiname in der Sicherung")
		}
		clean[name] = b
	}
	if _, err := tls.X509KeyPair(clean[serverCertFile], clean[serverKeyFile]); err != nil {
		return errors.New("Die Sicherung enthält kein gültiges Serverzertifikat")
	}
	if err := m.writeFiles(clean); err != nil {
		return err
	}
	return m.load()
}

// CACertPEM liefert das CA-Zertifikat zum Installieren auf Endgeräten.
func (m *Manager) CACertPEM() ([]byte, error) {
	return os.ReadFile(m.path(caCertFile))
}

func (m *Manager) Info() Info {
	c := m.Certificate()
	leaf := c.Leaf
	sum := sha256.Sum256(leaf.Raw)
	fp := strings.ToUpper(hex.EncodeToString(sum[:]))
	var parts []string
	for i := 0; i < len(fp); i += 2 {
		parts = append(parts, fp[i:i+2])
	}
	own := m.issuedByOwnCA(leaf)
	subject := leaf.Subject.CommonName
	if len(leaf.DNSNames) > 0 {
		subject = leaf.DNSNames[0]
	}
	return Info{
		Subject:     subject,
		Issuer:      leaf.Issuer.CommonName,
		NotBefore:   leaf.NotBefore,
		NotAfter:    leaf.NotAfter,
		Fingerprint: strings.Join(parts, ":"),
		SelfSigned:  own,
		HasCA:       own,
		Public:      !own && isPubliclyTrusted(c),
	}
}

func pemBlock(t string, b []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: t, Bytes: b})
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}

// UseOwnCA stellt ein neues Serverzertifikat der vorhandenen eigenen CA aus
// (Geräte, die die CA installiert haben, vertrauen ihm sofort). Ohne CA wird alles neu erzeugt.
func (m *Manager) UseOwnCA(name string) error {
	if name == "" {
		name = "EasyRadius"
	}
	caPEM, err1 := os.ReadFile(m.path(caCertFile))
	keyPEM, err2 := os.ReadFile(m.path(caKeyFile))
	if err1 != nil || err2 != nil {
		return m.Generate(name)
	}
	cb, _ := pem.Decode(caPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return m.Generate(name)
	}
	caCert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return m.Generate(name)
	}
	k, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		return m.Generate(name)
	}
	srvKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	now := time.Now().Add(-time.Hour)
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: name, Organization: []string{"EasyRadius"}},
		DNSNames:     dnsNames(name),
		NotBefore:    now,
		NotAfter:     now.AddDate(0, 0, 820),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &srvKey.PublicKey, k)
	if err != nil {
		return err
	}
	kd, _ := x509.MarshalPKCS8PrivateKey(srvKey)
	if err := m.writeFiles(map[string][]byte{
		serverCertFile: append(pemBlock("CERTIFICATE", der), caPEM...),
		serverKeyFile:  pemBlock("PRIVATE KEY", kd),
	}); err != nil {
		return err
	}
	return m.load()
}
