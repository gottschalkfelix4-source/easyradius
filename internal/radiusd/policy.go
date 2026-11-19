package radiusd

import (
	"encoding/hex"
	"errors"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gottschalkfelix4-source/easyradius/internal/attrs"
	"github.com/gottschalkfelix4-source/easyradius/internal/auth"
	"github.com/gottschalkfelix4-source/easyradius/internal/directory"
	"github.com/gottschalkfelix4-source/easyradius/internal/store"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
)

// finish prüft die Check-Attribute und übernimmt die Reply-Attribute.
func finish(dec decision, list []attrs.Attr, rc reqCtx) (decision, string) {
	check, reply := attrs.Split(list)
	if why := checkAttrs(check, rc); why != "" {
		return decision{}, why
	}
	dec.Attrs = reply
	return dec, ""
}

// checkAttrs gleicht alle Check-Attribute mit der Anfrage ab (alle müssen passen).
func checkAttrs(check []attrs.Attr, rc reqCtx) string {
	if rc.req == nil {
		return "" // Selbsttest ohne echte Anfrage
	}
	for _, a := range check {
		vals := reqValues(rc.req, a.VendorID, a.Code)
		if !a.Matches(vals) {
			return "Prüfattribut „" + a.Name + "“ passt nicht (erwartet " + a.Value + ", erhalten " + a.Display(vals) + ")"
		}
	}
	return ""
}

// reqValues liefert alle Werte eines (Hersteller-)Attributs aus der Anfrage.
func reqValues(p *radius.Packet, vendor uint32, code uint8) [][]byte {
	var out [][]byte
	for _, a := range p.Attributes {
		if vendor == 0 {
			if a.Type == radius.Type(code) {
				out = append(out, a.Attribute)
			}
			continue
		}
		if a.Type != rfc2865.VendorSpecific_Type {
			continue
		}
		id, v, err := radius.VendorSpecific(a.Attribute)
		if err != nil || id != vendor {
			continue
		}
		for len(v) >= 2 && int(v[1]) >= 2 && int(v[1]) <= len(v) {
			if v[0] == code {
				out = append(out, v[2:v[1]])
			}
			v = v[v[1]:]
		}
	}
	return out
}

// identity ist ein geprüfter Benutzer – lokal oder aus dem AD.
type identity struct {
	Username string
	AD       bool
	Local    *store.User
	ADGroups []string
}

// decision ist das Ergebnis der Gruppenregeln.
type decision struct {
	VLAN  string
	Group string
	Attrs []attrs.Attr
}

// reqCtx beschreibt, wo sich jemand anmeldet.
type reqCtx struct {
	client store.Client
	ssid   string
	req    *radius.Packet // Anfrage des Geräts (für Check-Attribute)
}

var calledRe = regexp.MustCompile(`^(?:[0-9A-Fa-f]{2}[-:.]?){5}[0-9A-Fa-f]{2}[:;](.+)$`)

// ssidFromCalled liest die SSID aus der Called-Station-Id ("AA-BB-CC-DD-EE-FF:Firmen-WLAN").
func ssidFromCalled(called string) string {
	if m := calledRe.FindStringSubmatch(strings.TrimSpace(called)); m != nil {
		return m[1]
	}
	return ""
}

// checkGroup prüft die Regeln einer Gruppe; "" heißt erlaubt.
func checkGroup(g *store.Group, rc reqCtx, now time.Time) string {
	switch {
	case !g.Enabled:
		return "Gruppe „" + g.Name + "“ ist gesperrt"
	case g.Expired(now):
		return "Gruppe „" + g.Name + "“ ist abgelaufen"
	case len(g.AllowedClients) > 0 && !slices.Contains(g.AllowedClients, rc.client.ID):
		return "Gruppe „" + g.Name + "“ darf sich nicht über „" + rc.client.Name + "“ anmelden"
	case len(g.AllowedSSIDs) > 0 && rc.ssid == "":
		return "Gruppe „" + g.Name + "“ ist auf bestimmte WLANs beschränkt, aber der Access Point übermittelt keine SSID"
	case len(g.AllowedSSIDs) > 0 && !slices.ContainsFunc(g.AllowedSSIDs, func(x string) bool { return strings.EqualFold(x, rc.ssid) }):
		return "Gruppe „" + g.Name + "“ darf das WLAN „" + rc.ssid + "“ nicht nutzen"
	}
	return ""
}

