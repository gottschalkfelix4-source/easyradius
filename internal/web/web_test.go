package web

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gottschalkfelix4-source/easyradius/internal/certs"
	"github.com/gottschalkfelix4-source/easyradius/internal/events"
	"github.com/gottschalkfelix4-source/easyradius/internal/radiusd"
	"github.com/gottschalkfelix4-source/easyradius/internal/store"
)

type env struct {
	t   *testing.T
	srv *httptest.Server
	ws  *Server
}

func newEnv(t *testing.T) *env {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	lg, _ := events.Open(dir, 200)
	cm, err := certs.NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.UpdateSettings(func(c *store.Settings) { c.AuthPort, c.AcctPort = 18220, 18230 })
	rs := radiusd.New(st, lg, cm)
	ws := &Server{Store: st, Log: lg, Certs: cm, Radius: rs, Version: "test"}
	srv := httptest.NewServer(ws.Handler())
	t.Cleanup(func() { ws.Close(); srv.Close(); lg.Close() })
	return &env{t: t, srv: srv, ws: ws}
}

type client struct {
	e     *env
	h     *http.Client
	token string
}

func (e *env) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, h: &http.Client{Jar: jar}}
}

func (c *client) do(method, path string, body any) (int, map[string]any, []byte) {
	c.e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.e.srv.URL+path, rd)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	} else {
		req.Header.Set("X-EasyRadius", "1")
	}
	resp, err := c.h.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	json.Unmarshal(raw, &m)
	return resp.StatusCode, m, raw
}

