package certs

// Benutzerzertifikate für EAP-TLS: Ausstellen (PKCS#12), Auflisten und Widerrufen.

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

const (
	clientsFile = "clients.json"
	revokedFile = "revoked.json"
)

// clientMu schützt clients.json und revoked.json.
var clientMu sync.Mutex

// ClientCertInfo beschreibt ein ausgestelltes Benutzerzertifikat.
type ClientCertInfo struct {
	Serial    string    `json:"serial"`
	User      string    `json:"user"`
	Created   time.Time `json:"created"`
	NotAfter  time.Time `json:"notAfter"`
	Revoked   bool      `json:"revoked"`
	RevokedAt time.Time `json:"revokedAt,omitempty"`
}

// revokedEntry ist ein Eintrag der Sperrliste.
type revokedEntry struct {
	Serial    string    `json:"serial"`
	RevokedAt time.Time `json:"revokedAt"`
}

// ErrNoCA meldet, dass keine eigene Zertifizierungsstelle vorhanden ist.
var ErrNoCA = errors.New("Keine eigene Zertifizierungsstelle vorhanden")

// SerialOf liefert die Seriennummer eines Zertifikats in der hier verwendeten Schreibweise
// (Hexadezimal, Großbuchstaben, ohne führende Nullen).
func SerialOf(c *x509.Certificate) string {
	return strings.ToUpper(c.SerialNumber.Text(16))
}

// normSerial bringt eine eingegebene Seriennummer in die Form von SerialOf
// (Doppelpunkte, Leerzeichen und führende Nullen werden entfernt).
func normSerial(s string) (string, bool) {
	s = strings.NewReplacer(":", "", " ", "", "-", "").Replace(strings.TrimSpace(s))
	n, ok := new(big.Int).SetString(s, 16)
	if !ok || n.Sign() <= 0 {
		return "", false
	}
	return strings.ToUpper(n.Text(16)), true
}

// loadCAForClients liest CA-Zertifikat und -Schlüssel aus dem Zertifikatsordner.
func (m *Manager) loadCAForClients() (*x509.Certificate, crypto.Signer, error) {
	caPEM, err := os.ReadFile(filepath.Join(m.dir, caCertFile))
	if err != nil {
		return nil, nil, ErrNoCA
	}
	keyPEM, err := os.ReadFile(filepath.Join(m.dir, caKeyFile))
	if err != nil {
		return nil, nil, ErrNoCA
	}
	cb, _ := pem.Decode(caPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, nil, ErrNoCA
	}
	ca, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("CA-Zertifikat ungültig: %w", err)
	}
	k, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("CA-Schlüssel ungültig: %w", err)
	}
	signer, ok := k.(crypto.Signer)
	if !ok {
		return nil, nil, errors.New("CA-Schlüssel kann nicht signieren")
	}
	return ca, signer, nil
}

