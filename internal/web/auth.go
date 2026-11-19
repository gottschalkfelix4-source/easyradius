package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/gottschalkfelix4-source/easyradius/internal/events"
	"github.com/gottschalkfelix4-source/easyradius/internal/store"

	"golang.org/x/crypto/bcrypt"
)

func platform() string { return runtime.GOOS }

// Sitzungen liegen nur im Speicher des Servers. Das Cookie enthält eine zufällige
// Kennung; Abmelden, Passwortwechsel und Rollenänderung beenden Sitzungen sofort.
const (
	sessionIdle     = 2 * time.Hour
	sessionAbsolute = 12 * time.Hour
	maxSessions     = 2000
)

type session struct {
	adminID  string
	created  time.Time
	lastSeen time.Time
}

// principal ist das angemeldete Konto einer Anfrage.
type principal struct {
	name    string
	role    string
	adminID string
	token   string // Name des API-Tokens, falls per Token angemeldet
	sid     string
}

type ctxKey struct{}

func principalOf(r *http.Request) principal {
	p, _ := r.Context().Value(ctxKey{}).(principal)
	return p
}

func hasBearer(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func randomToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func tokenHash(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// authenticate prüft Sitzungscookie oder API-Token.
func (s *Server) authenticate(r *http.Request) principal {
	if hasBearer(r) {
		tok := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if t, ok := s.Store.TokenByHash(tokenHash(tok)); ok && tok != "" {
			return principal{name: t.CreatedBy, role: t.Role, token: t.Name}
		}
		return principal{}
	}
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return principal{}
	}
	sid := tokenHash(c.Value)
	now := time.Now()
	s.sessMu.Lock()
	ss := s.sessions[sid]
	if ss != nil && (now.Sub(ss.lastSeen) > sessionIdle || now.Sub(ss.created) > sessionAbsolute) {
		delete(s.sessions, sid)
		ss = nil
	}
	var adminID string
	var created time.Time
	if ss != nil {
		ss.lastSeen = now
		adminID, created = ss.adminID, ss.created
	}
	s.sessMu.Unlock()
	if ss == nil {
		return principal{}
	}
	a, ok := s.Store.AdminByID(adminID)
	if !ok || created.Before(a.SessionsValidFrom) {
		s.dropSession(sid)
		return principal{}
	}
	return principal{name: a.Username, role: a.Role, adminID: a.ID, sid: sid}
}

func roleAllows(have, need string) bool {
	if need == store.RoleViewer {
		return have == store.RoleViewer || have == store.RoleAdmin
	}
	return have == store.RoleAdmin
}

func (s *Server) requireRole(need string, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := s.authenticate(r)
		if p.name == "" && p.token == "" {
			fail(w, http.StatusUnauthorized, "Bitte anmelden")
			return
		}
		if !roleAllows(p.role, need) {
			fail(w, http.StatusForbidden, "Dein Konto darf nur lesen")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
	})
}

func (s *Server) newSession(w http.ResponseWriter, r *http.Request, a store.Admin) {
	raw := randomToken(32)
	now := time.Now()
	s.sessMu.Lock()
	if len(s.sessions) >= maxSessions { // älteste Sitzung verdrängen
		var oldK string
		var oldT time.Time
		for k, v := range s.sessions {
			if oldK == "" || v.lastSeen.Before(oldT) {
				oldK, oldT = k, v.lastSeen
			}
		}
		delete(s.sessions, oldK)
	}
	s.sessions[tokenHash(raw)] = &session{adminID: a.ID, created: now, lastSeen: now}
	s.sessMu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: raw, Path: "/", HttpOnly: true,
		Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
	s.Store.TouchAdmin(a.ID)
}

func (s *Server) dropSession(sid string) {
	s.sessMu.Lock()
	delete(s.sessions, sid)
	s.sessMu.Unlock()
}

func (s *Server) dropAdminSessions(adminID string) {
	s.sessMu.Lock()
	for k, v := range s.sessions {
		if v.adminID == adminID {
			delete(s.sessions, k)
		}
	}
	s.sessMu.Unlock()
}

func (s *Server) sessionJanitor() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
		}
		now := time.Now()
		s.sessMu.Lock()
		for k, v := range s.sessions {
			if now.Sub(v.lastSeen) > sessionIdle || now.Sub(v.created) > sessionAbsolute {
				delete(s.sessions, k)
			}
		}
		s.sessMu.Unlock()
		s.failMu.Lock()
		for ip, list := range s.fails {
			if len(list) == 0 || now.Sub(list[len(list)-1]) > 10*time.Minute {
				delete(s.fails, ip)
			}
		}
		s.failMu.Unlock()
	}
}