// authorize wendet die Gruppenregeln an.
func (s *Server) authorize(id *identity, rc reqCtx) (decision, string) {
	now := time.Now()
	if !id.AD {
		u := id.Local
		g, ok := s.Store.Group(u.GroupID)
		if u.GroupID == "" || !ok {
			return finish(decision{VLAN: u.VLAN}, u.Attributes, rc)
		}
		if why := checkGroup(&g, rc, now); why != "" {
			return decision{}, why
		}
		vlan := u.VLAN
		if vlan == "" {
			vlan = g.VLAN
		}
		return finish(decision{VLAN: vlan, Group: g.Name}, attrs.Merge(g.Attributes, u.Attributes), rc)
	}

	// AD-Benutzer: erste passende, verknüpfte Gruppe (alphabetisch) gilt.
	firstReason := ""
	matched := false
	for _, g := range s.Store.Groups() {
		linked := false
		for _, cfg := range g.ADGroups {
			for _, ag := range id.ADGroups {
				if directory.GroupMatches(ag, cfg) {
					linked = true
				}
			}
		}
		if !linked {
			continue
		}
		matched = true
		if why := checkGroup(&g, rc, now); why != "" {
			if firstReason == "" {
				firstReason = why
			}
			continue
		}
		check, _ := attrs.Split(g.Attributes)
		if why := checkAttrs(check, rc); why != "" {
			if firstReason == "" {
				firstReason = why
			}
			continue
		}
		return finish(decision{VLAN: g.VLAN, Group: g.Name}, g.Attributes, rc)
	}
	if s.Store.Settings().AD.RequireGroup {
		if !matched {
			return decision{}, "AD-Benutzer ist in keiner AD-Gruppe, die mit einer EasyRadius-Gruppe verknüpft ist"
		}
		return decision{}, firstReason
	}
	return decision{}, ""
}

// ---- Anmeldedaten prüfen ----

// localUser liefert den lokalen Benutzer (falls vorhanden) samt Sperrgrund.
func (s *Server) localUser(name string) (u *store.User, exists bool, reason string) {
	lu, ok := s.Store.FindUser(stripRealm(name))
	if !ok {
		return nil, false, "Benutzer unbekannt"
	}
	if !lu.Enabled {
		return nil, true, "Benutzer ist deaktiviert"
	}
	if lu.Expired(time.Now()) {
		return nil, true, "Benutzerkonto ist abgelaufen"
	}
	return &lu, true, ""
}

func dirReason(err error) string {
	var de *directory.Error
	if errors.As(err, &de) {
		return de.Msg
	}
	return "AD: " + err.Error()
}

func (s *Server) verifyPAP(name, pw string) (*identity, string) {
	u, exists, reason := s.localUser(name)
	if exists {
		if u == nil {
			return nil, reason
		}
		h, _ := hex.DecodeString(u.NTHash)
		if !auth.CheckPAP(pw, h) {
			return nil, "Falsches Passwort (oder falsches Shared Secret)"
		}
		return &identity{Username: u.Username, Local: u}, ""
	}
	d := s.directory()
	if d == nil {
		return nil, reason
	}
	au, err := d.AuthPAP(name, pw)
	if err != nil {
		return nil, dirReason(err)
	}
	return &identity{Username: au.Name, AD: true, ADGroups: au.Groups}, ""
}

func (s *Server) verifyMSCHAP(name string, authC, peerC, ntResp []byte) (*identity, *auth.MSCHAPv2Result, string) {
	u, exists, reason := s.localUser(name)
	if exists {
		if u == nil {
			return nil, nil, reason
		}
		h, _ := hex.DecodeString(u.NTHash)
		res, err := auth.VerifyMSCHAPv2(authC, peerC, ntResp, name, h)
		if err != nil {
			return nil, nil, "Falsches Passwort"
		}
		return &identity{Username: u.Username, Local: u}, res, ""
	}
	d := s.directory()
	if d == nil {
		return nil, nil, reason
	}
	au, hashHash, err := d.AuthMSCHAPv2(name, auth.ChallengeHash(peerC, authC, name), ntResp)
	if err != nil {
		return nil, nil, dirReason(err)
	}
	return &identity{Username: au.Name, AD: true, ADGroups: au.Groups},
		auth.MSCHAPv2Success(hashHash, ntResp, peerC, authC, name), ""
}

