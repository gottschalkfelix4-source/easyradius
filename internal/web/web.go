// Package web stellt die Verwaltungsoberfläche und ihre JSON-API bereit.
package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gottschalkfelix4-source/easyradius/internal/auth"
	"github.com/gottschalkfelix4-source/easyradius/internal/certs"
	"github.com/gottschalkfelix4-source/easyradius/internal/events"
	"github.com/gottschalkfelix4-source/easyradius/internal/letsencrypt"
	"github.com/gottschalkfelix4-source/easyradius/internal/radiusd"
	"github.com/gottschalkfelix4-source/easyradius/internal/store"

	"golang.org/x/crypto/bcrypt"
)

//go:embed static
var staticFS embed.FS

type Server struct {
	Store   *store.Store
	Log     *events.Log
	Certs   *certs.Manager
	Radius  *radiusd.Server
	ACME    *letsencrypt.Service
	Version string

	failMu sync.Mutex
	fails  map[string][]time.Time
}

const cookieName = "er_session"

func (s *Server) Handler() http.Handler {
	s.fails = map[string][]time.Time{}
	mux := http.NewServeMux()
	sub, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /", noCache(http.FileServer(http.FS(sub))))

	mux.HandleFunc("GET /einrichten", s.onboarding)
	mux.HandleFunc("GET /onboarding.js", s.onboardingScript)
	mux.HandleFunc("GET /ca.crt", s.publicCA)
	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("POST /api/setup", s.setup)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)

	a := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.requireAuth(h)) }
	a("GET /api/dashboard", s.dashboard)
	a("GET /api/users", s.listUsers)
	a("POST /api/users", s.saveUser)
	a("PUT /api/users/{id}", s.saveUser)
	a("DELETE /api/users/{id}", s.deleteUser)
	a("POST /api/users/test", s.testLogin)
	a("GET /api/groups", s.listGroups)
	a("POST /api/groups", s.saveGroup)
	a("PUT /api/groups/{id}", s.saveGroup)
	a("DELETE /api/groups/{id}", s.deleteGroup)
	a("GET /api/ad", s.getAD)
	a("PUT /api/ad", s.putAD)
	a("GET /api/acme", s.getACME)
	a("PUT /api/acme", s.putACME)
	a("POST /api/acme/run", s.runACME)
	a("GET /api/acme/providers", s.acmeProviders)
	a("GET /api/attributes", s.attributeDictionary)
	a("POST /api/cert/own", s.certOwnCA)
	a("GET /api/qr.png", s.qrPNG)
	a("GET /api/clients", s.listClients)
	a("POST /api/clients", s.saveClient)
	a("PUT /api/clients/{id}", s.saveClient)
	a("DELETE /api/clients/{id}", s.deleteClient)
	a("GET /api/events", s.listEvents)
	a("GET /api/events/stream", s.streamEvents)
	a("GET /api/settings", s.getSettings)
	a("PUT /api/settings", s.putSettings)
	a("POST /api/admin/password", s.changePassword)
	a("GET /api/cert", s.certInfo)
	a("POST /api/cert/generate", s.certGenerate)
	a("POST /api/cert/upload", s.certUpload)
	a("GET /api/cert/ca.crt", s.certDownload)
	return securityHeaders(mux)
}

// ---- Hilfsfunktionen ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")
		// Schutz vor CSRF: schreibende API-Aufrufe brauchen unseren Header.
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Method != http.MethodGet && r.Header.Get("X-EasyRadius") != "1" {
			fail(w, http.StatusForbidden, "Ungültige Anfrage")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// ---- Sitzung ----

func (s *Server) sign(payload string) string {
	m := hmac.New(sha256.New, []byte(s.Store.Settings().SessionKey))
	m.Write([]byte(payload))
	return payload + "|" + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Server) sessionUser(r *http.Request) string {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return ""
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return ""
	}
	if !hmac.Equal([]byte(s.sign(parts[0]+"|"+parts[1])), raw) {
		return ""
	}
	exp, _ := strconv.ParseInt(parts[1], 10, 64)
	if time.Now().Unix() > exp {
		return ""
	}
	cfg := s.Store.Settings()
	if parts[0] != cfg.AdminUser {
		return ""
	}
	return parts[0]
}

func (s *Server) setSession(w http.ResponseWriter, user string) {
	exp := time.Now().Add(12 * time.Hour)
	val := base64.RawURLEncoding.EncodeToString([]byte(s.sign(user + "|" + strconv.FormatInt(exp.Unix(), 10))))
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: val, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: exp})
}

