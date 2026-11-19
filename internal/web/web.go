// Package web stellt die Verwaltungsoberfläche und ihre JSON-API bereit.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gottschalkfelix4-source/easyradius/internal/certs"
	"github.com/gottschalkfelix4-source/easyradius/internal/events"
	"github.com/gottschalkfelix4-source/easyradius/internal/letsencrypt"
	"github.com/gottschalkfelix4-source/easyradius/internal/radiusd"
	"github.com/gottschalkfelix4-source/easyradius/internal/store"
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
	fails  map[string][]time.Time // Fehlversuche je IP

	sessMu   sync.Mutex
	sessions map[string]*session

	done     chan struct{} // geschlossen beim Beenden (beendet Live-Protokolle)
	doneOnce sync.Once
}

const cookieName = "er_session"

// Grenzen für Anfragen.
const (
	maxBody       = 1 << 20  // JSON
	maxUploadBody = 32 << 20 // Sicherung, CSV, Zertifikate
)

// Öffentliche Pfade, die auch bei „nur HTTPS“ per HTTP erreichbar bleiben
// (Einrichtungsseite für Handys, CA-Zertifikat).
var publicHTTP = map[string]bool{
	"/einrichten": true, "/ca.crt": true, "/onboarding.js": true, "/style.css": true, "/favicon.svg": true,
}

func (s *Server) Handler() http.Handler {
	s.fails = map[string][]time.Time{}
	s.sessions = map[string]*session{}
	if s.done == nil {
		s.done = make(chan struct{})
	}
	go s.sessionJanitor()

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

	// view: alle angemeldeten Konten (auch „nur lesen“); admin: nur Administratoren.
	view := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.requireRole(store.RoleViewer, h)) }
	admin := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.requireRole(store.RoleAdmin, h)) }

	view("GET /api/dashboard", s.dashboard)
	view("GET /api/me", s.me)
	view("POST /api/admin/password", s.changePassword) // eigenes Passwort
	view("POST /api/logout-all", s.logoutAll)

	view("GET /api/users", s.listUsers)
	admin("POST /api/users", s.saveUser)
	admin("PUT /api/users/{id}", s.saveUser)
	admin("DELETE /api/users/{id}", s.deleteUser)
	admin("POST /api/users/test", s.testLogin)
	view("GET /api/users.csv", s.exportUsersCSV)
	admin("POST /api/users/import", s.importUsersCSV)

	view("GET /api/groups", s.listGroups)
	admin("POST /api/groups", s.saveGroup)
	admin("PUT /api/groups/{id}", s.saveGroup)
	admin("DELETE /api/groups/{id}", s.deleteGroup)

	view("GET /api/macs", s.listMACs)
	admin("POST /api/macs", s.saveMAC)
	admin("PUT /api/macs/{id}", s.saveMAC)
	admin("DELETE /api/macs/{id}", s.deleteMAC)
	view("GET /api/macs.csv", s.exportMACsCSV)
	admin("POST /api/macs/import", s.importMACsCSV)

	view("GET /api/clients", s.listClients)
	admin("GET /api/clients/{id}/secret", s.clientSecret)
	admin("POST /api/clients", s.saveClient)
	admin("PUT /api/clients/{id}", s.saveClient)
	admin("DELETE /api/clients/{id}", s.deleteClient)

	view("GET /api/online", s.listOnline)
	admin("POST /api/online/disconnect", s.disconnect)
	view("GET /api/accounting", s.listAccounting)
	view("GET /api/accounting.csv", s.exportAccountingCSV)
	view("GET /api/lockouts", s.listLockouts)
	admin("POST /api/lockouts/unlock", s.unlock)

	view("GET /api/ad", s.getAD)
	admin("PUT /api/ad", s.putAD)
	view("GET /api/acme", s.getACME)
	admin("PUT /api/acme", s.putACME)
	admin("POST /api/acme/run", s.runACME)
	view("GET /api/acme/providers", s.acmeProviders)
	view("GET /api/attributes", s.attributeDictionary)
	view("GET /api/qr.png", s.qrPNG)

	view("GET /api/events", s.listEvents)
	view("GET /api/events/stream", s.streamEvents)
	view("GET /api/settings", s.getSettings)
	admin("PUT /api/settings", s.putSettings)

	view("GET /api/cert", s.certInfo)
	admin("POST /api/cert/generate", s.certGenerate)
	admin("POST /api/cert/upload", s.certUpload)
	admin("POST /api/cert/own", s.certOwnCA)
	view("GET /api/cert/ca.crt", s.certDownload)
	s.clientCertRoutes(view, admin)

	admin("GET /api/admins", s.listAdmins)
	admin("POST /api/admins", s.saveAdmin)
	admin("PUT /api/admins/{id}", s.saveAdmin)
	admin("DELETE /api/admins/{id}", s.deleteAdmin)
	admin("GET /api/tokens", s.listTokens)
	admin("POST /api/tokens", s.createToken)
	admin("DELETE /api/tokens/{id}", s.deleteToken)

	admin("POST /api/backup", s.backup)
	admin("POST /api/restore", s.restore)

	return s.securityHeaders(mux)
}

