package web

import (
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/gottschalkfelix4-source/easyradius/internal/attrs"
	"github.com/gottschalkfelix4-source/easyradius/internal/directory"
	"github.com/gottschalkfelix4-source/easyradius/internal/events"
	"github.com/gottschalkfelix4-source/easyradius/internal/letsencrypt"
	"github.com/gottschalkfelix4-source/easyradius/internal/store"
)

// ---- Gruppen ----

type groupOut struct {
	store.Group
	Members int  `json:"members"`
	Expired bool `json:"expired"`
}

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	count := map[string]int{}
	for _, u := range s.Store.Users() {
		count[u.GroupID]++
	}
	out := []groupOut{}
	for _, g := range s.Store.Groups() {
		out = append(out, groupOut{Group: g, Members: count[g.ID], Expired: g.Expired(time.Now())})
	}
	writeJSON(w, 200, out)
}

func (s *Server) saveGroup(w http.ResponseWriter, r *http.Request) {
	var g store.Group
	if readJSON(r, &g) != nil {
		fail(w, 400, "Ungültige Eingabe")
		return
	}
	g.ID = r.PathValue("id")
	if g.VLAN != "" {
		if n, err := strconv.Atoi(g.VLAN); err != nil || n < 1 || n > 4094 {
			fail(w, 400, "VLAN muss eine Zahl zwischen 1 und 4094 sein")
			return
		}
	}
	if g.ExpiresAt != "" {
		if _, err := time.Parse("2006-01-02", g.ExpiresAt); err != nil {
			fail(w, 400, "Ablaufdatum ungültig")
			return
		}
	}
	saved, err := s.Store.SaveGroup(g)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, saved)
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteGroup(r.PathValue("id")); err != nil {
		fail(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---- Active Directory ----

func (s *Server) getAD(w http.ResponseWriter, r *http.Request) {
	c := s.Store.Settings().AD
	dom, joined := directory.JoinedDomain()
	status := ""
	if c.Mode != "off" {
		if _, err := s.Radius.Directory(); err != nil {
			status = err.Error()
		}
	}
	writeJSON(w, 200, map[string]any{
		"mode": c.Mode, "requireGroup": c.RequireGroup, "ldapUrl": c.LDAPURL, "startTls": c.StartTLS,
		"skipVerify": c.SkipVerify, "baseDn": c.BaseDN, "bindUser": c.BindUser, "hasBindPassword": c.BindPassword != "",
		"userAttribute": c.UserAttribute, "joined": joined, "domain": dom, "status": status,
	})
}

func (s *Server) putAD(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode, LDAPURL, BaseDN, BindUser, BindPassword, UserAttribute string
		RequireGroup, StartTLS, SkipVerify                           bool
	}
	if readJSON(r, &in) != nil {
		fail(w, 400, "Ungültige Eingabe")
		return
	}
	switch in.Mode {
	case "off", "domain", "ldap":
	default:
		fail(w, 400, "Unbekannter Modus")
		return
	}
	if in.Mode == "domain" {
		if _, ok := directory.JoinedDomain(); !ok {
			fail(w, 400, "Dieser Rechner ist kein Mitglied einer Domäne. Erst der Domäne beitreten oder LDAP wählen.")
			return
		}
	}
	if in.Mode == "ldap" {
		in.LDAPURL = strings.TrimSpace(in.LDAPURL)
		if !strings.HasPrefix(in.LDAPURL, "ldap://") && !strings.HasPrefix(in.LDAPURL, "ldaps://") {
			fail(w, 400, "LDAP-Adresse muss mit ldap:// oder ldaps:// beginnen")
			return
		}
		if strings.TrimSpace(in.BaseDN) == "" || strings.TrimSpace(in.BindUser) == "" {
			fail(w, 400, "Basis-DN und Dienstkonto werden für LDAP benötigt")
			return
		}
	}
	s.Store.UpdateSettings(func(c *store.Settings) {
		a := &c.AD
		a.Mode, a.RequireGroup = in.Mode, in.RequireGroup
		a.LDAPURL, a.StartTLS, a.SkipVerify = in.LDAPURL, in.StartTLS, in.SkipVerify
		a.BaseDN, a.BindUser = strings.TrimSpace(in.BaseDN), strings.TrimSpace(in.BindUser)
		if in.BindPassword != "" {
			a.BindPassword = in.BindPassword
		}
		a.UserAttribute = strings.TrimSpace(in.UserAttribute)
		if a.UserAttribute == "" {
			a.UserAttribute = "sAMAccountName"
		}
	})
	s.Log.Add(events.Event{Kind: events.Info, Message: "AD-Anbindung geändert (Modus: " + in.Mode + ")"})
	s.getAD(w, r)
}

// testLogin prüft lokale und AD-Konten wie bei einer echten Anmeldung.
func (s *Server) testLogin(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Password string }
	if readJSON(r, &in) != nil || in.Username == "" {
		fail(w, 400, "Benutzername fehlt")
		return
	}
	writeJSON(w, 200, s.Radius.TestLogin(strings.TrimSpace(in.Username), in.Password))
}