func validPassword(pw string) error {
	if len(pw) < 8 {
		return errors.New("Das Passwort muss mindestens 8 Zeichen lang sein")
	}
	if len(pw) > 72 { // bcrypt verarbeitet höchstens 72 Byte
		return errors.New("Das Passwort darf höchstens 72 Byte lang sein (Umlaute zählen doppelt)")
	}
	return nil
}

func hashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", errors.New("Passwort konnte nicht verarbeitet werden")
	}
	return string(h), nil
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Password string }
	if readJSON(r, &in) != nil {
		fail(w, 400, "Ungültige Eingabe")
		return
	}
	if !s.Store.NeedsSetup() {
		fail(w, 409, "Die Einrichtung ist bereits abgeschlossen")
		return
	}
	if err := validPassword(in.Password); err != nil {
		fail(w, 400, err.Error())
		return
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	a, err := s.Store.CreateFirstAdmin(in.Username, hash)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.Log.Add(events.Event{Kind: events.Audit, Admin: a.Username, ClientIP: clientIP(r), Message: "Administrator „" + a.Username + "“ eingerichtet"})
	s.newSession(w, r, a)
	ok(w)
}

// Ein fester Hash, damit unbekannte Konten genauso lange brauchen wie bekannte.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("easyradius-dummy-password"), bcrypt.DefaultCost)

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	now := time.Now()
	// Versuch vorab verbuchen – parallele Anfragen können die Grenze so nicht umgehen.
	s.failMu.Lock()
	var recent []time.Time
	for _, t := range s.fails[ip] {
		if now.Sub(t) < 10*time.Minute {
			recent = append(recent, t)
		}
	}
	blocked := len(recent) >= 8
	if !blocked {
		recent = append(recent, now)
	}
	if len(recent) == 0 {
		delete(s.fails, ip)
	} else if len(s.fails) < 10000 || s.fails[ip] != nil {
		s.fails[ip] = recent
	}
	s.failMu.Unlock()
	if blocked {
		fail(w, 429, "Zu viele Fehlversuche – bitte 10 Minuten warten")
		return
	}

	var in struct{ Username, Password string }
	if readJSON(r, &in) != nil {
		fail(w, 400, "Ungültige Eingabe")
		return
	}
	a, found := s.Store.FindAdmin(strings.TrimSpace(in.Username))
	hash := dummyHash
	if found {
		hash = []byte(a.Hash)
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(in.Password)) != nil || !found {
		time.Sleep(500 * time.Millisecond)
		s.Log.Add(events.Event{Kind: events.Warning, ClientIP: ip, User: in.Username, Message: "Fehlgeschlagene Anmeldung an der Weboberfläche"})
		fail(w, 401, "Benutzername oder Passwort falsch")
		return
	}
	// Erfolgreich: den vorab verbuchten Versuch wieder entfernen.
	s.failMu.Lock()
	if l := s.fails[ip]; len(l) > 0 {
		s.fails[ip] = l[:len(l)-1]
		if len(s.fails[ip]) == 0 {
			delete(s.fails, ip)
		}
	}
	s.failMu.Unlock()
	s.newSession(w, r, a)
	writeJSON(w, 200, map[string]any{"ok": true, "role": a.Role})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.dropSession(tokenHash(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
	ok(w)
}

// logoutAll beendet alle Sitzungen des eigenen Kontos (z. B. nach Verlust eines Geräts).
func (s *Server) logoutAll(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	if p.adminID == "" {
		fail(w, 400, "Mit einem API-Token nicht möglich")
		return
	}
	s.Store.RevokeSessions(p.adminID)
	s.dropAdminSessions(p.adminID)
	s.audit(r, "Überall abgemeldet")
	ok(w)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	writeJSON(w, 200, map[string]any{"user": p.name, "role": p.role, "token": p.token != ""})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct{ Current, New string }
	if readJSON(r, &in) != nil {
		fail(w, 400, "Ungültige Eingabe")
		return
	}
	p := principalOf(r)
	a, found := s.Store.AdminByID(p.adminID)
	if !found {
		fail(w, 400, "Mit einem API-Token nicht möglich")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(a.Hash), []byte(in.Current)) != nil {
		fail(w, 400, "Das aktuelle Passwort stimmt nicht")
		return
	}
	if err := validPassword(in.New); err != nil {
		fail(w, 400, err.Error())
		return
	}
	hash, err := hashPassword(in.New)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	a.Hash = hash
	if _, err := s.Store.SaveAdmin(a); err != nil {
		fail(w, 500, err.Error())
		return
	}
	// Alle anderen Sitzungen beenden, diese neu ausstellen.
	s.dropAdminSessions(a.ID)
	a, _ = s.Store.AdminByID(a.ID)
	s.newSession(w, r, a)
	s.audit(r, "Eigenes Passwort geändert")
	ok(w)
}

// ---- Administratoren ----

type adminOut struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
	LastLogin time.Time `json:"lastLogin"`
	Self      bool      `json:"self"`
}

