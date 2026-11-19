package directory

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// LDAP prüft Anmeldungen per LDAP-Bind gegen Active Directory (nur PAP).
type LDAP struct {
	URL           string // ldap://dc.firma.local oder ldaps://dc.firma.local
	StartTLS      bool
	SkipVerify    bool
	BaseDN        string
	BindUser      string
	BindPassword  string
	UserAttribute string
}

func (l *LDAP) Describe() string { return "LDAP " + l.URL }

func (l *LDAP) dial() (*ldap.Conn, error) {
	u, err := url.Parse(l.URL)
	if err != nil || u.Host == "" {
		return nil, &Error{Msg: "LDAP-Adresse ungültig (Beispiel: ldaps://dc.firma.local)"}
	}
	host := u.Hostname()
	tlsConf := &tls.Config{ServerName: host, InsecureSkipVerify: l.SkipVerify}
	c, err := ldap.DialURL(l.URL, ldap.DialWithTLSConfig(tlsConf), ldap.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}))
	if err != nil {
		return nil, &Error{Msg: "LDAP-Server nicht erreichbar: " + err.Error()}
	}
	c.SetTimeout(10 * time.Second)
	if l.StartTLS && u.Scheme == "ldap" {
		if err := c.StartTLS(tlsConf); err != nil {
			c.Close()
			return nil, &Error{Msg: "StartTLS fehlgeschlagen: " + err.Error()}
		}
	}
	return c, nil
}

// find sucht den Benutzer mit dem Dienstkonto und liefert seinen DN.
func (l *LDAP) find(c *ldap.Conn, name string) (dn, account string, err error) {
	if err := c.Bind(l.BindUser, l.BindPassword); err != nil {
		return "", "", &Error{Msg: "Dienstkonto für LDAP wurde abgelehnt – Benutzer/Passwort in den AD-Einstellungen prüfen"}
	}
	attr := l.UserAttribute
	if attr == "" {
		attr = "sAMAccountName"
	}
	user, _, upn := SplitName(name)
	filter := fmt.Sprintf("(&(objectCategory=person)(objectClass=user)(%s=%s))", attr, ldap.EscapeFilter(user))
	if upn {
		filter = fmt.Sprintf("(&(objectCategory=person)(objectClass=user)(userPrincipalName=%s))", ldap.EscapeFilter(name))
	}
	res, err := c.Search(ldap.NewSearchRequest(l.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 10, false,
		filter, []string{"distinguishedName", "sAMAccountName", "userAccountControl"}, nil))
	if err != nil {
		return "", "", &Error{Msg: "LDAP-Suche fehlgeschlagen: " + err.Error()}
	}
	if len(res.Entries) != 1 {
		return "", "", ErrBadCredentials
	}
	e := res.Entries[0]
	if uac := e.GetAttributeValue("userAccountControl"); uac != "" {
		var v int
		fmt.Sscan(uac, &v)
		if v&0x2 != 0 {
			return "", "", &Error{Msg: "AD-Konto ist deaktiviert"}
		}
	}
	return e.DN, e.GetAttributeValue("sAMAccountName"), nil
}

func (l *LDAP) AuthPAP(username, password string) (*User, error) {
	if password == "" {
		return nil, ErrBadCredentials // leeres Passwort = anonymer Bind, nie akzeptieren
	}
	c, err := l.dial()
	if err != nil {
		return nil, err
	}
	defer c.Close()
	dn, account, err := l.find(c, username)
	if err != nil {
		return nil, err
	}
	if err := c.Bind(dn, password); err != nil {
		var le *ldap.Error
		if errors.As(err, &le) && le.ResultCode == ldap.LDAPResultInvalidCredentials {
			return nil, ldapReason(le.Err.Error())
		}
		return nil, &Error{Msg: "LDAP-Anmeldung fehlgeschlagen: " + err.Error()}
	}
	// Gruppen (inkl. verschachtelter) mit dem Dienstkonto lesen.
	u := &User{Name: account}
	if err := c.Bind(l.BindUser, l.BindPassword); err == nil {
		res, err := c.Search(ldap.NewSearchRequest(l.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 10, false,
			fmt.Sprintf("(&(objectClass=group)(member:1.2.840.113556.1.4.1941:=%s))", ldap.EscapeFilter(dn)),
			[]string{"cn"}, nil))
		if err == nil {
			for _, e := range res.Entries {
				u.Groups = append(u.Groups, e.GetAttributeValue("cn"))
			}
		}
	}
	return u, nil
}

// ldapReason wertet den AD-Diagnosecode ("data 52e" usw.) aus.
func ldapReason(diag string) error {
	d := strings.ToLower(diag)
	switch {
	case strings.Contains(d, "data 532"):
		return ntStatusErrorText("AD-Passwort ist abgelaufen")
	case strings.Contains(d, "data 533"):
		return ntStatusErrorText("AD-Konto ist deaktiviert")
	case strings.Contains(d, "data 701"):
		return ntStatusErrorText("AD-Konto ist abgelaufen")
	case strings.Contains(d, "data 773"):
		return ntStatusErrorText("AD-Passwort muss geändert werden")
	case strings.Contains(d, "data 775"):
		return ntStatusErrorText("AD-Konto ist gesperrt (zu viele Fehlversuche)")
	case strings.Contains(d, "data 530"):
		return ntStatusErrorText("Anmeldung zu dieser Uhrzeit nicht erlaubt (AD-Anmeldezeiten)")
	}
	return ErrBadCredentials
}

func ntStatusErrorText(msg string) error { return &Error{Msg: msg} }

func (l *LDAP) AuthMSCHAPv2(string, []byte, []byte) (*User, []byte, error) {
	return nil, nil, ErrNoMSCHAP
}
