// Package store speichert Benutzer, Geräte und Einstellungen in einer JSON-Datei.
package store

import (
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

type Settings struct {
	AuthPort     int    `json:"authPort"`
	AcctPort     int    `json:"acctPort"`
	WebAddr      string `json:"webAddr"`
	ServerName   string `json:"serverName"`
	AdminUser    string `json:"adminUser"`
	AdminHash    string `json:"adminHash"` // bcrypt
	SessionKey   string `json:"sessionKey"`
	LogRetention int    `json:"logRetention"` // Anzahl Einträge

	AD   ADSettings   `json:"ad"`
	ACME ACMESettings `json:"acme"`
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
	Settings Settings  `json:"settings"`
	Users    []*User   `json:"users"`
	Clients  []*Client `json:"clients"`
	Groups   []*Group  `json:"groups"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	d    data
}

var ErrNotFound = errors.New("nicht gefunden")

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "config.json")}
	b, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.d.Settings = Settings{AuthPort: 1812, AcctPort: 1813, WebAddr: ":8088", ServerName: "EasyRadius", LogRetention: 5000}
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &s.d); err != nil {
			return nil, fmt.Errorf("config.json beschädigt: %w", err)
		}
	}
	if s.d.Settings.SessionKey == "" {
		s.d.Settings.SessionKey = NewID() + NewID()
	}
	if s.d.Settings.LogRetention == 0 {
		s.d.Settings.LogRetention = 5000
	}
	if s.d.Settings.AD.Mode == "" {
		s.d.Settings.AD.Mode = "off"
		s.d.Settings.AD.RequireGroup = true
		s.d.Settings.AD.UserAttribute = "sAMAccountName"
	}
	return s, s.saveLocked()
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(&s.d, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
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
	return s.d.Settings
}

func (s *Store) UpdateSettings(fn func(*Settings)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.d.Settings)
	return s.saveLocked()
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

func (s *Store) SaveUser(u User) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u.Username = strings.TrimSpace(u.Username)
	if u.Username == "" {
		return u, errors.New("Benutzername fehlt")
	}
	for _, o := range s.d.Users {
		if o.ID != u.ID && strings.EqualFold(o.Username, u.Username) {
			return u, errors.New("Benutzername ist bereits vergeben")
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
		s.d.Users = append(s.d.Users, &u)
		return u, s.saveLocked()
	}
	for i, o := range s.d.Users {
		if o.ID == u.ID {
			if u.NTHash == "" {
				u.NTHash = o.NTHash
			}
			u.CreatedAt, u.LastLoginAt = o.CreatedAt, o.LastLoginAt
			s.d.Users[i] = &u
			return u, s.saveLocked()
		}
	}
	return u, ErrNotFound
}

func (s *Store) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, u := range s.d.Users {
		if u.ID == id {
			s.d.Users = append(s.d.Users[:i], s.d.Users[i+1:]...)
			return s.saveLocked()
		}
	}
	return ErrNotFound
}

// TouchUser merkt sich die letzte erfolgreiche Anmeldung (nur im Speicher,
// gesichert beim nächsten Speichern – vermeidet Schreibzugriffe pro Login).
func (s *Store) TouchUser(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.d.Users {
		if strings.EqualFold(u.Username, username) {
			u.LastLoginAt = time.Now()
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
		if c.ID == id {
			c.LastSeen = time.Now()
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
	if c.ID == "" {
		if c.Secret == "" {
			return c, errors.New("Shared Secret fehlt")
		}
		c.ID = NewID()
		c.CreatedAt = time.Now()
		s.d.Clients = append(s.d.Clients, &c)
		return c, s.saveLocked()
	}
	for i, o := range s.d.Clients {
		if o.ID == c.ID {
			if c.Secret == "" {
				c.Secret = o.Secret
			}
			c.CreatedAt, c.LastSeen = o.CreatedAt, o.LastSeen
			s.d.Clients[i] = &c
			return c, s.saveLocked()
		}
	}
	return c, ErrNotFound
}

func (s *Store) DeleteClient(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.d.Clients {
		if c.ID == id {
			s.d.Clients = append(s.d.Clients[:i], s.d.Clients[i+1:]...)
			for _, g := range s.d.Groups {
				g.AllowedClients = slices.DeleteFunc(g.AllowedClients, func(x string) bool { return x == id })
			}
			return s.saveLocked()
		}
	}
	return ErrNotFound
}

// Flush schreibt In-Memory-Änderungen (z. B. letzte Logins) auf die Platte.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
		s.d.Groups = append(s.d.Groups, &g)
		return g, s.saveLocked()
	}
	for i, o := range s.d.Groups {
		if o.ID == g.ID {
			g.CreatedAt = o.CreatedAt
			s.d.Groups[i] = &g
			return g, s.saveLocked()
		}
	}
	return g, ErrNotFound
}

// DeleteGroup entfernt die Gruppe und löst die Zuordnung der Benutzer.
func (s *Store) DeleteGroup(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, g := range s.d.Groups {
		if g.ID == id {
			s.d.Groups = append(s.d.Groups[:i], s.d.Groups[i+1:]...)
			for _, u := range s.d.Users {
				if u.GroupID == id {
					u.GroupID = ""
				}
			}
			return s.saveLocked()
		}
	}
	return ErrNotFound
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