func (s *Server) requireAuth(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.sessionUser(r) == "" {
			fail(w, http.StatusUnauthorized, "Bitte anmelden")
			return
		}
		h(w, r)
	})
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	cfg := s.Store.Settings()
	writeJSON(w, 200, map[string]any{
		"setup":    cfg.AdminHash == "",
		"user":     s.sessionUser(r),
		"version":  s.Version,
		"platform": runtime.GOOS,
		"server":   cfg.ServerName,
	})
}

func validPassword(pw string) error {
	if len(pw) < 8 {
		return errors.New("Das Passwort muss mindestens 8 Zeichen lang sein")
	}
	return nil
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Password string }
	if readJSON(r, &in) != nil {
		fail(w, 400, "Ungültige Eingabe")
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" {
		fail(w, 400, "Benutzername fehlt")
		return
	}
	if err := validPassword(in.Password); err != nil {
		fail(w, 400, err.Error())
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	var already bool
	s.Store.UpdateSettings(func(c *store.Settings) {
		if c.AdminHash != "" {
			already = true
			return
		}
		c.AdminUser, c.AdminHash = in.Username, string(hash)
	})
	if already {
		fail(w, 409, "Die Einrichtung ist bereits abgeschlossen")
		return
	}
	s.Log.Add(events.Event{Kind: events.Info, Message: "Administrator „" + in.Username + "“ eingerichtet"})
	s.setSession(w, in.Username)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	s.failMu.Lock()
	recent := s.fails[ip][:0]
	for _, t := range s.fails[ip] {
		if time.Since(t) < 10*time.Minute {
			recent = append(recent, t)
		}
	}
	s.fails[ip] = recent
	blocked := len(recent) >= 8
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
	cfg := s.Store.Settings()
	if cfg.AdminHash == "" || !strings.EqualFold(in.Username, cfg.AdminUser) ||
		bcrypt.CompareHashAndPassword([]byte(cfg.AdminHash), []byte(in.Password)) != nil {
		s.failMu.Lock()
		s.fails[ip] = append(s.fails[ip], time.Now())
		s.failMu.Unlock()
		time.Sleep(700 * time.Millisecond)
		s.Log.Add(events.Event{Kind: events.Warning, ClientIP: ip, Message: "Fehlgeschlagene Anmeldung an der Weboberfläche"})
		fail(w, 401, "Benutzername oder Passwort falsch")
		return
	}
	s.setSession(w, cfg.AdminUser)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct{ Current, New string }
	if readJSON(r, &in) != nil {
		fail(w, 400, "Ungültige Eingabe")
		return
	}
	cfg := s.Store.Settings()
	if bcrypt.CompareHashAndPassword([]byte(cfg.AdminHash), []byte(in.Current)) != nil {
		fail(w, 400, "Das aktuelle Passwort stimmt nicht")
		return
	}
	if err := validPassword(in.New); err != nil {
		fail(w, 400, err.Error())
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(in.New), bcrypt.DefaultCost)
	s.Store.UpdateSettings(func(c *store.Settings) { c.AdminHash = string(hash) })
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---- Übersicht ----

func localIPs() []string {
	var out []string
	ifaces, _ := net.Interfaces()
	for _, i := range ifaces {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				if v4 := n.IP.To4(); v4 != nil && !v4.IsLinkLocalUnicast() {
					out = append(out, v4.String())
				}
			}
		}
	}
	return out
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	users := s.Store.Users()
	active := 0
	for _, u := range users {
		if u.Enabled && !u.Expired(time.Now()) {
			active++
		}
	}
	writeJSON(w, 200, map[string]any{
		"status":      s.Radius.Status(),
		"ips":         localIPs(),
		"users":       len(users),
		"activeUsers": active,
		"clients":     len(s.Store.Clients()),
		"groups":      len(s.Store.Groups()),
		"adMode":      s.Store.Settings().AD.Mode,
		"stats":       s.Log.Stats(time.Now()),
		"online":      s.Radius.Online(),
		"unknown":     s.Radius.UnknownClients(),
		"recent":      s.Log.Recent(8),
		"cert":        s.Certs.Info(),
	})
}

// ---- Benutzer ----

type userOut struct {
	store.User
	NTHash  string `json:"ntHash,omitempty"`
	Expired bool   `json:"expired"`
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	out := []userOut{}
	for _, u := range s.Store.Users() {
		out = append(out, userOut{User: u, Expired: u.Expired(time.Now())})
	}
	writeJSON(w, 200, out)
}