// ---- AD-Quelle ----

type dirCache struct {
	mu     sync.Mutex
	key    string
	dir    directory.Directory
	domain *directory.Domain // bleibt erhalten (LSA-Verbindung)
}

// directory liefert die konfigurierte AD-Quelle oder nil.
func (s *Server) directory() directory.Directory {
	if s.dirOverride != nil {
		return s.dirOverride
	}
	d, _ := s.Directory()
	return d
}

// Directory liefert die AD-Quelle bzw. den Grund, warum sie nicht verfügbar ist.
func (s *Server) Directory() (directory.Directory, error) {
	cfg := s.Store.Settings().AD
	s.dirs.mu.Lock()
	defer s.dirs.mu.Unlock()
	switch cfg.Mode {
	case "domain":
		if s.dirs.domain == nil {
			d, err := directory.NewDomain()
			if err != nil {
				return nil, err
			}
			s.dirs.domain = d
		}
		return s.dirs.domain, nil
	case "ldap":
		key := strings.Join([]string{cfg.LDAPURL, cfg.BaseDN, cfg.BindUser, cfg.BindPassword, cfg.UserAttribute,
			boolStr(cfg.StartTLS), boolStr(cfg.SkipVerify)}, "\x00")
		if s.dirs.key != key || s.dirs.dir == nil {
			s.dirs.key = key
			s.dirs.dir = &directory.LDAP{URL: cfg.LDAPURL, StartTLS: cfg.StartTLS, SkipVerify: cfg.SkipVerify,
				BaseDN: cfg.BaseDN, BindUser: cfg.BindUser, BindPassword: cfg.BindPassword, UserAttribute: cfg.UserAttribute}
		}
		return s.dirs.dir, nil
	}
	return nil, directory.ErrNotAvailable
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// ---- Selbsttest für die Oberfläche ----

// TestResult beschreibt das Ergebnis von „Anmeldung testen“.
type TestResult struct {
	OK       bool     `json:"ok"`
	Message  string   `json:"message"`
	Source   string   `json:"source"`
	Groups   []string `json:"groups,omitempty"`
	Group    string   `json:"group,omitempty"`
	VLAN     string   `json:"vlan,omitempty"`
	WLANOK   bool     `json:"wlanOk"`
	WLANNote string   `json:"wlanNote,omitempty"`
}

// TestLogin prüft Benutzername/Passwort genau wie bei einer echten Anmeldung –
// inklusive MS-CHAPv2 (WLAN) und Gruppenregeln (ohne Geräte-/WLAN-Einschränkung).
func (s *Server) TestLogin(name, pw string) TestResult {
	var r TestResult
	id, why := s.verifyPAP(name, pw)
	if id == nil {
		r.Message = why
		return r
	}
	r.Source = "Lokaler Benutzer"
	if id.AD {
		r.Source = "Active Directory"
		r.Groups = id.ADGroups
	}

	// MS-CHAPv2 wie bei PEAP durchspielen.
	authC, peerC := make([]byte, 16), make([]byte, 16)
	for i := range authC {
		authC[i], peerC[i] = byte(i*7+1), byte(i*13+5)
	}
	nt := auth.NTResponse(auth.ChallengeHash(peerC, authC, name), auth.NTHash(pw))
	if _, res, why := s.verifyMSCHAP(name, authC, peerC, nt); res == nil {
		r.WLANNote = why
	} else {
		r.WLANOK = true
	}

	dec, why := s.authorize(id, reqCtx{client: store.Client{ID: "*test*", Name: "Test"}})
	if why != "" && !strings.Contains(why, "darf sich nicht über") && !strings.Contains(why, "SSID") && !strings.Contains(why, "WLAN „") {
		r.Message = "Passwort stimmt, aber: " + why
		return r
	}
	r.OK, r.Group, r.VLAN = true, dec.Group, dec.VLAN
	r.Message = "Benutzername und Passwort stimmen."
	return r
}