// Close beendet laufende Live-Verbindungen (vor dem Herunterfahren).
func (s *Server) Close() {
	if s.done == nil {
		return
	}
	s.doneOnce.Do(func() { close(s.done) })
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

func ok(w http.ResponseWriter) { writeJSON(w, 200, map[string]bool{"ok": true}) }

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(v)
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

func isLoopback(r *http.Request) bool {
	ip := net.ParseIP(clientIP(r))
	return ip != nil && ip.IsLoopback()
}

// httpsURL baut die HTTPS-Adresse für eine Weiterleitung.
func (s *Server) httpsURL(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := httpsPort(s.Store.Settings().HTTPSAddr)
	if port != "443" {
		host += ":" + port
	}
	return "https://" + host + r.URL.RequestURI()
}

func httpsPort(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i+1:]
	}
	return "8443"
}

func (s *Server) securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'")
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}

		// Aus dem Netz nur HTTPS (falls eingestellt) – die Einrichtungsseite bleibt erreichbar.
		cfg := s.Store.Settings()
		if r.TLS == nil && cfg.HTTPSOnly && cfg.HTTPSAddr != "" && !isLoopback(r) && !publicHTTP[r.URL.Path] {
			if r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/api/") {
				http.Redirect(w, r, s.httpsURL(r), http.StatusTemporaryRedirect)
				return
			}
			fail(w, http.StatusForbidden, "Bitte die Weboberfläche per HTTPS öffnen: "+s.httpsURL(&http.Request{Host: r.Host, URL: r.URL}))
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/") {
			limit := int64(maxBody)
			if strings.HasSuffix(r.URL.Path, "/import") || r.URL.Path == "/api/restore" || r.URL.Path == "/api/cert/upload" {
				limit = maxUploadBody
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			// Schutz vor CSRF: schreibende Aufrufe mit Cookie brauchen unseren Header.
			// API-Tokens (Authorization: Bearer) senden keine Cookies und sind ausgenommen.
			if r.Method != http.MethodGet && r.Header.Get("X-EasyRadius") != "1" && !hasBearer(r) {
				fail(w, http.StatusForbidden, "Ungültige Anfrage")
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// audit schreibt eine Änderung ins Protokoll – mit Konto und Herkunft.
func (s *Server) audit(r *http.Request, msg string) {
	p := principalOf(r)
	who := p.name
	if p.token != "" {
		who = "Token „" + p.token + "“"
	}
	s.Log.Add(events.Event{Kind: events.Audit, Admin: who, ClientIP: clientIP(r), Message: msg})
}

// ---- Status für die Startseite ----

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	cfg := s.Store.Settings()
	p := s.authenticate(r)
	writeJSON(w, 200, map[string]any{
		"setup":     s.Store.NeedsSetup(),
		"user":      p.name,
		"role":      p.role,
		"version":   s.Version,
		"platform":  platform(),
		"server":    cfg.ServerName,
		"secure":    r.TLS != nil,
		"loopback":  isLoopback(r),
		"httpsPort": httpsPort(cfg.HTTPSAddr),
		"httpsOn":   cfg.HTTPSAddr != "",
	})
}

// ---- Übersicht ----

func localIPs() []string {
	out := []string{}
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
		"macs":        len(s.Store.MACs()),
		"adMode":      s.Store.Settings().AD.Mode,
		"stats":       s.Log.Stats(time.Now()),
		"online":      s.Radius.Online(),
		"unknown":     s.Radius.UnknownClients(),
		"lockouts":    len(s.Radius.Lockouts()),
		"recent":      s.Log.Recent(8),
		"cert":        s.Certs.Info(),
	})
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
	// Das Live-Protokoll darf länger laufen als die normale Schreib-Zeitgrenze.
	http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch := s.Log.Subscribe()
	defer s.Log.Unsubscribe(ch)
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	fmt.Fprint(w, ": ok\n\n")
	fl.Flush()
	sessionCheck := time.NewTicker(time.Minute)
	defer sessionCheck.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.done:
			return
		case <-sessionCheck.C:
			if s.authenticate(r).name == "" { // abgemeldet oder Passwort geändert
				return
			}
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
		"authPort": c.AuthPort, "acctPort": c.AcctPort, "webAddr": c.WebAddr, "httpsAddr": c.HTTPSAddr,
		"httpsOnly": c.HTTPSOnly, "serverName": c.ServerName, "adminUser": principalOf(r).name,
		"logRetention": c.LogRetention, "lockout": c.Lockout, "eap": c.EAP,
	})
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AuthPort, AcctPort int
		ServerName         string
		HTTPSOnly          *bool
		LogRetention       *int
		Lockout            *store.LockoutSettings
		EAP                *store.EAPSettings
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
	if len(in.ServerName) > 64 {
		fail(w, 400, "Servername zu lang (max. 64 Zeichen)")
		return
	}
	if in.Lockout != nil && in.Lockout.Enabled && (in.Lockout.Attempts < 3 || in.Lockout.Attempts > 100 || in.Lockout.Minutes < 1 || in.Lockout.Minutes > 1440) {
		fail(w, 400, "Sperre: 3–100 Fehlversuche und 1–1440 Minuten")
		return
	}
	if in.LogRetention != nil && (*in.LogRetention < 100 || *in.LogRetention > 100000) {
		fail(w, 400, "Protokoll: 100 bis 100000 Einträge")
		return
	}
	if in.HTTPSOnly != nil && *in.HTTPSOnly && r.TLS == nil && !isLoopback(r) {
		fail(w, 400, "„Nur HTTPS“ bitte über eine HTTPS-Verbindung einschalten – sonst sperrst du dich womöglich aus")
		return
	}
	old := s.Store.Settings()
	apply := func(c *store.Settings) {
		c.AuthPort, c.AcctPort, c.ServerName = in.AuthPort, in.AcctPort, in.ServerName
		if in.HTTPSOnly != nil {
			c.HTTPSOnly = *in.HTTPSOnly
		}
		if in.LogRetention != nil {
			c.LogRetention = *in.LogRetention
		}
		if in.Lockout != nil {
			c.Lockout = *in.Lockout
		}
		if in.EAP != nil {
			c.EAP = *in.EAP
		}
	}
	if err := s.Store.UpdateSettings(apply); err != nil {
		fail(w, 500, err.Error())
		return
	}
	if old.AuthPort != in.AuthPort || old.AcctPort != in.AcctPort {
		if err := s.Radius.Restart(); err != nil {
			// Neue Ports gehen nicht – alte wiederherstellen, damit RADIUS weiterläuft.
			s.Store.UpdateSettings(func(c *store.Settings) { c.AuthPort, c.AcctPort = old.AuthPort, old.AcctPort })
			s.Radius.Restart()
			fail(w, 400, err.Error()+" – die bisherigen Ports bleiben aktiv")
			return
		}
	}
	if in.LogRetention != nil {
		s.Log.SetMax(*in.LogRetention)
	}
	s.audit(r, "Einstellungen geändert")
	ok(w)
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
	s.Store.UpdateSettings(func(c *store.Settings) { c.ACME.Enabled = false })
	s.audit(r, "Neue Zertifizierungsstelle und neues Zertifikat erzeugt")
	writeJSON(w, 200, s.Certs.Info())
}

func (s *Server) certUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(4 << 20); err != nil {
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
	s.Store.UpdateSettings(func(c *store.Settings) { c.ACME.Enabled = false })
	s.audit(r, "Eigenes Zertifikat importiert")
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

// Shutdown hilft main beim geordneten Beenden mehrerer HTTP-Server.
func Shutdown(ctx context.Context, servers ...*http.Server) {
	var wg sync.WaitGroup
	for _, hs := range servers {
		if hs == nil {
			continue
		}
		wg.Add(1)
		go func(hs *http.Server) {
			defer wg.Done()
			if err := hs.Shutdown(ctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
				hs.Close()
			}
		}(hs)
	}
	wg.Wait()
}