func (s *Server) saveUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		store.User
		Password string `json:"password"`
	}
	if readJSON(r, &in) != nil {
		fail(w, 400, "Ungültige Eingabe")
		return
	}
	u := in.User
	u.ID = r.PathValue("id")
	u.NTHash = ""
	if in.Password != "" {
		u.NTHash = hex.EncodeToString(auth.NTHash(in.Password))
	}
	if u.VLAN != "" {
		if n, err := strconv.Atoi(u.VLAN); err != nil || n < 1 || n > 4094 {
			fail(w, 400, "VLAN muss eine Zahl zwischen 1 und 4094 sein")
			return
		}
	}
	if u.ExpiresAt != "" {
		if _, err := time.Parse("2006-01-02", u.ExpiresAt); err != nil {
			fail(w, 400, "Ablaufdatum ungültig")
			return
		}
	}
	if u.GroupID != "" {
		if _, ok := s.Store.Group(u.GroupID); !ok {
			fail(w, 400, "Gruppe nicht gefunden")
			return
		}
	}
	saved, err := s.Store.SaveUser(u)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	saved.NTHash = ""
	writeJSON(w, 200, saved)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteUser(r.PathValue("id")); err != nil {
		fail(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---- Geräte ----

func (s *Server) listClients(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Store.Clients())
}

func (s *Server) saveClient(w http.ResponseWriter, r *http.Request) {
	var c store.Client
	if readJSON(r, &c) != nil {
		fail(w, 400, "Ungültige Eingabe")
		return
	}
	c.ID = r.PathValue("id")
	saved, err := s.Store.SaveClient(c)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, saved)
}

func (s *Server) deleteClient(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteClient(r.PathValue("id")); err != nil {
		fail(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---- Protokoll ----

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n <= 0 || n > 5000 {
		n = 500
	}
	writeJSON(w, 200, s.Log.Recent(n))
}

func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "Streaming nicht möglich")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch := s.Log.Subscribe()
	defer s.Log.Unsubscribe(ch)
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	fmt.Fprint(w, ": ok\n\n")
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-ch:
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

// ---- Einstellungen ----

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	c := s.Store.Settings()
	writeJSON(w, 200, map[string]any{
		"authPort": c.AuthPort, "acctPort": c.AcctPort, "webAddr": c.WebAddr,
		"serverName": c.ServerName, "adminUser": c.AdminUser,
	})
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AuthPort, AcctPort int
		ServerName         string
	}
	if readJSON(r, &in) != nil {
		fail(w, 400, "Ungültige Eingabe")
		return
	}
	if in.AuthPort < 1 || in.AuthPort > 65535 || in.AcctPort < 1 || in.AcctPort > 65535 || in.AuthPort == in.AcctPort {
		fail(w, 400, "Ungültige Ports")
		return
	}
	in.ServerName = strings.TrimSpace(in.ServerName)
	if in.ServerName == "" {
		in.ServerName = "EasyRadius"
	}
	old := s.Store.Settings()
	s.Store.UpdateSettings(func(c *store.Settings) {
		c.AuthPort, c.AcctPort, c.ServerName = in.AuthPort, in.AcctPort, in.ServerName
	})
	if old.AuthPort != in.AuthPort || old.AcctPort != in.AcctPort {
		if err := s.Radius.Restart(); err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---- Zertifikat ----

func (s *Server) certInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Certs.Info())
}

func (s *Server) certGenerate(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name string }
	readJSON(r, &in)
	if err := s.Certs.Generate(strings.TrimSpace(in.Name)); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.Log.Add(events.Event{Kind: events.Info, Message: "Neues Zertifikat erzeugt"})
	writeJSON(w, 200, s.Certs.Info())
}

func (s *Server) certUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		fail(w, 400, "Upload fehlgeschlagen")
		return
	}
	read := func(name string) []byte {
		f, _, err := r.FormFile(name)
		if err != nil {
			return nil
		}
		defer f.Close()
		b, _ := io.ReadAll(io.LimitReader(f, 1<<20))
		return b
	}
	cert, key := read("cert"), read("key")
	if cert == nil || key == nil {
		fail(w, 400, "Bitte Zertifikat und Schlüssel auswählen (PEM-Format)")
		return
	}
	if err := s.Certs.Import(cert, key); err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.Log.Add(events.Event{Kind: events.Info, Message: "Eigenes Zertifikat importiert"})
	writeJSON(w, 200, s.Certs.Info())
}

func (s *Server) certDownload(w http.ResponseWriter, r *http.Request) {
	b, err := s.Certs.ActiveCACertPEM()
	if err != nil {
		fail(w, 404, "Das aktive Zertifikat stammt nicht von der eigenen Zertifizierungsstelle")
		return
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="EasyRadius-CA.crt"`)
	w.Write(b)
}
