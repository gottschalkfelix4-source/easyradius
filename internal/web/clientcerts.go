package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gottschalkfelix4-source/easyradius/internal/certs"
)

// clientCertRoutes registriert die Endpunkte für Benutzerzertifikate (EAP-TLS).
func (s *Server) clientCertRoutes(view, admin func(string, http.HandlerFunc)) {
	view("GET /api/clientcerts", s.listClientCerts)
	admin("POST /api/users/{id}/certificate", s.issueClientCert)
	admin("POST /api/clientcerts/{serial}/revoke", s.revokeClientCert)
}

func (s *Server) listClientCerts(w http.ResponseWriter, r *http.Request) {
	list := s.Certs.ClientCerts()
	if list == nil {
		list = []certs.ClientCertInfo{}
	}
	writeJSON(w, 200, list)
}

func (s *Server) issueClientCert(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Days     int
		Password string
	}
	if readJSON(r, &in) != nil {
		fail(w, 400, "Ungültige Eingabe")
		return
	}
	var name string
	for _, u := range s.Store.Users() {
		if u.ID == r.PathValue("id") {
			name = u.Username
		}
	}
	if name == "" {
		fail(w, 404, "Benutzer nicht gefunden")
		return
	}
	if in.Days < 1 || in.Days > 3650 {
		fail(w, 400, "Laufzeit: 1 bis 3650 Tage")
		return
	}
	if len(in.Password) < 6 {
		fail(w, 400, "Bitte ein Passwort mit mindestens 6 Zeichen für die Zertifikatsdatei wählen")
		return
	}
	p12, serial, err := s.Certs.IssueClientCert(name, in.Days, in.Password)
	if err != nil {
		if errors.Is(err, certs.ErrNoCA) {
			fail(w, 400, "Keine eigene Zertifizierungsstelle vorhanden – unter Zertifikat eine erzeugen")
			return
		}
		fail(w, 500, err.Error())
		return
	}
	s.audit(r, "Zertifikat für „"+name+"“ ausgestellt (Seriennummer "+serial+")")
	file := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>|`, r) || r < 0x20 {
			return '_'
		}
		return r
	}, name)
	w.Header().Set("Content-Type", "application/x-pkcs12")
	w.Header().Set("Content-Disposition", `attachment; filename="`+file+`.p12"`)
	w.Write(p12)
}

func (s *Server) revokeClientCert(w http.ResponseWriter, r *http.Request) {
	serial := r.PathValue("serial")
	if err := s.Certs.RevokeClientCert(serial); err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.audit(r, "Zertifikat "+serial+" gesperrt")
	ok(w)
}
