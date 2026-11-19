// Package store speichert Benutzer, Geräte und Einstellungen in einer JSON-Datei.
package store

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gottschalkfelix4-source/easyradius/internal/attrs"
)

// Aktuelle Version des Dateiformats.
const formatVersion = 2

type User struct {
	ID          string       `json:"id"`
	Username    string       `json:"username"`
	FullName    string       `json:"fullName"`
	NTHash      string       `json:"ntHash"` // hex, nie an die GUI ausgeliefert
	Enabled     bool         `json:"enabled"`
	VLAN        string       `json:"vlan"`
	GroupID     string       `json:"groupId"`
	Attributes  []attrs.Attr `json:"attributes"`
	Note        string       `json:"note"`
	ExpiresAt   string       `json:"expiresAt"` // YYYY-MM-DD, leer = nie
	CreatedAt   time.Time    `json:"createdAt"`
	LastLoginAt time.Time    `json:"lastLoginAt"`
}

// Expired meldet, ob das Konto abgelaufen ist.
func (u *User) Expired(now time.Time) bool { return expired(u.ExpiresAt, now) }

func expired(date string, now time.Time) bool {
	if date == "" {
		return false
	}
	d, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil {
		return false
	}
	return now.After(d.Add(24 * time.Hour))
}

// Group bündelt Regeln für mehrere Benutzer.
type Group struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Description    string       `json:"description"`
	Enabled        bool         `json:"enabled"`
	VLAN           string       `json:"vlan"`
	ExpiresAt      string       `json:"expiresAt"`
	AllowedClients []string     `json:"allowedClients"` // Geräte-IDs, leer = alle
	AllowedSSIDs   []string     `json:"allowedSsids"`   // leer = alle
	ADGroups       []string     `json:"adGroups"`       // verknüpfte AD-Gruppen
	Attributes     []attrs.Attr `json:"attributes"`     // zusätzliche Antwortattribute
	CreatedAt      time.Time    `json:"createdAt"`
}

func (g *Group) Expired(now time.Time) bool { return expired(g.ExpiresAt, now) }

