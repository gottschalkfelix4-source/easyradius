// Package letsencrypt holt und erneuert ein öffentlich vertrauenswürdiges
// Serverzertifikat per ACME. Damit brauchen Android-Geräte keine eigene CA mehr:
// „Systemzertifikate verwenden“ + Domain genügt.
//
// Drei Wege zur Bestätigung der Domain:
//   - dns:     über die API des DNS-Anbieters (49 Anbieter, siehe providers_gen.go)
//   - acmedns: einmaliger CNAME-Eintrag bei einem beliebigen Anbieter (acme-dns)
//   - http:    Port 80 ist aus dem Internet erreichbar
package letsencrypt

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/acmedns"
	"github.com/go-acme/lego/v4/registration"
)

// Methoden
const (
	MethodDNS     = "dns"
	MethodACMEDNS = "acmedns"
	MethodHTTP    = "http"

	DefaultACMEDNS = "https://auth.acme-dns.io"
)

// Field ist eine Zugangsangabe eines DNS-Anbieters (Umgebungsvariable von lego).
type Field struct {
	Env  string `json:"env"`
	Help string `json:"help"`
}

// Provider beschreibt einen DNS-Anbieter.
type Provider struct {
	Code   string  `json:"code"`
	Name   string  `json:"name"`
	URL    string  `json:"url"`
	Fields []Field `json:"fields"`
	new    func() (challenge.Provider, error)
}

// Providers liefert die unterstützten DNS-Anbieter alphabetisch.
func Providers() []Provider {
	out := append([]Provider(nil), providers...)
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func providerByCode(code string) (*Provider, bool) {
	for i := range providers {
		if providers[i].Code == code {
			return &providers[i], true
		}
	}
	return nil, false
}

// Config beschreibt einen Zertifikatsauftrag.
type Config struct {
	Domain      string
	Email       string
	Method      string
	Provider    string            // Code des DNS-Anbieters (Methode dns)
	Credentials map[string]string // Umgebungsvariablen des Anbieters
	ACMEDNSURL  string            // Methode acmedns
	DataDir     string            // für die acme-dns-Kontodaten
	Staging     bool
	AccountKey  string // PEM; leer = neu erzeugen
}

// Result enthält Zertifikatskette und Schlüssel im PEM-Format.
type Result struct {
	CertPEM, KeyPEM []byte
	AccountKey      string
}

// CNAMERequired: Bei der CNAME-Methode muss einmalig ein DNS-Eintrag angelegt werden.
type CNAMERequired struct {
	Name, Target string
}

func (e *CNAMERequired) Error() string {
	return fmt.Sprintf("Bitte beim DNS-Anbieter einmalig einen CNAME-Eintrag anlegen: %s → %s", e.Name, e.Target)
}

type user struct {
	email string
	reg   *registration.Resource
	key   crypto.PrivateKey
}

func (u *user) GetEmail() string                        { return u.email }
func (u *user) GetRegistration() *registration.Resource { return u.reg }
func (u *user) GetPrivateKey() crypto.PrivateKey        { return u.key }

// lego liest Zugangsdaten aus Umgebungsvariablen – während des Aufbaus exklusiv setzen.
var envMu sync.Mutex

func withEnv(vars map[string]string, fn func() (challenge.Provider, error)) (challenge.Provider, error) {
	envMu.Lock()
	defer envMu.Unlock()
	var set []string
	for k, v := range vars {
		if v = strings.TrimSpace(v); v != "" {
			os.Setenv(k, v)
			set = append(set, k)
		}
	}
	defer func() {
		for _, k := range set {
			os.Unsetenv(k)
		}
	}()
	return fn()
}

func (c *Config) provider() (challenge.Provider, error) {
	switch c.Method {
	case MethodDNS:
		p, ok := providerByCode(c.Provider)
		if !ok {
			return nil, errors.New("Bitte einen DNS-Anbieter auswählen")
		}
		prov, err := withEnv(c.Credentials, p.new)
		if err != nil {
			return nil, fmt.Errorf("%s: Zugangsdaten unvollständig oder ungültig (%v)", p.Name, err)
		}
		return prov, nil
	case MethodACMEDNS:
		base := c.ACMEDNSURL
		if base == "" {
			base = DefaultACMEDNS
		}
		return withEnv(map[string]string{
			"ACME_DNS_API_BASE":     base,
			"ACME_DNS_STORAGE_PATH": c.DataDir + `\acme-dns-accounts.json`,
		}, func() (challenge.Provider, error) { return acmedns.NewDNSProvider() })
	}
	return nil, errors.New("unbekannte Methode")
}

// Obtain beantragt ein Zertifikat für c.Domain.
func Obtain(c Config) (*Result, error) {
	c.Domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(c.Domain), "."))
	if c.Domain == "" || !strings.Contains(c.Domain, ".") {
		return nil, errors.New("Bitte eine vollständige Domain angeben, z. B. radius.example.de")
	}
	key, keyPEM, err := accountKey(c.AccountKey)
	if err != nil {
		return nil, err
	}
	u := &user{email: c.Email, key: key}
	cfg := lego.NewConfig(u)
	cfg.CADirURL = lego.LEDirectoryProduction
	if c.Staging {
		cfg.CADirURL = lego.LEDirectoryStaging
	}
	cfg.UserAgent = "EasyRadius"
	cfg.Certificate.KeyType = certcrypto.RSA2048 // maximal kompatibel mit WLAN-Clients
	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("Let's Encrypt nicht erreichbar: %w", err)
	}
	if reg, err := client.Registration.ResolveAccountByKey(); err == nil {
		u.reg = reg
	} else if reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true}); err == nil {
		u.reg = reg
	} else {
		return nil, fmt.Errorf("Let's-Encrypt-Konto: %w", err)
	}

	if c.Method == MethodHTTP {
		if err := client.Challenge.SetHTTP01Provider(http01.NewProviderServer("", "80")); err != nil {
			return nil, err
		}
	} else {
		prov, err := c.provider()
		if err != nil {
			return nil, err
		}
		// Öffentliche DNS-Server fragen – interne DNS-Server kennen die Zone oft anders.
		if err := client.Challenge.SetDNS01Provider(prov,
			dns01.AddRecursiveNameservers([]string{"1.1.1.1:53", "8.8.8.8:53", "9.9.9.9:53"}),
			dns01.AddDNSTimeout(10*time.Second)); err != nil {
			return nil, err
		}
	}

	res, err := client.Certificate.Obtain(certificate.ObtainRequest{Domains: []string{c.Domain}, Bundle: true})
	if err != nil {
		var cn acmedns.ErrCNAMERequired
		if errors.As(err, &cn) {
			return &Result{AccountKey: keyPEM}, &CNAMERequired{Name: strings.TrimSuffix(cn.FQDN, "."), Target: strings.TrimSuffix(cn.Target, ".")}
		}
		var cnp *acmedns.ErrCNAMERequired
		if errors.As(err, &cnp) {
			return &Result{AccountKey: keyPEM}, &CNAMERequired{Name: strings.TrimSuffix(cnp.FQDN, "."), Target: strings.TrimSuffix(cnp.Target, ".")}
		}
		return &Result{AccountKey: keyPEM}, friendly(err, c.Method)
	}
	return &Result{CertPEM: res.Certificate, KeyPEM: res.PrivateKey, AccountKey: keyPEM}, nil
}

