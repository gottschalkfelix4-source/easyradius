package radiusd

import (
	"encoding/hex"
	"errors"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gottschalkfelix4-source/easyradius/internal/attrs"
	"github.com/gottschalkfelix4-source/easyradius/internal/auth"
	"github.com/gottschalkfelix4-source/easyradius/internal/directory"
	"github.com/gottschalkfelix4-source/easyradius/internal/events"
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

// identity ist ein geprüfter Benutzer – lokal, aus dem AD oder eine MAC-Adresse.
type identity struct {
	Username string
	AD       bool
	Local    *store.User
	MAC      *store.MACEntry
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

// authorizeLocal wendet Gruppe, VLAN und Attribute eines lokalen Eintrags an.
func (s *Server) authorizeLocal(groupID, vlan string, list []attrs.Attr, rc reqCtx, now time.Time) (decision, string) {
	g, ok := s.Store.Group(groupID)
	if groupID == "" || !ok {
		return finish(decision{VLAN: vlan}, list, rc)
	}
	if why := checkGroup(&g, rc, now); why != "" {
		return decision{}, why
	}
	if vlan == "" {
		vlan = g.VLAN
	}
	return finish(decision{VLAN: vlan, Group: g.Name}, attrs.Merge(g.Attributes, list), rc)
}

// authorize wendet die Gruppenregeln an.
func (s *Server) authorize(id *identity, rc reqCtx) (decision, string) {
	now := time.Now()
	switch {
	case id.MAC != nil:
		return s.authorizeLocal(id.MAC.GroupID, id.MAC.VLAN, id.MAC.Attributes, rc, now)
	case !id.AD:
		return s.authorizeLocal(id.Local.GroupID, id.Local.VLAN, id.Local.Attributes, rc, now)
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
	if matched {
		// Der Benutzer gehört zu verknüpften Gruppen, die ihn aber alle ablehnen
		// (gesperrt, abgelaufen, falsches WLAN …). Das gilt immer – auch wenn
		// „nur mit verknüpfter Gruppe“ ausgeschaltet ist.
		return decision{}, firstReason
	}
	if s.Store.Settings().AD.RequireGroup {
		return decision{}, "AD-Benutzer ist in keiner AD-Gruppe, die mit einer EasyRadius-Gruppe verknüpft ist"
	}
	return decision{}, ""
}

// ---- Anmeldedaten prüfen ----

// validName lehnt Namen mit Steuerzeichen (u. a. NUL) und übergroße Namen ab.
func validName(name string) bool {
	if name == "" || len(name) > 253 {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

const badName = "Ungültiger Benutzername"

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

// dirReason übersetzt AD-Fehler; bad meldet ein falsches Passwort (für die Sperre).
func dirReason(err error) (reason string, bad bool) {
	var de *directory.Error
	if errors.As(err, &de) {
		return de.Msg, de.BadSecret
	}
	return "AD: " + err.Error(), false
}

// cred beschreibt, woher eine Anmeldung kommt (für die Sperre nach Fehlversuchen).
type cred struct {
	station string
	test    bool // Selbsttest aus der Oberfläche: keine Sperre zählen
}

func (s *Server) verifyPAP(name, pw string, c cred) (*identity, string) {
	if !validName(name) {
		return nil, badName
	}
	if why := s.locked(name, c); why != "" {
		return nil, why
	}
	u, exists, reason := s.localUser(name)
	if exists {
		if u == nil {
			return nil, reason
		}
		h, _ := hex.DecodeString(u.NTHash)
		if !auth.CheckPAP(pw, h) {
			s.failed(name, c)
			return nil, "Falsches Passwort (oder falsches Shared Secret)"
		}
		s.succeeded(name, c)
		return &identity{Username: u.Username, Local: u}, ""
	}
	d := s.directory()
	if d == nil {
		s.failed(name, c)
		return nil, reason
	}
	au, err := d.AuthPAP(name, pw)
	if err != nil {
		why, bad := dirReason(err)
		if bad {
			s.failed(name, c)
		}
		return nil, why
	}
	s.succeeded(name, c)
	return &identity{Username: au.Name, AD: true, ADGroups: au.Groups}, ""
}

func (s *Server) verifyMSCHAP(name string, authC, peerC, ntResp []byte, c cred) (*identity, *auth.MSCHAPv2Result, string) {
	if !validName(name) {
		return nil, nil, badName
	}
	if why := s.locked(name, c); why != "" {
		return nil, nil, why
	}
	u, exists, reason := s.localUser(name)
	if exists {
		if u == nil {
			return nil, nil, reason
		}
		h, _ := hex.DecodeString(u.NTHash)
		res, err := auth.VerifyMSCHAPv2(authC, peerC, ntResp, name, h)
		if err != nil {
			s.failed(name, c)
			return nil, nil, "Falsches Passwort"
		}
		s.succeeded(name, c)
		return &identity{Username: u.Username, Local: u}, res, ""
	}
	d := s.directory()
	if d == nil {
		s.failed(name, c)
		return nil, nil, reason
	}
	au, hashHash, err := d.AuthMSCHAPv2(name, auth.ChallengeHash(peerC, authC, name), ntResp)
	if err != nil {
		why, bad := dirReason(err)
		if bad {
			s.failed(name, c)
		}
		return nil, nil, why
	}
	s.succeeded(name, c)
	return &identity{Username: au.Name, AD: true, ADGroups: au.Groups},
		auth.MSCHAPv2Success(hashHash, ntResp, peerC, authC, name), ""
}

// macIdentity erkennt eine MAC-Anmeldung (MAB): Der Benutzername ist eine
// eingetragene MAC-Adresse und passt zur Calling-Station-Id (falls übermittelt).
func (s *Server) macIdentity(p *radius.Packet) (*identity, string, bool) {
	user := rfc2865.UserName_GetString(p)
	mac := store.NormalizeMAC(user)
	if mac == "" {
		return nil, "", false
	}
	m, ok := s.Store.FindMAC(mac)
	if !ok {
		// Call-Check ist eindeutig eine MAC-Anmeldung – sonst normal weiterprüfen.
		if rfc2865.ServiceType_Get(p) == rfc2865.ServiceType_Value_CallCheck {
			return nil, "MAC-Adresse " + mac + " ist nicht eingetragen", true
		}
		return nil, "", false
	}
	if st := rfc2865.CallingStationID_GetString(p); st != "" && store.NormalizeMAC(st) != mac {
		return nil, "MAC-Anmeldung: Benutzername passt nicht zur Calling-Station-Id", true
	}
	switch {
	case !m.Enabled:
		return nil, "MAC-Adresse ist deaktiviert", true
	case m.Expired(time.Now()):
		return nil, "MAC-Eintrag ist abgelaufen", true
	}
	s.Store.TouchMAC(m.ID)
	name := m.MAC
	if m.Name != "" {
		name = m.Name + " (" + m.MAC + ")"
	}
	return &identity{Username: name, MAC: &m}, "", true
}

// ---- Sperre nach Fehlversuchen ----

type lockState struct {
	fails []time.Time
	until time.Time
}

type lockouts struct {
	mu sync.Mutex
	m  map[string]*lockState
}

// LockInfo beschreibt eine aktive Sperre für die Oberfläche.
type LockInfo struct {
	Key   string    `json:"key"`
	Kind  string    `json:"kind"` // "user" oder "station"
	Name  string    `json:"name"`
	Until time.Time `json:"until"`
}

func lockKeys(name string, c cred) []string {
	keys := []string{"user|" + strings.ToLower(stripRealm(name))}
	if mac := store.NormalizeMAC(c.station); mac != "" {
		keys = append(keys, "station|"+mac)
	}
	return keys
}

func (s *Server) locked(name string, c cred) string {
	cfg := s.Store.Settings().Lockout
	if !cfg.Enabled || c.test {
		return ""
	}
	s.locks.mu.Lock()
	defer s.locks.mu.Unlock()
	for _, k := range lockKeys(name, c) {
		if st := s.locks.m[k]; st != nil && time.Now().Before(st.until) {
			if strings.HasPrefix(k, "user|") {
				return "Benutzer vorübergehend gesperrt (zu viele Fehlversuche)"
			}
			return "Endgerät vorübergehend gesperrt (zu viele Fehlversuche)"
		}
	}
	return ""
}

func (s *Server) failed(name string, c cred) {
	cfg := s.Store.Settings().Lockout
	if !cfg.Enabled || c.test {
		return
	}
	window := time.Duration(cfg.Minutes) * time.Minute
	now := time.Now()
	s.locks.mu.Lock()
	defer s.locks.mu.Unlock()
	if s.locks.m == nil {
		s.locks.m = map[string]*lockState{}
	}
	for _, k := range lockKeys(name, c) {
		st := s.locks.m[k]
		if st == nil {
			if len(s.locks.m) >= 20000 {
				continue
			}
			st = &lockState{}
			s.locks.m[k] = st
		}
		st.fails = slices.DeleteFunc(st.fails, func(t time.Time) bool { return now.Sub(t) > window })
		st.fails = append(st.fails, now)
		if len(st.fails) >= cfg.Attempts && now.After(st.until) {
			st.until = now.Add(window)
			st.fails = nil
			kind, who := "Benutzer", k[5:]
			if strings.HasPrefix(k, "station|") {
				kind, who = "Endgerät", k[8:]
			}
			s.Log.Add(events.Event{Kind: events.Warning, User: who,
				Message: kind + " „" + who + "“ nach " + strconv.Itoa(cfg.Attempts) + " Fehlversuchen für " + strconv.Itoa(cfg.Minutes) + " Minuten gesperrt"})
		}
	}
}

func (s *Server) succeeded(name string, c cred) {
	if c.test {
		return
	}
	s.locks.mu.Lock()
	defer s.locks.mu.Unlock()
	delete(s.locks.m, "user|"+strings.ToLower(stripRealm(name)))
}

// pruneLocks entfernt abgelaufene Einträge (vom Aufräumer aufgerufen).
func (s *Server) pruneLocks() {
	window := time.Duration(s.Store.Settings().Lockout.Minutes) * time.Minute
	now := time.Now()
	s.locks.mu.Lock()
	defer s.locks.mu.Unlock()
	for k, st := range s.locks.m {
		st.fails = slices.DeleteFunc(st.fails, func(t time.Time) bool { return now.Sub(t) > window })
		if len(st.fails) == 0 && now.After(st.until) {
			delete(s.locks.m, k)
		}
	}
}

// Lockouts listet aktive Sperren.
func (s *Server) Lockouts() []LockInfo {
	s.locks.mu.Lock()
	defer s.locks.mu.Unlock()
	out := []LockInfo{}
	for k, st := range s.locks.m {
		if time.Now().Before(st.until) {
			kind, name, _ := strings.Cut(k, "|")
			out = append(out, LockInfo{Key: k, Kind: kind, Name: name, Until: st.until})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Until.After(out[j].Until) })
	return out
}

// Unlock hebt eine Sperre auf ("" = alle).
func (s *Server) Unlock(key string) {
	s.locks.mu.Lock()
	defer s.locks.mu.Unlock()
	if key == "" {
		s.locks.m = map[string]*lockState{}
		return
	}
	delete(s.locks.m, key)
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
			if old, ok := s.dirs.dir.(*directory.LDAP); ok {
				old.Close()
			}
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
	test := cred{test: true}
	id, why := s.verifyPAP(name, pw, test)
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
	if _, res, why := s.verifyMSCHAP(name, authC, peerC, nt, test); res == nil {
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
