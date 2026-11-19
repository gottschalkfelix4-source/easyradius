package letsencrypt

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/gottschalkfelix4-source/easyradius/internal/certs"
	"github.com/gottschalkfelix4-source/easyradius/internal/events"
	"github.com/gottschalkfelix4-source/easyradius/internal/store"
)

// Service erneuert das Zertifikat automatisch (stündlicher Check, Erneuerung 30 Tage vor Ablauf).
type Service struct {
	Store   *store.Store
	Certs   *certs.Manager
	Log     *events.Log
	DataDir string

	mu      sync.Mutex
	running bool
}

// Running meldet, ob gerade ein Zertifikat beantragt wird.
func (s *Service) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Start beantragt sofort im Hintergrund ein Zertifikat.
func (s *Service) Start() bool {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return false
	}
	s.running = true
	s.mu.Unlock()
	go func() {
		defer func() { s.mu.Lock(); s.running = false; s.mu.Unlock() }()
		s.renew()
	}()
	return true
}

func (s *Service) needsRenewal() bool {
	cfg := s.Store.Settings().ACME
	if !cfg.Enabled {
		return false
	}
	info := s.Certs.Info()
	if !strings.EqualFold(info.Subject, cfg.Domain) || (!info.Public && !cfg.Staging) {
		return true
	}
	return time.Until(info.NotAfter) < 30*24*time.Hour
}

// Loop prüft regelmäßig; nach einem Fehler wird frühestens nach 6 Stunden erneut versucht.
// Endet, wenn ctx beendet wird.
func (s *Service) Loop(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		cfg := s.Store.Settings().ACME
		if s.needsRenewal() && time.Since(cfg.LastAttempt) > 6*time.Hour {
			s.Start()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// sameOrder prüft, ob die Einstellungen noch zum laufenden Auftrag passen
// (sonst wurde während des Beantragens umgestellt und das Ergebnis verworfen).
func sameOrder(a, b store.ACMESettings) bool {
	return a.Enabled == b.Enabled && strings.EqualFold(a.Domain, b.Domain) && a.Method == b.Method &&
		a.Provider == b.Provider && a.Staging == b.Staging
}

// ConfigFrom baut den Auftrag aus den Einstellungen (inkl. Übernahme alter Cloudflare-Einstellungen).
func ConfigFrom(a store.ACMESettings, dataDir string) Config {
	method, prov, creds := a.Method, a.Provider, a.Credentials
	if method == "" {
		method = MethodDNS
	}
	if len(creds) == 0 && a.APIToken != "" && prov == "cloudflare" {
		creds = map[string]string{"CF_DNS_API_TOKEN": a.APIToken}
	}
	return Config{Domain: a.Domain, Email: a.Email, Method: method, Provider: prov, Credentials: creds,
		ACMEDNSURL: a.ACMEDNSURL, DataDir: dataDir, Staging: a.Staging, AccountKey: a.AccountKey}
}

func (s *Service) renew() {
	a := s.Store.Settings().ACME
	s.Store.UpdateSettings(func(c *store.Settings) { c.ACME.LastAttempt = time.Now() })
	s.Log.Add(events.Event{Kind: events.Info, Message: "Let's Encrypt: Zertifikat für " + a.Domain + " wird beantragt …"})
	res, err := Obtain(ConfigFrom(a, s.DataDir))
	if now := s.Store.Settings().ACME; !sameOrder(a, now) {
		s.Log.Add(events.Event{Kind: events.Info, Message: "Let's Encrypt: Einstellungen wurden während des Beantragens geändert – Ergebnis verworfen"})
		return
	}
	if err == nil {
		err = s.Certs.Import(res.CertPEM, res.KeyPEM)
	}
	var cname *CNAMERequired
	isCNAME := errors.As(err, &cname)
	s.Store.UpdateSettings(func(c *store.Settings) {
		if res != nil && res.AccountKey != "" {
			c.ACME.AccountKey = res.AccountKey
		}
		c.ACME.CNAMEName, c.ACME.CNAMETarget = "", ""
		switch {
		case isCNAME:
			c.ACME.CNAMEName, c.ACME.CNAMETarget = cname.Name, cname.Target
			c.ACME.LastError = ""
			c.ACME.LastAttempt = time.Time{} // sofort erneut versuchen dürfen
		case err != nil:
			c.ACME.LastError = err.Error()
		default:
			c.ACME.LastError = ""
			c.ACME.LastSuccess = time.Now()
		}
	})
	switch {
	case isCNAME:
		s.Log.Add(events.Event{Kind: events.Warning, Message: "Let's Encrypt: " + err.Error()})
	case err != nil:
		s.Log.Add(events.Event{Kind: events.Warning, Message: "Let's Encrypt fehlgeschlagen: " + err.Error()})
	default:
		s.Log.Add(events.Event{Kind: events.Info, Message: "Let's-Encrypt-Zertifikat für " + a.Domain + " ist aktiv"})
	}
}