// friendly übersetzt typische Fehler.
func friendly(err error, method string) error {
	msg := err.Error()
	switch {
	case method == MethodHTTP && strings.Contains(msg, "bind"):
		return errors.New("Port 80 ist auf diesem Rechner belegt (z. B. durch IIS) – HTTP-Prüfung nicht möglich")
	case method == MethodHTTP && (strings.Contains(msg, "Timeout") || strings.Contains(msg, "connection") || strings.Contains(msg, "unauthorized")):
		return fmt.Errorf("Let's Encrypt konnte den Server über Port 80 nicht erreichen. Zeigt die Domain auf deine öffentliche IP und ist Port 80 an diesen Rechner weitergeleitet? (%s)", short(msg))
	case strings.Contains(msg, "rateLimited"):
		return errors.New("Zu viele Versuche bei Let's Encrypt – bitte später erneut probieren (oder das Testsystem nutzen)")
	case strings.Contains(msg, "propagation") || strings.Contains(msg, "time limit exceeded"):
		return fmt.Errorf("Der DNS-Eintrag wurde nicht rechtzeitig sichtbar. (%s)", short(msg))
	}
	return fmt.Errorf("Zertifikat konnte nicht ausgestellt werden: %s", short(msg))
}

func short(s string) string {
	if len(s) > 400 {
		return s[:400] + " …"
	}
	return s
}

func accountKey(p string) (crypto.PrivateKey, string, error) {
	if p != "" {
		if b, _ := pem.Decode([]byte(p)); b != nil {
			if k, err := x509.ParseECPrivateKey(b.Bytes); err == nil {
				return k, p, nil
			}
		}
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", err
	}
	d, _ := x509.MarshalECPrivateKey(k)
	return k, string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: d})), nil
}