func (c *client) upload(path string, fields map[string]string, file []byte) (int, map[string]any) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("file", "upload")
	fw.Write(file)
	mw.Close()
	req, _ := http.NewRequest("POST", c.e.srv.URL+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-EasyRadius", "1")
	resp, err := c.h.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

const adminPW = "Admin-Passwort-1"

func (e *env) admin() *client {
	c := e.client()
	if code, m, _ := c.do("POST", "/api/setup", map[string]string{"username": "chef", "password": adminPW}); code != 200 {
		e.t.Fatalf("setup: %d %v", code, m)
	}
	return c
}

func TestSetupLoginAndRoles(t *testing.T) {
	e := newEnv(t)
	a := e.admin()
	if code, _, _ := e.client().do("POST", "/api/setup", map[string]string{"username": "x", "password": "12345678"}); code != 409 {
		t.Fatal("zweite Einrichtung möglich")
	}
	// Passwort > 72 Byte wird abgelehnt statt still kaputt gespeichert.
	if code, _, _ := a.do("POST", "/api/admins", map[string]string{"username": "lang", "role": "admin", "password": strings.Repeat("ä", 40)}); code != 400 {
		t.Fatal("überlanges Passwort angenommen")
	}
	if code, m, _ := a.do("POST", "/api/admins", map[string]string{"username": "leser", "role": "viewer", "password": "Leser-Passwort"}); code != 200 {
		t.Fatalf("Leser anlegen: %v", m)
	}
	if code, _, _ := a.do("POST", "/api/admins", map[string]string{"username": "a|b", "role": "viewer", "password": "Leser-Passwort"}); code != 400 {
		t.Fatal("Name mit | angenommen")
	}
	v := e.client()
	if code, m, _ := v.do("POST", "/api/login", map[string]string{"username": "leser", "password": "Leser-Passwort"}); code != 200 || m["role"] != "viewer" {
		t.Fatalf("Login Leser: %d %v", code, m)
	}
	if code, _, _ := v.do("GET", "/api/users", nil); code != 200 {
		t.Fatal("Leser darf nicht lesen")
	}
	if code, _, _ := v.do("POST", "/api/users", map[string]any{"username": "x", "password": "y"}); code != 403 {
		t.Fatal("Leser darf schreiben")
	}
	// CSRF: ohne Header abgelehnt.
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/users", strings.NewReader("{}"))
	resp, _ := a.h.Do(req)
	if resp.StatusCode != 403 {
		t.Fatal("Schreiben ohne X-EasyRadius möglich")
	}
}

func TestPasswordChangeEndsOtherSessions(t *testing.T) {
	e := newEnv(t)
	a := e.admin()
	b := e.client()
	b.do("POST", "/api/login", map[string]string{"username": "chef", "password": adminPW})
	if code, _, _ := b.do("GET", "/api/me", nil); code != 200 {
		t.Fatal("zweite Sitzung ungültig")
	}
	if code, m, _ := a.do("POST", "/api/admin/password", map[string]string{"current": adminPW, "new": "Neues-Passwort-2"}); code != 200 {
		t.Fatalf("Passwort ändern: %v", m)
	}
	if code, _, _ := b.do("GET", "/api/me", nil); code != 401 {
		t.Fatal("andere Sitzung nach Passwortwechsel noch gültig")
	}
	if code, _, _ := a.do("GET", "/api/me", nil); code != 200 {
		t.Fatal("eigene Sitzung nach Passwortwechsel ungültig")
	}
	a.do("POST", "/api/logout", nil)
	if code, _, _ := a.do("GET", "/api/me", nil); code != 401 {
		t.Fatal("nach Abmelden noch angemeldet")
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	e.admin()
	c := e.client()
	code := 0
	for i := 0; i < 9; i++ {
		code, _, _ = c.do("POST", "/api/login", map[string]string{"username": "chef", "password": "falsch"})
	}
	if code != 429 {
		t.Fatalf("keine Bremse nach 9 Fehlversuchen: %d", code)
	}
}

func TestClientSecretHidden(t *testing.T) {
	e := newEnv(t)
	a := e.admin()
	_, m, _ := a.do("POST", "/api/clients", map[string]any{"name": "AP", "address": "10.0.0.5", "secret": "Geheimnis-123"})
	id, _ := m["id"].(string)
	if m["requireMA"] != true {
		t.Error("neues Gerät ohne Message-Authenticator-Pflicht")
	}
	_, _, raw := a.do("GET", "/api/clients", nil)
	if strings.Contains(string(raw), "Geheimnis-123") {
		t.Fatal("Secret steht in der Geräteliste")
	}
	_, m, _ = a.do("GET", "/api/clients/"+id+"/secret", nil)
	if m["secret"] != "Geheimnis-123" {
		t.Fatalf("Secret-Abruf: %v", m)
	}
}

func TestAPIToken(t *testing.T) {
	e := newEnv(t)
	a := e.admin()
	_, m, _ := a.do("POST", "/api/tokens", map[string]string{"name": "Skript", "role": "viewer"})
	tok, _ := m["token"].(string)
	if !strings.HasPrefix(tok, "er_") {
		t.Fatalf("Token: %v", m)
	}
	c := e.client()
	c.token = tok
	if code, _, _ := c.do("GET", "/api/users", nil); code != 200 {
		t.Fatal("Token wird nicht akzeptiert")
	}
	if code, _, _ := c.do("POST", "/api/groups", map[string]any{"name": "x"}); code != 403 {
		t.Fatal("Lese-Token darf schreiben")
	}
	c.token = tok + "x"
	if code, _, _ := c.do("GET", "/api/users", nil); code != 401 {
		t.Fatal("falsches Token akzeptiert")
	}
}

func TestCSVImportAndBackup(t *testing.T) {
	e := newEnv(t)
	a := e.admin()
	a.do("POST", "/api/groups", map[string]any{"name": "Büro", "enabled": true})
	csv := "\xef\xbb\xbfBenutzername;Passwort;Gruppe;VLAN\nanna;Pw-anna-1;Büro;10\nben;Pw-ben-1;;\n"
	if code, m := a.upload("/api/users/import", nil, []byte(csv)); code != 200 || m["created"] != float64(2) {
		t.Fatalf("Import: %d %v", code, m)
	}
	if code, m := a.upload("/api/users/import", nil, []byte("Benutzername;Gruppe\nanna;Gibtsnicht\n")); code != 400 || !strings.Contains(m["error"].(string), "Zeile 2") {
		t.Fatalf("fehlerhafte Gruppe: %d %v", code, m)
	}
	if code, m := a.upload("/api/macs/import", nil, []byte("MAC,Name\n00:11:22:33:44:55,Drucker\n")); code != 200 || m["created"] != float64(1) {
		t.Fatalf("MAC-Import: %d %v", code, m)
	}

	code, _, backup := a.do("POST", "/api/backup", map[string]string{"password": "Sicherung-1"})
	if code != 200 || !bytes.Contains(backup, []byte(backupMagic)) {
		t.Fatalf("Sicherung: %d", code)
	}
	a.do("DELETE", "/api/users/"+e.ws.Store.Users()[0].ID, nil)
	if code, _ := a.upload("/api/restore", map[string]string{"password": "falsch-falsch"}, backup); code != 400 {
		t.Fatal("Wiederherstellung mit falschem Passwort")
	}
	if code, m := a.upload("/api/restore", map[string]string{"password": "Sicherung-1"}, backup); code != 200 {
		t.Fatalf("Wiederherstellung: %v", m)
	}
	if len(e.ws.Store.Users()) != 2 || len(e.ws.Store.MACs()) != 1 {
		t.Fatal("Wiederherstellung unvollständig")
	}
	if code, _, _ := a.do("GET", "/api/me", nil); code != 401 {
		t.Fatal("Sitzungen nach Wiederherstellung noch gültig")
	}
}

func TestHTTPSOnly(t *testing.T) {
	e := newEnv(t)
	e.ws.Store.UpdateSettings(func(c *store.Settings) { c.HTTPSOnly = true })
	// Testanfragen kommen von 127.0.0.1 und dürfen weiter per HTTP.
	if code, _, _ := e.client().do("GET", "/api/state", nil); code != 200 {
		t.Fatal("localhost per HTTP gesperrt")
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.0.2.10:5000"
	req.Host = "radius.local:8088"
	rec := httptest.NewRecorder()
	e.ws.securityHeaders(http.NotFoundHandler()).ServeHTTP(rec, req)
	if rec.Code != 307 || !strings.HasPrefix(rec.Header().Get("Location"), "https://radius.local:8443/") {
		t.Fatalf("keine Weiterleitung: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	req = httptest.NewRequest("GET", "/einrichten", nil)
	req.RemoteAddr = "192.0.2.10:5000"
	rec = httptest.NewRecorder()
	e.ws.securityHeaders(http.NotFoundHandler()).ServeHTTP(rec, req)
	if rec.Code == 307 {
		t.Fatal("Einrichtungsseite umgeleitet")
	}
}