// ---- Let's Encrypt ----

func (s *Server) getACME(w http.ResponseWriter, r *http.Request) {
	c := s.Store.Settings().ACME
	cfg := letsencrypt.ConfigFrom(c, "")
	saved := []string{} // Namen gespeicherter Zugangsdaten (Werte bleiben geheim)
	for k, v := range cfg.Credentials {
		if v != "" {
			saved = append(saved, k)
		}
	}
	writeJSON(w, 200, map[string]any{
		"enabled": c.Enabled, "domain": c.Domain, "email": c.Email, "staging": c.Staging,
		"method": cfg.Method, "provider": cfg.Provider, "savedCredentials": saved, "acmeDnsUrl": c.ACMEDNSURL,
		"defaultAcmeDnsUrl": letsencrypt.DefaultACMEDNS, "cnameName": c.CNAMEName, "cnameTarget": c.CNAMETarget,
		"lastSuccess": c.LastSuccess, "lastError": c.LastError, "lastAttempt": c.LastAttempt, "running": s.ACME.Running(),
	})
}

func (s *Server) acmeProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, letsencrypt.Providers())
}

func (s *Server) putACME(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled, Staging, AcceptTOS                 bool
		Domain, Email, Method, Provider, ACMEDNSURL string
		Credentials                                 map[string]string
	}
	if readJSON(r, &in) != nil {
		fail(w, 400, "Ungültige Eingabe")
		return
	}
	in.Domain = strings.ToLower(strings.TrimSpace(in.Domain))
	in.Email = strings.TrimSpace(in.Email)
	old := s.Store.Settings().ACME
	oldCfg := letsencrypt.ConfigFrom(old, "")

	// Leere Felder = gespeicherten Wert behalten (nur beim selben Anbieter).
	creds := map[string]string{}
	if in.Provider == oldCfg.Provider {
		for k, v := range oldCfg.Credentials {
			creds[k] = v
		}
	}
	for k, v := range in.Credentials {
		if v = strings.TrimSpace(v); v != "" {
			creds[k] = v
		}
	}
	if in.Enabled {
		if !strings.Contains(in.Domain, ".") {
			fail(w, 400, "Bitte eine vollständige Domain angeben, z. B. radius.example.de")
			return
		}
		if in.Email != "" {
			if _, err := mail.ParseAddress(in.Email); err != nil {
				fail(w, 400, "E-Mail-Adresse ungültig")
				return
			}
		}
		switch in.Method {
		case letsencrypt.MethodDNS:
			if in.Provider == "" {
				fail(w, 400, "Bitte deinen DNS-Anbieter auswählen")
				return
			}
			if len(creds) == 0 {
				fail(w, 400, "Bitte die Zugangsdaten deines DNS-Anbieters eintragen")
				return
			}
		case letsencrypt.MethodACMEDNS, letsencrypt.MethodHTTP:
		default:
			fail(w, 400, "Bitte einen Weg zur Bestätigung wählen")
			return
		}
		if !in.AcceptTOS {
			fail(w, 400, "Bitte die Nutzungsbedingungen von Let's Encrypt akzeptieren")
			return
		}
	}
	s.Store.UpdateSettings(func(c *store.Settings) {
		a := &c.ACME
		if a.Domain != in.Domain || a.Method != in.Method {
			a.CNAMEName, a.CNAMETarget = "", ""
		}
		a.Enabled, a.Domain, a.Email, a.Staging = in.Enabled, in.Domain, in.Email, in.Staging
		a.Method, a.Provider, a.Credentials = in.Method, in.Provider, creds
		a.ACMEDNSURL = strings.TrimSpace(in.ACMEDNSURL)
		a.APIToken = ""
		if old.Staging != in.Staging {
			a.AccountKey = "" // anderes Let's-Encrypt-System, neues Konto
		}
		a.LastAttempt, a.LastError = time.Time{}, ""
	})
	if in.Enabled {
		s.ACME.Start()
	}
	s.getACME(w, r)
}

// attributeDictionary liefert Wörterbuch und Vorlagen für den Attribut-Editor.
func (s *Server) attributeDictionary(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"dictionary": attrs.Dictionary, "presets": attrs.Presets, "vendors": attrs.Vendors()})
}

func (s *Server) runACME(w http.ResponseWriter, r *http.Request) {
	if !s.Store.Settings().ACME.Enabled {
		fail(w, 400, "Let's Encrypt ist nicht aktiviert")
		return
	}
	s.ACME.Start()
	s.getACME(w, r)
}

func (s *Server) certOwnCA(w http.ResponseWriter, r *http.Request) {
	s.Store.UpdateSettings(func(c *store.Settings) { c.ACME.Enabled = false })
	if err := s.Certs.UseOwnCA(s.Store.Settings().ServerName); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.Log.Add(events.Event{Kind: events.Info, Message: "Zurück zum Zertifikat der eigenen CA"})
	writeJSON(w, 200, s.Certs.Info())
}