func (s *Server) listAdmins(w http.ResponseWriter, r *http.Request) {
	me := principalOf(r).adminID
	out := []adminOut{}
	for _, a := range s.Store.Admins() {
		out = append(out, adminOut{ID: a.ID, Username: a.Username, Role: a.Role, CreatedAt: a.CreatedAt, LastLogin: a.LastLogin, Self: a.ID == me})
	}
	writeJSON(w, 200, out)
}

func (s *Server) saveAdmin(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Role, Password string }
	if readJSON(r, &in) != nil {
		fail(w, 400, "Ungültige Eingabe")
		return
	}
	a := store.Admin{ID: r.PathValue("id"), Username: in.Username, Role: in.Role}
	if a.ID != "" {
		old, found := s.Store.AdminByID(a.ID)
		if !found {
			fail(w, 404, "Konto nicht gefunden")
			return
		}
		if in.Role == "" {
			a.Role = old.Role
		}
	}
	if in.Password != "" || a.ID == "" {
		if err := validPassword(in.Password); err != nil {
			fail(w, 400, err.Error())
			return
		}
		h, err := hashPassword(in.Password)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		a.Hash = h
	}
	saved, err := s.Store.SaveAdmin(a)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if r.PathValue("id") != "" && (in.Password != "" || in.Role != "") {
		s.dropAdminSessions(saved.ID)
	}
	s.audit(r, "Konto „"+saved.Username+"“ ("+roleName(saved.Role)+") gespeichert")
	writeJSON(w, 200, adminOut{ID: saved.ID, Username: saved.Username, Role: saved.Role, CreatedAt: saved.CreatedAt})
}

func roleName(r string) string {
	if r == store.RoleViewer {
		return "nur lesen"
	}
	return "Administrator"
}

func (s *Server) deleteAdmin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, found := s.Store.AdminByID(id)
	if !found {
		fail(w, 404, "Konto nicht gefunden")
		return
	}
	if id == principalOf(r).adminID {
		fail(w, 400, "Das eigene Konto kann nicht gelöscht werden")
		return
	}
	if err := s.Store.DeleteAdmin(id); err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.dropAdminSessions(id)
	s.audit(r, "Konto „"+a.Username+"“ gelöscht")
	ok(w)
}

// ---- API-Tokens ----

type tokenOut struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Prefix    string    `json:"prefix"`
	Role      string    `json:"role"`
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
	LastUsed  time.Time `json:"lastUsed"`
	ExpiresAt string    `json:"expiresAt"`
	Token     string    `json:"token,omitempty"` // nur direkt nach dem Anlegen
}

func toTokenOut(t store.APIToken) tokenOut {
	return tokenOut{ID: t.ID, Name: t.Name, Prefix: t.Prefix, Role: t.Role, CreatedBy: t.CreatedBy,
		CreatedAt: t.CreatedAt, LastUsed: t.LastUsed, ExpiresAt: t.ExpiresAt}
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	out := []tokenOut{}
	for _, t := range s.Store.Tokens() {
		out = append(out, toTokenOut(t))
	}
	writeJSON(w, 200, out)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name, Role, ExpiresAt string }
	if readJSON(r, &in) != nil {
		fail(w, 400, "Ungültige Eingabe")
		return
	}
	if principalOf(r).token != "" {
		fail(w, 403, "Tokens können nur angemeldete Konten anlegen")
		return
	}
	raw := "er_" + randomToken(32)
	t, err := s.Store.AddToken(store.APIToken{Name: in.Name, Role: in.Role, ExpiresAt: in.ExpiresAt,
		Hash: tokenHash(raw), Prefix: raw[:10], CreatedBy: principalOf(r).name})
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.audit(r, "API-Token „"+t.Name+"“ ("+roleName(t.Role)+") angelegt")
	out := toTokenOut(t)
	out.Token = raw
	writeJSON(w, 200, out)
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := id
	for _, t := range s.Store.Tokens() {
		if t.ID == id {
			name = t.Name
		}
	}
	if err := s.Store.DeleteToken(id); err != nil {
		fail(w, 404, err.Error())
		return
	}
	s.audit(r, "API-Token „"+name+"“ gelöscht")
	ok(w)
}
