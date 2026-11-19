// Package directory prüft Anmeldungen gegen Active Directory –
// entweder über den Domänenbeitritt des Rechners oder per LDAP.
package directory

import (
	"errors"
	"strings"
)

// User ist ein erfolgreich geprüftes AD-Konto.
type User struct {
	Name   string   // Anmeldename ohne Domäne
	Domain string   // NetBIOS-Domäne (falls bekannt)
	Groups []string // Gruppennamen (inkl. verschachtelter Gruppen)
}

// Directory ist eine Anmeldequelle.
type Directory interface {
	// AuthPAP prüft Benutzername und Klartextpasswort.
	AuthPAP(username, password string) (*User, error)
	// AuthMSCHAPv2 prüft eine MS-CHAPv2-Antwort. challenge8 ist der ChallengeHash
	// nach RFC 2759. Zurück kommt der Hash des NT-Hashes für die Serverantwort.
	AuthMSCHAPv2(username string, challenge8, ntResponse []byte) (*User, []byte, error)
	// Describe beschreibt die Quelle für die Oberfläche.
	Describe() string
}

// Fehler mit verständlicher Meldung für Protokoll und Oberfläche.
type Error struct {
	Msg       string
	BadSecret bool // Benutzer existiert nicht oder Passwort falsch
}

func (e *Error) Error() string { return e.Msg }

var (
	ErrBadCredentials = &Error{Msg: "Falsches Passwort oder unbekannter AD-Benutzer", BadSecret: true}
	ErrNoMSCHAP       = &Error{Msg: "Mit LDAP ist nur PAP möglich – für WLAN (PEAP) muss der Rechner der Domäne beitreten"}
	ErrNotAvailable   = errors.New("Active Directory ist nicht verfügbar")
)

// SplitName zerlegt "DOMÄNE\name" und "name@domäne".
func SplitName(username string) (user, domain string, upn bool) {
	if i := strings.LastIndex(username, `\`); i >= 0 {
		return username[i+1:], username[:i], false
	}
	if i := strings.LastIndex(username, "@"); i > 0 {
		return username[:i], username[i+1:], true
	}
	return username, "", false
}

// splitGroup zerlegt "FIRMA\Gruppe" bzw. "CN=Gruppe,OU=..." in Domäne und Namen.
func splitGroup(s string) (domain, name string) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(strings.ToUpper(s), "CN=") {
		s = s[3:]
		if i := strings.Index(s, ","); i >= 0 {
			s = s[:i]
		}
		return "", s
	}
	if i := strings.LastIndex(s, `\`); i >= 0 {
		return s[:i], s[i+1:]
	}
	return "", s
}

// GroupMatches vergleicht AD-Gruppennamen: "WLAN-Mitarbeiter" und
// "CN=WLAN-Mitarbeiter,OU=..." passen zu jeder Domäne, "FIRMA\WLAN-Mitarbeiter"
// nur zur Domäne FIRMA – eine gleichnamige Gruppe einer vertrauten Domäne zählt dann nicht.
func GroupMatches(adGroup, configured string) bool {
	ad, an := splitGroup(adGroup)
	cd, cn := splitGroup(configured)
	if !strings.EqualFold(an, cn) {
		return false
	}
	return cd == "" || ad == "" || strings.EqualFold(ad, cd)
}