type Client struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Address   string    `json:"address"` // IP oder Netz in CIDR-Schreibweise
	Secret    string    `json:"secret"`
	RequireMA bool      `json:"requireMA"` // Message-Authenticator verlangen (Blast-RADIUS)
	CoAPort   int       `json:"coaPort"`   // Port für Disconnect/CoA (RFC 5176), 0 = 3799
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"createdAt"`
	LastSeen  time.Time `json:"lastSeen"`
}

// Matches prüft, ob eine IP zu diesem Gerät gehört.
func (c *Client) Matches(ip net.IP) bool {
	if strings.Contains(c.Address, "/") {
		_, n, err := net.ParseCIDR(c.Address)
		return err == nil && n.Contains(ip)
	}
	a := net.ParseIP(c.Address)
	return a != nil && a.Equal(ip)
}

// MACEntry erlaubt Geräten ohne 802.1X (Drucker, Telefone, Kameras) die Anmeldung
// anhand ihrer MAC-Adresse (MAC Authentication Bypass).
type MACEntry struct {
	ID         string       `json:"id"`
	MAC        string       `json:"mac"` // normalisiert: aa-bb-cc-dd-ee-ff
	Name       string       `json:"name"`
	Enabled    bool         `json:"enabled"`
	VLAN       string       `json:"vlan"`
	GroupID    string       `json:"groupId"`
	Attributes []attrs.Attr `json:"attributes"`
	Note       string       `json:"note"`
	ExpiresAt  string       `json:"expiresAt"`
	CreatedAt  time.Time    `json:"createdAt"`
	LastSeen   time.Time    `json:"lastSeen"`
}

func (m *MACEntry) Expired(now time.Time) bool { return expired(m.ExpiresAt, now) }

// Rollen der Administratoren.
const (
	RoleAdmin  = "admin"  // darf alles
	RoleViewer = "viewer" // nur lesen
)

// Admin ist ein Konto für die Weboberfläche und die Windows-App.
type Admin struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Hash      string    `json:"hash"` // bcrypt
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
	LastLogin time.Time `json:"lastLogin"`
	// Sitzungen, die vor diesem Zeitpunkt begonnen haben, sind ungültig.
	SessionsValidFrom time.Time `json:"sessionsValidFrom"`
}

// APIToken erlaubt Skripten den Zugriff auf die API ohne Passwort.
type APIToken struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Hash      string    `json:"hash"`   // SHA-256 des Tokens (hex)
	Prefix    string    `json:"prefix"` // erste Zeichen zur Wiedererkennung
	Role      string    `json:"role"`
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
	LastUsed  time.Time `json:"lastUsed"`
	ExpiresAt string    `json:"expiresAt"` // YYYY-MM-DD, leer = nie
}

func (t *APIToken) Expired(now time.Time) bool { return expired(t.ExpiresAt, now) }

type Settings struct {
	AuthPort     int    `json:"authPort"`
	AcctPort     int    `json:"acctPort"`
	WebAddr      string `json:"webAddr"`
	HTTPSAddr    string `json:"httpsAddr"` // Weboberfläche per HTTPS, leer = aus
	HTTPSOnly    bool   `json:"httpsOnly"` // aus dem Netz nur HTTPS (außer Einrichtungsseite)
	ServerName   string `json:"serverName"`
	SessionKey   string `json:"sessionKey"`
	LogRetention int    `json:"logRetention"` // Anzahl Einträge

	// Alt (bis 1.6): ein einzelnes Administratorkonto. Wird beim Laden übernommen.
	AdminUser string `json:"adminUser,omitempty"`
	AdminHash string `json:"adminHash,omitempty"`

	Lockout LockoutSettings `json:"lockout"`
	EAP     EAPSettings     `json:"eap"`
	AD      ADSettings      `json:"ad"`
	ACME    ACMESettings    `json:"acme"`
}

// LockoutSettings: Schutz vor Passwort-Raten über RADIUS.
type LockoutSettings struct {
	Enabled  bool `json:"enabled"`
	Attempts int  `json:"attempts"` // Fehlversuche …
	Minutes  int  `json:"minutes"`  // … innerhalb dieser Zeit sperren für dieselbe Dauer
}

// EAPSettings: welche WLAN-Verfahren neben PEAP angeboten werden.
type EAPSettings struct {
	TTLS bool `json:"ttls"` // EAP-TTLS mit PAP oder MS-CHAPv2 innen
	TLS  bool `json:"tls"`  // EAP-TLS mit Benutzerzertifikaten der eigenen CA
}

// ADSettings steuert die Anbindung an Active Directory.
type ADSettings struct {
	Mode          string `json:"mode"`         // "off", "domain", "ldap"
	RequireGroup  bool   `json:"requireGroup"` // nur AD-Benutzer mit verknüpfter Gruppe
	LDAPURL       string `json:"ldapUrl"`      // ldaps://dc.firma.local
	StartTLS      bool   `json:"startTls"`
	SkipVerify    bool   `json:"skipVerify"` // Zertifikat des DC nicht prüfen
	BaseDN        string `json:"baseDn"`
	BindUser      string `json:"bindUser"` // z. B. svc-radius@firma.local
	BindPassword  string `json:"bindPassword"`
	UserAttribute string `json:"userAttribute"` // sAMAccountName
}

// ACMESettings für automatische Let's-Encrypt-Zertifikate.
type ACMESettings struct {
	Enabled     bool              `json:"enabled"`
	Domain      string            `json:"domain"`
	Email       string            `json:"email"`
	Method      string            `json:"method"`   // "dns", "acmedns", "http"
	Provider    string            `json:"provider"` // Code des DNS-Anbieters
	Credentials map[string]string `json:"credentials"`
	ACMEDNSURL  string            `json:"acmeDnsUrl"`
	CNAMEName   string            `json:"cnameName"` // ausstehender CNAME (Methode acmedns)
	CNAMETarget string            `json:"cnameTarget"`
	APIToken    string            `json:"apiToken"` // alt (nur Cloudflare)
	Staging     bool              `json:"staging"`
	AccountKey  string            `json:"accountKey"` // PEM
	LastSuccess time.Time         `json:"lastSuccess"`
	LastError   string            `json:"lastError"`
	LastAttempt time.Time         `json:"lastAttempt"`
}

type data struct {
	Version  int         `json:"version"`
	Settings Settings    `json:"settings"`
	Admins   []*Admin    `json:"admins"`
	Tokens   []*APIToken `json:"tokens"`
	Users    []*User     `json:"users"`
	Clients  []*Client   `json:"clients"`
	Groups   []*Group    `json:"groups"`
	MACs     []*MACEntry `json:"macs"`
}

type Store struct {
	mu    sync.RWMutex
	path  string
	d     data
	key   []byte // Schlüssel für Geheimnisse in der Datei
	dirty bool   // ungespeicherte Kleinigkeiten (letzte Anmeldung usw.)
}

var ErrNotFound = errors.New("nicht gefunden")

func defaultSettings() Settings {
	return Settings{AuthPort: 1812, AcctPort: 1813, WebAddr: ":8088", HTTPSAddr: ":8443",
		ServerName: "EasyRadius", LogRetention: 5000,
		Lockout: LockoutSettings{Enabled: true, Attempts: 10, Minutes: 15},
		EAP:     EAPSettings{TTLS: true}}
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	key, err := loadKey(dir)
	if err != nil {
		return nil, fmt.Errorf("Schlüssel für Geheimnisse: %w", err)
	}
	s := &Store{path: filepath.Join(dir, "config.json"), key: key}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		// Absturz zwischen den Umbenennungen beim Speichern? Dann die Sicherung nehmen.
		if bb, e2 := os.ReadFile(s.path + ".bak"); e2 == nil {
			b, err = bb, nil
		}
	}
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.d.Settings = defaultSettings()
		s.d.Version = formatVersion
	case err != nil:
		return nil, err
	default:
		if err := s.decode(b); err != nil {
			bak, e2 := os.ReadFile(s.path + ".bak")
			if e2 != nil || s.decode(bak) != nil {
				return nil, fmt.Errorf("config.json beschädigt: %w", err)
			}
		}
	}
	s.migrate()
	return s, s.saveLocked()
}

// ReadSettings liest nur die Einstellungen, ohne etwas zu verändern
// (für Hilfsbefehle, die ohne Administratorrechte laufen).
func ReadSettings(dir string) (Settings, error) {
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return defaultSettings(), err
	}
	var d struct {
		Settings Settings `json:"settings"`
	}
	d.Settings = defaultSettings()
	err = json.Unmarshal(bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF}), &d)
	return d.Settings, err
}

func (s *Store) decode(b []byte) error {
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF}) // z. B. mit dem Windows-Editor gespeichert
	var d data
	if err := json.Unmarshal(b, &d); err != nil {
		return err
	}
	if err := unsealAll(&d, s.key); err != nil {
		return err
	}
	s.d = d
	return nil
}

// migrate bringt ältere Dateien auf den aktuellen Stand.
func (s *Store) migrate() {
	st := &s.d.Settings
	if st.SessionKey == "" {
		st.SessionKey = NewID() + NewID()
	}
	if st.LogRetention == 0 {
		st.LogRetention = 5000
	}
	if st.AD.Mode == "" {
		st.AD.Mode = "off"
		st.AD.RequireGroup = true
		st.AD.UserAttribute = "sAMAccountName"
	}
	if s.d.Version < 2 {
		// 1.x: Ein Admin in den Einstellungen → Liste von Admins.
		if st.AdminHash != "" && len(s.d.Admins) == 0 {
			s.d.Admins = append(s.d.Admins, &Admin{ID: NewID(), Username: st.AdminUser, Hash: st.AdminHash,
				Role: RoleAdmin, CreatedAt: time.Now()})
		}
		st.HTTPSAddr = ":8443"
		st.Lockout = LockoutSettings{Enabled: true, Attempts: 10, Minutes: 15}
		st.EAP.TTLS = true
		// Bestehende Geräte liefen bisher ohne Pflicht zum Message-Authenticator.
		// Neue Geräte bekommen die Pflicht standardmäßig.
		s.d.Version = 2
	}
	st.AdminUser, st.AdminHash = "", ""
	if st.Lockout.Attempts <= 0 {
		st.Lockout.Attempts = 10
	}
	if st.Lockout.Minutes <= 0 {
		st.Lockout.Minutes = 15
	}
}

// saveLocked schreibt die Datei atomar: erst vollständig in eine temporäre Datei,
// dann umbenennen. Die vorige Fassung bleibt als config.json.bak erhalten.
func (s *Store) saveLocked() error {
	b, err := s.encode()
	if err != nil {
		return err
	}
	if err := writeFileSync(s.path+".tmp", b); err != nil {
		return err
	}
	if _, err := os.Stat(s.path); err == nil {
		os.Remove(s.path + ".bak")
		if err := os.Rename(s.path, s.path+".bak"); err != nil {
			return err
		}
	}
	if err := os.Rename(s.path+".tmp", s.path); err != nil {
		return err
	}
	syncDir(filepath.Dir(s.path))
	s.dirty = false
	return nil
}

func (s *Store) encode() ([]byte, error) {
	// Tiefe Kopie, damit die Geheimnisse im Speicher im Klartext bleiben.
	raw, err := json.Marshal(&s.d)
	if err != nil {
		return nil, err
	}
	var cp data
	if err := json.Unmarshal(raw, &cp); err != nil {
		return nil, err
	}
	if err := sealAll(&cp, s.key); err != nil {
		return nil, err
	}
	return json.MarshalIndent(&cp, "", "  ")
}

func writeFileSync(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// mutate führt eine Änderung aus und speichert sie. Schlägt das Speichern fehl,
// wird der Zustand im Speicher zurückgesetzt – Datei und Speicher bleiben gleich.
func (s *Store) mutate(fn func() error) error {
	before, err := json.Marshal(&s.d)
	if err != nil {
		return err
	}
	restore := func() {
		var d data
		if json.Unmarshal(before, &d) == nil {
			s.d = d
		}
	}
	if err := fn(); err != nil {
		restore()
		return err
	}
	if err := s.saveLocked(); err != nil {
		restore()
		return fmt.Errorf("Speichern fehlgeschlagen: %w", err)
	}
	return nil
}

func NewID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ---- Einstellungen ----

func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.d.Settings
	st.ACME.Credentials = cloneMap(st.ACME.Credentials)
	return st
}

func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s *Store) UpdateSettings(fn func(*Settings)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(func() error { fn(&s.d.Settings); return nil })
}

// ---- Administratoren ----

func (s *Store) Admins() []Admin {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Admin, 0, len(s.d.Admins))
	for _, a := range s.d.Admins {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Username) < strings.ToLower(out[j].Username) })
	return out
}

// NeedsSetup meldet, ob noch kein Administrator angelegt ist.
func (s *Store) NeedsSetup() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.d.Admins) == 0
}

func (s *Store) FindAdmin(name string) (Admin, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.d.Admins {
		if strings.EqualFold(a.Username, name) {
			return *a, true
		}
	}
	return Admin{}, false
}

func (s *Store) AdminByID(id string) (Admin, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.d.Admins {
		if a.ID == id {
			return *a, true
		}
	}
	return Admin{}, false
}

func validAdminName(n string) error {
	if n == "" {
		return errors.New("Benutzername fehlt")
	}
	if len(n) > 64 || strings.ContainsAny(n, "|\\/\"<>") || strings.IndexFunc(n, func(r rune) bool { return r < 0x20 }) >= 0 {
		return errors.New("Der Benutzername enthält unzulässige Zeichen")
	}
	return nil
}

// CreateFirstAdmin legt das erste Konto an – nur solange noch keines existiert.
func (s *Store) CreateFirstAdmin(name, hash string) (Admin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = strings.TrimSpace(name)
	if err := validAdminName(name); err != nil {
		return Admin{}, err
	}
	if len(s.d.Admins) > 0 {
		return Admin{}, errors.New("Die Einrichtung ist bereits abgeschlossen")
	}
	a := &Admin{ID: NewID(), Username: name, Hash: hash, Role: RoleAdmin, CreatedAt: time.Now()}
	err := s.mutate(func() error { s.d.Admins = append(s.d.Admins, a); return nil })
	return *a, err
}

// SaveAdmin legt ein Konto an oder ändert es. hash leer = Passwort unverändert.
func (s *Store) SaveAdmin(a Admin) (Admin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.Username = strings.TrimSpace(a.Username)
	if err := validAdminName(a.Username); err != nil {
		return a, err
	}
	if a.Role != RoleAdmin && a.Role != RoleViewer {
		return a, errors.New("Unbekannte Rolle")
	}
	for _, o := range s.d.Admins {
		if o.ID != a.ID && strings.EqualFold(o.Username, a.Username) {
			return a, errors.New("Diesen Benutzernamen gibt es schon")
		}
	}
	if a.ID == "" {
		if a.Hash == "" {
			return a, errors.New("Passwort fehlt")
		}
		a.ID, a.CreatedAt = NewID(), time.Now()
		cp := a
		return a, s.mutate(func() error { s.d.Admins = append(s.d.Admins, &cp); return nil })
	}
	i := slices.IndexFunc(s.d.Admins, func(o *Admin) bool { return o.ID == a.ID })
	if i < 0 {
		return a, ErrNotFound
	}
	o := s.d.Admins[i]
	if o.Role == RoleAdmin && a.Role != RoleAdmin && s.countAdmins() == 1 {
		return a, errors.New("Mindestens ein Konto muss die Rolle Administrator behalten")
	}
	a.CreatedAt, a.LastLogin, a.SessionsValidFrom = o.CreatedAt, o.LastLogin, o.SessionsValidFrom
	if a.Hash == "" {
		a.Hash = o.Hash
	} else {
		a.SessionsValidFrom = time.Now() // neues Passwort: alte Sitzungen beenden
	}
	if a.Role != o.Role {
		a.SessionsValidFrom = time.Now()
	}
	cp := a
	return a, s.mutate(func() error { s.d.Admins[i] = &cp; return nil })
}

func (s *Store) countAdmins() int {
	n := 0
	for _, a := range s.d.Admins {
		if a.Role == RoleAdmin {
			n++
		}
	}
	return n
}

func (s *Store) DeleteAdmin(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.d.Admins, func(o *Admin) bool { return o.ID == id })
	if i < 0 {
		return ErrNotFound
	}
	if s.d.Admins[i].Role == RoleAdmin && s.countAdmins() == 1 {
		return errors.New("Das letzte Administratorkonto kann nicht gelöscht werden")
	}
	return s.mutate(func() error {
		s.d.Admins = slices.Delete(slices.Clone(s.d.Admins), i, i+1)
		return nil
	})
}

// RevokeSessions beendet alle Sitzungen eines Kontos (z. B. beim Abmelden überall).
func (s *Store) RevokeSessions(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.d.Admins {
		if a.ID == id {
			return s.mutate(func() error { a.SessionsValidFrom = time.Now(); return nil })
		}
	}
	return ErrNotFound
}

func (s *Store) TouchAdmin(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.d.Admins {
		if a.ID == id {
			a.LastLogin = time.Now()
			s.dirty = true
		}
	}
}

// ---- API-Tokens ----

func (s *Store) Tokens() []APIToken {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]APIToken, 0, len(s.d.Tokens))
	for _, t := range s.d.Tokens {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *Store) AddToken(t APIToken) (APIToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		return t, errors.New("Name fehlt")
	}
	if t.Role != RoleAdmin && t.Role != RoleViewer {
		return t, errors.New("Unbekannte Rolle")
	}
	if t.ExpiresAt != "" {
		if _, err := time.Parse("2006-01-02", t.ExpiresAt); err != nil {
			return t, errors.New("Ablaufdatum ungültig")
		}
	}
	t.ID, t.CreatedAt = NewID(), time.Now()
	cp := t
	return t, s.mutate(func() error { s.d.Tokens = append(s.d.Tokens, &cp); return nil })
}

// TokenByHash sucht ein gültiges Token und merkt sich die Nutzung.
func (s *Store) TokenByHash(h string) (APIToken, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.d.Tokens {
		if t.Hash == h && !t.Expired(time.Now()) {
			if time.Since(t.LastUsed) > time.Minute {
				t.LastUsed = time.Now()
				s.dirty = true
			}
			return *t, true
		}
	}
	return APIToken{}, false
}

func (s *Store) DeleteToken(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.d.Tokens, func(o *APIToken) bool { return o.ID == id })
	if i < 0 {
		return ErrNotFound
	}
	return s.mutate(func() error {
		s.d.Tokens = slices.Delete(slices.Clone(s.d.Tokens), i, i+1)
		return nil
	})
}

// ---- Benutzer ----

func (s *Store) Users() []User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]User, 0, len(s.d.Users))
	for _, u := range s.d.Users {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Username) < strings.ToLower(out[j].Username) })
	return out
}

// FindUser sucht ohne Beachtung der Groß-/Kleinschreibung.
func (s *Store) FindUser(username string) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.d.Users {
		if strings.EqualFold(u.Username, username) {
			return *u, true
		}
	}
	return User{}, false
}

func validUserName(n string) error {
	if n == "" {
		return errors.New("Benutzername fehlt")
	}
	if len(n) > 253 || strings.IndexFunc(n, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return errors.New("Der Benutzername enthält unzulässige Zeichen")
	}
	return nil
}

func (s *Store) SaveUser(u User) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveUserLocked(u)
}

func (s *Store) saveUserLocked(u User) (User, error) {
	u.Username = strings.TrimSpace(u.Username)
	if err := validUserName(u.Username); err != nil {
		return u, err
	}
	for _, o := range s.d.Users {
		if o.ID != u.ID && strings.EqualFold(o.Username, u.Username) {
			return u, errors.New("Benutzername „" + u.Username + "“ ist bereits vergeben")
		}
	}
	if err := normalizeAttrs(u.Attributes); err != nil {
		return u, err
	}
	if u.ID == "" {
		if u.NTHash == "" {
			return u, errors.New("Passwort fehlt")
		}
		u.ID = NewID()
		u.CreatedAt = time.Now()
		cp := u
		return u, s.mutate(func() error { s.d.Users = append(s.d.Users, &cp); return nil })
	}
	i := slices.IndexFunc(s.d.Users, func(o *User) bool { return o.ID == u.ID })
	if i < 0 {
		return u, ErrNotFound
	}
	o := s.d.Users[i]
	if u.NTHash == "" {
		u.NTHash = o.NTHash
	}
	u.CreatedAt, u.LastLoginAt = o.CreatedAt, o.LastLoginAt
	cp := u
	return u, s.mutate(func() error { s.d.Users[i] = &cp; return nil })
}

// ImportUsers legt mehrere Benutzer in einem Schritt an bzw. aktualisiert sie
// (Abgleich über den Benutzernamen). Entweder alle oder keiner.
func (s *Store) ImportUsers(list []User) (created, updated int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err = s.mutate(func() error {
		for _, u := range list {
			u.Username = strings.TrimSpace(u.Username)
			if err := validUserName(u.Username); err != nil {
				return err
			}
			if err := normalizeAttrs(u.Attributes); err != nil {
				return err
			}
			i := slices.IndexFunc(s.d.Users, func(o *User) bool { return strings.EqualFold(o.Username, u.Username) })
			if i < 0 {
				if u.NTHash == "" {
					return errors.New("Für „" + u.Username + "“ fehlt das Passwort")
				}
				u.ID, u.CreatedAt = NewID(), time.Now()
				cp := u
				s.d.Users = append(s.d.Users, &cp)
				created++
				continue
			}
			o := s.d.Users[i]
			u.ID, u.CreatedAt, u.LastLoginAt = o.ID, o.CreatedAt, o.LastLoginAt
			if u.NTHash == "" {
				u.NTHash = o.NTHash
			}
			if u.Attributes == nil {
				u.Attributes = o.Attributes
			}
			cp := u
			s.d.Users[i] = &cp
			updated++
		}
		return nil
	})
	if err != nil {
		created, updated = 0, 0
	}
	return
}

func (s *Store) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.d.Users, func(o *User) bool { return o.ID == id })
	if i < 0 {
		return ErrNotFound
	}
	return s.mutate(func() error {
		s.d.Users = slices.Delete(slices.Clone(s.d.Users), i, i+1)
		return nil
	})
}

// TouchUser merkt sich die letzte erfolgreiche Anmeldung (nur im Speicher,
// gesichert beim nächsten Speichern – vermeidet Schreibzugriffe pro Login).
func (s *Store) TouchUser(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.d.Users {
		if strings.EqualFold(u.Username, username) {
			u.LastLoginAt = time.Now()
			s.dirty = true
		}
	}
}

// ---- Geräte (RADIUS-Clients) ----

func (s *Store) Clients() []Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Client, 0, len(s.d.Clients))
	for _, c := range s.d.Clients {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func (s *Store) Client(id string) (Client, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.d.Clients {
		if c.ID == id {
			return *c, true
		}
	}
	return Client{}, false
}

// ClientFor liefert das passende Gerät; exakte IPs haben Vorrang vor Netzen.
func (s *Store) ClientFor(ip net.IP) (Client, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var best *Client
	bestOnes := -1
	for _, c := range s.d.Clients {
		if !c.Matches(ip) {
			continue
		}
		ones := 128
		if strings.Contains(c.Address, "/") {
			_, n, _ := net.ParseCIDR(c.Address)
			ones, _ = n.Mask.Size()
		}
		if ones > bestOnes {
			best, bestOnes = c, ones
		}
	}
	if best == nil {
		return Client{}, false
	}
	return *best, true
}

func (s *Store) TouchClient(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.d.Clients {
		if c.ID == id && time.Since(c.LastSeen) > 30*time.Second {
			c.LastSeen = time.Now()
			s.dirty = true
		}
	}
}

func (s *Store) SaveClient(c Client) (Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.Name, c.Address = strings.TrimSpace(c.Name), strings.TrimSpace(c.Address)
	if c.Name == "" {
		return c, errors.New("Name fehlt")
	}
	if strings.Contains(c.Address, "/") {
		if _, _, err := net.ParseCIDR(c.Address); err != nil {
			return c, errors.New("Netz ungültig (Beispiel: 192.168.1.0/24)")
		}
	} else if net.ParseIP(c.Address) == nil {
		return c, errors.New("IP-Adresse ungültig")
	}
	if c.CoAPort < 0 || c.CoAPort > 65535 {
		return c, errors.New("CoA-Port ungültig")
	}
	if c.ID == "" {
		if c.Secret == "" {
			return c, errors.New("Shared Secret fehlt")
		}
		c.ID = NewID()
		c.CreatedAt = time.Now()
		cp := c
		return c, s.mutate(func() error { s.d.Clients = append(s.d.Clients, &cp); return nil })
	}
	i := slices.IndexFunc(s.d.Clients, func(o *Client) bool { return o.ID == c.ID })
	if i < 0 {
		return c, ErrNotFound
	}
	o := s.d.Clients[i]
	if c.Secret == "" {
		c.Secret = o.Secret
	}
	c.CreatedAt, c.LastSeen = o.CreatedAt, o.LastSeen
	cp := c
	return c, s.mutate(func() error { s.d.Clients[i] = &cp; return nil })
}

func (s *Store) DeleteClient(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.d.Clients, func(o *Client) bool { return o.ID == id })
	if i < 0 {
		return ErrNotFound
	}
	return s.mutate(func() error {
		s.d.Clients = slices.Delete(slices.Clone(s.d.Clients), i, i+1)
		for _, g := range s.d.Groups {
			g.AllowedClients = slices.DeleteFunc(slices.Clone(g.AllowedClients), func(x string) bool { return x == id })
		}
		return nil
	})
}

// Flush schreibt In-Memory-Änderungen (z. B. letzte Logins) auf die Platte.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	return s.saveLocked()
}

// ---- Gruppen ----

func (s *Store) Groups() []Group {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Group, 0, len(s.d.Groups))
	for _, g := range s.d.Groups {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func (s *Store) Group(id string) (Group, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, g := range s.d.Groups {
		if g.ID == id {
			return *g, true
		}
	}
	return Group{}, false
}

// GroupByName sucht eine Gruppe ohne Beachtung der Groß-/Kleinschreibung.
func (s *Store) GroupByName(name string) (Group, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, g := range s.d.Groups {
		if strings.EqualFold(g.Name, strings.TrimSpace(name)) {
			return *g, true
		}
	}
	return Group{}, false
}

func cleanList(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v != "" && !seen[strings.ToLower(v)] {
			seen[strings.ToLower(v)] = true
			out = append(out, v)
		}
	}
	return out
}

func (s *Store) SaveGroup(g Group) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g.Name = strings.TrimSpace(g.Name)
	if g.Name == "" {
		return g, errors.New("Name fehlt")
	}
	for _, o := range s.d.Groups {
		if o.ID != g.ID && strings.EqualFold(o.Name, g.Name) {
			return g, errors.New("Diesen Gruppennamen gibt es schon")
		}
	}
	g.AllowedClients = cleanList(g.AllowedClients)
	g.AllowedSSIDs = cleanList(g.AllowedSSIDs)
	g.ADGroups = cleanList(g.ADGroups)
	if err := normalizeAttrs(g.Attributes); err != nil {
		return g, err
	}
	if g.ID == "" {
		g.ID = NewID()
		g.CreatedAt = time.Now()
		cp := g
		return g, s.mutate(func() error { s.d.Groups = append(s.d.Groups, &cp); return nil })
	}
	i := slices.IndexFunc(s.d.Groups, func(o *Group) bool { return o.ID == g.ID })
	if i < 0 {
		return g, ErrNotFound
	}
	g.CreatedAt = s.d.Groups[i].CreatedAt
	cp := g
	return g, s.mutate(func() error { s.d.Groups[i] = &cp; return nil })
}

// DeleteGroup entfernt die Gruppe und löst die Zuordnung der Benutzer.
func (s *Store) DeleteGroup(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.d.Groups, func(o *Group) bool { return o.ID == id })
	if i < 0 {
		return ErrNotFound
	}
	return s.mutate(func() error {
		s.d.Groups = slices.Delete(slices.Clone(s.d.Groups), i, i+1)
		for _, u := range s.d.Users {
			if u.GroupID == id {
				u.GroupID = ""
			}
		}
		for _, m := range s.d.MACs {
			if m.GroupID == id {
				m.GroupID = ""
			}
		}
		return nil
	})
}

// ---- MAC-Adressen (MAB) ----

// NormalizeMAC bringt "AA:BB:CC:DD:EE:FF", "aabb.ccdd.eeff" usw. auf "aa-bb-cc-dd-ee-ff".
// Liefert "" für alles, was keine MAC-Adresse ist.
func NormalizeMAC(s string) string {
	var hexd []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
			hexd = append(hexd, c)
		case c >= 'A' && c <= 'F':
			hexd = append(hexd, c+32)
		case c == ':' || c == '-' || c == '.' || c == ' ':
		default:
			return ""
		}
	}
	if len(hexd) != 12 {
		return ""
	}
	out := make([]byte, 0, 17)
	for i := 0; i < 12; i += 2 {
		if i > 0 {
			out = append(out, '-')
		}
		out = append(out, hexd[i], hexd[i+1])
	}
	return string(out)
}

func (s *Store) MACs() []MACEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]MACEntry, 0, len(s.d.MACs))
	for _, m := range s.d.MACs {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MAC < out[j].MAC })
	return out
}

func (s *Store) FindMAC(mac string) (MACEntry, bool) {
	mac = NormalizeMAC(mac)
	if mac == "" {
		return MACEntry{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.d.MACs {
		if m.MAC == mac {
			return *m, true
		}
	}
	return MACEntry{}, false
}

func (s *Store) prepMAC(m *MACEntry) error {
	m.MAC = NormalizeMAC(m.MAC)
	if m.MAC == "" {
		return errors.New("MAC-Adresse ungültig (Beispiel: AA-BB-CC-DD-EE-FF)")
	}
	m.Name = strings.TrimSpace(m.Name)
	for _, o := range s.d.MACs {
		if o.ID != m.ID && o.MAC == m.MAC {
			return errors.New("Die MAC-Adresse " + m.MAC + " ist bereits eingetragen")
		}
	}
	return normalizeAttrs(m.Attributes)
}

func (s *Store) SaveMAC(m MACEntry) (MACEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepMAC(&m); err != nil {
		return m, err
	}
	if m.ID == "" {
		m.ID, m.CreatedAt = NewID(), time.Now()
		cp := m
		return m, s.mutate(func() error { s.d.MACs = append(s.d.MACs, &cp); return nil })
	}
	i := slices.IndexFunc(s.d.MACs, func(o *MACEntry) bool { return o.ID == m.ID })
	if i < 0 {
		return m, ErrNotFound
	}
	m.CreatedAt, m.LastSeen = s.d.MACs[i].CreatedAt, s.d.MACs[i].LastSeen
	cp := m
	return m, s.mutate(func() error { s.d.MACs[i] = &cp; return nil })
}

// ImportMACs legt MAC-Einträge an bzw. aktualisiert sie (Abgleich über die Adresse).
func (s *Store) ImportMACs(list []MACEntry) (created, updated int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err = s.mutate(func() error {
		for _, m := range list {
			mac := NormalizeMAC(m.MAC)
			i := slices.IndexFunc(s.d.MACs, func(o *MACEntry) bool { return o.MAC == mac })
			if i >= 0 {
				m.ID = s.d.MACs[i].ID
			}
			if err := s.prepMAC(&m); err != nil {
				return err
			}
			if i < 0 {
				m.ID, m.CreatedAt = NewID(), time.Now()
				cp := m
				s.d.MACs = append(s.d.MACs, &cp)
				created++
				continue
			}
			m.CreatedAt, m.LastSeen = s.d.MACs[i].CreatedAt, s.d.MACs[i].LastSeen
			if m.Attributes == nil {
				m.Attributes = s.d.MACs[i].Attributes
			}
			cp := m
			s.d.MACs[i] = &cp
			updated++
		}
		return nil
	})
	if err != nil {
		created, updated = 0, 0
	}
	return
}

func (s *Store) DeleteMAC(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.d.MACs, func(o *MACEntry) bool { return o.ID == id })
	if i < 0 {
		return ErrNotFound
	}
	return s.mutate(func() error {
		s.d.MACs = slices.Delete(slices.Clone(s.d.MACs), i, i+1)
		return nil
	})
}

func (s *Store) TouchMAC(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.d.MACs {
		if m.ID == id {
			m.LastSeen = time.Now()
			s.dirty = true
		}
	}
}

func normalizeAttrs(list []attrs.Attr) error {
	for i, a := range list {
		n, err := attrs.Normalize(a)
		if err != nil {
			return err
		}
		list[i] = n
	}
	return nil
}

// ---- Sicherung ----

// Export liefert die komplette Konfiguration im Klartext (für die verschlüsselte Sicherung).
func (s *Store) Export() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return json.MarshalIndent(&s.d, "", "  ")
}

// Import ersetzt die komplette Konfiguration (aus einer Sicherung).
func (s *Store) Import(b []byte) error {
	var d data
	if err := json.Unmarshal(b, &d); err != nil {
		return errors.New("Die Sicherung enthält keine gültige Konfiguration")
	}
	if len(d.Admins) == 0 && d.Settings.AdminHash == "" {
		return errors.New("Die Sicherung enthält kein Administratorkonto")
	}
	for _, u := range d.Users {
		if u == nil || u.ID == "" {
			return errors.New("Die Sicherung ist unvollständig")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(func() error {
		s.d = d
		s.migrate()
		return nil
	})
}