// ClientCAPool liefert die eigene CA als Vertrauensanker für Benutzerzertifikate (EAP-TLS).
func (m *Manager) ClientCAPool() (*x509.CertPool, error) {
	b, err := os.ReadFile(filepath.Join(m.dir, caCertFile))
	if err != nil {
		return nil, ErrNoCA
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, ErrNoCA
	}
	ca, err := x509.ParseCertificate(blk.Bytes)
	if err != nil || !ca.IsCA {
		return nil, ErrNoCA
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return pool, nil
}

// IssueClientCert stellt ein Benutzerzertifikat (CN = Benutzername) der eigenen CA aus und
// liefert es samt Schlüssel und CA-Zertifikat als passwortgeschützte PKCS#12-Datei.
func (m *Manager) IssueClientCert(user string, days int, password string) (p12 []byte, serialHex string, err error) {
	user = strings.TrimSpace(user)
	if user == "" || len(user) > 64 || strings.IndexFunc(user, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return nil, "", errors.New("Ungültiger Benutzername für das Zertifikat")
	}
	if days <= 0 {
		days = 365
	}
	if days > 3650 {
		return nil, "", errors.New("Laufzeit höchstens 10 Jahre")
	}
	ca, caKey, err := m.loadCAForClients()
	if err != nil {
		return nil, "", err
	}
	// RSA 2048: wird von allen gängigen Geräten (auch älteren Android-Versionen) importiert.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, "", err
	}
	sn, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, "", err
	}
	sn.Add(sn, big.NewInt(1)) // niemals 0
	now := time.Now()
	notAfter := now.AddDate(0, 0, days)
	if notAfter.After(ca.NotAfter) {
		notAfter = ca.NotAfter
	}
	tmpl := &x509.Certificate{
		SerialNumber: sn,
		Subject:      pkix.Name{CommonName: user, Organization: []string{"EasyRadius"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, "", err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, "", err
	}
	p12, err = pkcs12.Modern.Encode(key, cert, []*x509.Certificate{ca}, password)
	if err != nil {
		return nil, "", fmt.Errorf("PKCS#12 erzeugen: %w", err)
	}
	serialHex = SerialOf(cert)

	clientMu.Lock()
	defer clientMu.Unlock()
	list, err := m.readClients()
	if err != nil {
		return nil, "", err
	}
	list = append(list, ClientCertInfo{Serial: serialHex, User: user, Created: now, NotAfter: notAfter})
	if err := m.saveClientJSON(clientsFile, list); err != nil {
		return nil, "", err
	}
	return p12, serialHex, nil
}

// RevokeClientCert sperrt ein Benutzerzertifikat dauerhaft.
func (m *Manager) RevokeClientCert(serialHex string) error {
	sn, ok := normSerial(serialHex)
	if !ok {
		return errors.New("Ungültige Seriennummer")
	}
	clientMu.Lock()
	defer clientMu.Unlock()
	now := time.Now()
	rev, err := m.readRevoked()
	if err != nil {
		return err
	}
	found := false
	for _, r := range rev {
		if r.Serial == sn {
			found = true
		}
	}
	if !found {
		rev = append(rev, revokedEntry{Serial: sn, RevokedAt: now})
		if err := m.saveClientJSON(revokedFile, rev); err != nil {
			return err
		}
	}
	list, err := m.readClients()
	if err != nil {
		return err
	}
	changed := false
	for i := range list {
		if list[i].Serial == sn && !list[i].Revoked {
			list[i].Revoked, list[i].RevokedAt = true, now
			changed = true
		}
	}
	if changed {
		return m.saveClientJSON(clientsFile, list)
	}
	return nil
}

// IsRevoked meldet, ob ein Zertifikat auf der Sperrliste steht.
func (m *Manager) IsRevoked(serialHex string) bool {
	sn, ok := normSerial(serialHex)
	if !ok {
		return true // unbrauchbare Seriennummer: sicherheitshalber als gesperrt behandeln
	}
	clientMu.Lock()
	defer clientMu.Unlock()
	list, err := m.readRevoked()
	if err != nil {
		return true // Sperrliste unlesbar: lieber ablehnen als ein gesperrtes Zertifikat zulassen
	}
	for _, r := range list {
		if r.Serial == sn {
			return true
		}
	}
	return false
}

// ClientCerts listet alle ausgestellten Benutzerzertifikate (neueste zuerst).
func (m *Manager) ClientCerts() []ClientCertInfo {
	clientMu.Lock()
	defer clientMu.Unlock()
	list, _ := m.readClients()
	rev := map[string]time.Time{}
	revList, _ := m.readRevoked()
	for _, r := range revList {
		rev[r.Serial] = r.RevokedAt
	}
	for i := range list {
		if t, ok := rev[list[i].Serial]; ok && !list[i].Revoked {
			list[i].Revoked, list[i].RevokedAt = true, t
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Created.After(list[j].Created) })
	return list
}

func (m *Manager) readClients() ([]ClientCertInfo, error) {
	var list []ClientCertInfo
	b, err := os.ReadFile(filepath.Join(m.dir, clientsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("Zertifikatsliste beschädigt: %w", err)
	}
	return list, nil
}

func (m *Manager) readRevoked() ([]revokedEntry, error) {
	var list []revokedEntry
	b, err := os.ReadFile(filepath.Join(m.dir, revokedFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("Sperrliste beschädigt: %w", err)
	}
	return list, nil
}

// saveClientJSON schreibt atomar (erst temporäre Datei, dann umbenennen).
func (m *Manager) saveClientJSON(name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(m.dir, name)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
