package radiusd

// EAP-TTLSv0 (RFC 5281): TLS-Tunnel, darin Diameter-AVPs mit PAP, MS-CHAPv2
// oder EAP-MSCHAPv2 (in EAP-Message-AVPs).

import (
	"bytes"
	"crypto/subtle"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
)

// AVP-Codes (RADIUS-Attributnummern, RFC 5281 Abschnitt 10)
const (
	avpUserName     = 1
	avpUserPassword = 2
	avpCHAPPassword = 3
	avpEAPMessage   = 79

	vendorMicrosoft     = 311
	avpMSCHAPResponse   = 1
	avpMSCHAPChallenge  = 11
	avpMSCHAP2Response  = 25
	avpMSCHAP2Success   = 26
	avpFlagVendor       = 0x80
	avpFlagMandatory    = 0x40
	maxTTLSInnerMessage = 16 * 1024
)

// papVerifier prüft Benutzername + Passwort inkl. Gruppenregeln.
type papVerifier func(name, pw string) (user string, ok bool, reason string)

// avp ist ein Diameter-AVP aus dem Tunnel.
type avp struct {
	code   uint32
	vendor uint32
	flags  byte
	data   []byte
}

// appendAVP hängt ein AVP an (Code 4, Flags 1, Länge 3, [Vendor 4], Daten, auf 4 Byte aufgefüllt).
func appendAVP(b []byte, code, vendor uint32, data []byte) []byte {
	flags := byte(avpFlagMandatory)
	hl := 8
	if vendor != 0 {
		flags |= avpFlagVendor
		hl = 12
	}
	l := hl + len(data)
	var h [12]byte
	binary.BigEndian.PutUint32(h[0:4], code)
	binary.BigEndian.PutUint32(h[4:8], uint32(l))
	h[4] = flags
	binary.BigEndian.PutUint32(h[8:12], vendor)
	b = append(b, h[:hl]...)
	b = append(b, data...)
	for i := l; i%4 != 0; i++ {
		b = append(b, 0)
	}
	return b
}

// parseAVPs zerlegt eine AVP-Folge. complete=false: das letzte AVP ist noch unvollständig.
func parseAVPs(b []byte) (list []avp, complete bool, err error) {
	for len(b) > 0 {
		if len(b) < 8 {
			return nil, false, nil
		}
		a := avp{code: binary.BigEndian.Uint32(b[0:4]), flags: b[4]}
		l := int(binary.BigEndian.Uint32(b[4:8]) & 0xffffff)
		hl := 8
		if a.flags&avpFlagVendor != 0 {
			hl = 12
		}
		if l < hl {
			return nil, false, errors.New("ungültige AVP-Länge")
		}
		if l > len(b) {
			return nil, false, nil
		}
		if hl == 12 {
			a.vendor = binary.BigEndian.Uint32(b[8:12])
		}
		a.data = b[hl:l]
		list = append(list, a)
		padded := (l + 3) &^ 3
		if padded > len(b) {
			padded = len(b) // fehlende Auffüllung am Ende tolerieren
		}
		b = b[padded:]
	}
	return list, true, nil
}

// findAVP liefert die Daten des ersten passenden AVP (nil = fehlt).
func findAVP(list []avp, code, vendor uint32) []byte {
	for _, a := range list {
		if a.code == code && a.vendor == vendor {
			return a.data
		}
	}
	return nil
}

func hasAVP(list []avp, code, vendor uint32) bool {
	for _, a := range list {
		if a.code == code && a.vendor == vendor {
			return true
		}
	}
	return false
}

// readAVPs liest die nächste vollständige AVP-Folge aus dem Tunnel
// (ggf. über mehrere TLS-Records verteilt).
func readAVPs(tc *tls.Conn) ([]avp, error) {
	var buf []byte
	for {
		r, err := readRecord(tc)
		if err != nil {
			return nil, err
		}
		buf = append(buf, r...)
		if len(buf) > maxTTLSInnerMessage {
			return nil, errors.New("innere Nachricht zu groß")
		}
		list, complete, err := parseAVPs(buf)
		if err != nil {
			return nil, err
		}
		if complete && len(list) > 0 {
			return list, nil
		}
	}
}

func newTTLSSession(cert *tls.Certificate, verify MSCHAPVerifier, verifyPAP papVerifier, serverID string) *tunnelSession {
	s := newTunnelSession(cert, eapTypeTTLS, serverID)
	s.verify = verify
	s.verifyPAP = verifyPAP
	s.phase = s.ttlsPhase
	return s
}

// ttlsPhase: Nach dem Handshake schickt der Client seine Anmeldedaten als AVPs.
func (s *tunnelSession) ttlsPhase(tc *tls.Conn) {
	list, err := readAVPs(tc)
	if err != nil {
		if !errors.Is(err, io.EOF) {
			s.result.reason = "Ungültige Anmeldedaten im Tunnel: " + err.Error()
		}
		return
	}
	msk, err := s.exportKey(tc, "ttls keying material", 64)
	if err != nil {
		s.result.reason = "Schlüsselableitung fehlgeschlagen: " + err.Error()
		return
	}
	user := string(findAVP(list, avpUserName, 0))
	s.result.user = user

	var ok bool
	switch {
	case hasAVP(list, avpEAPMessage, 0):
		s.result.method = "EAP-TTLS/EAP-MSCHAPv2"
		ok = s.ttlsInnerEAP(tc, list)
	case hasAVP(list, avpMSCHAP2Response, vendorMicrosoft):
		s.result.method = "EAP-TTLS/MSCHAPv2"
		ok = s.ttlsMSCHAPv2(tc, list, user)
	case hasAVP(list, avpUserPassword, 0):
		s.result.method = "EAP-TTLS/PAP"
		ok = s.ttlsPAP(list, user)
	case hasAVP(list, avpCHAPPassword, 0), hasAVP(list, avpMSCHAPResponse, vendorMicrosoft):
		s.result.reason = "CHAP/MS-CHAP (v1) wird im TTLS-Tunnel nicht unterstützt – innere Methode „PAP“ oder „MSCHAPv2“ wählen"
	default:
		s.result.reason = "Unbekannte innere Methode im TTLS-Tunnel – „PAP“ oder „MSCHAPv2“ wählen"
	}
	if ok {
		s.result.ok = true
		s.result.reason = ""
		s.result.msk = msk
	}
}

// ttlsPAP prüft User-Name + User-Password.
func (s *tunnelSession) ttlsPAP(list []avp, user string) bool {
	if user == "" {
		s.result.reason = "Kein Benutzername im Tunnel"
		return false
	}
	pw := bytes.TrimRight(findAVP(list, avpUserPassword, 0), "\x00")
	uname, ok, why := s.verifyPAP(user, string(pw))
	if uname != "" {
		s.result.user = uname
	}
	if !ok {
		s.result.reason = why
		return false
	}
	return true
}

// ttlsMSCHAPv2 prüft MS-CHAPv2 mit der impliziten Challenge (RFC 5281 Abschnitt 11.2.4).
func (s *tunnelSession) ttlsMSCHAPv2(tc *tls.Conn, list []avp, user string) bool {
	if user == "" {
		s.result.reason = "Kein Benutzername im Tunnel"
		return false
	}
	chal := findAVP(list, avpMSCHAPChallenge, vendorMicrosoft)
	resp := findAVP(list, avpMSCHAP2Response, vendorMicrosoft)
	if len(chal) != 16 || len(resp) != 50 {
		s.result.reason = "Ungültige MS-CHAPv2-Daten im Tunnel"
		return false
	}
	implicit, err := s.exportKey(tc, "ttls challenge", 17)
	if err != nil {
		s.result.reason = "Schlüsselableitung fehlgeschlagen: " + err.Error()
		return false
	}
	if subtle.ConstantTimeCompare(chal, implicit[:16]) != 1 || resp[0] != implicit[16] {
		s.result.reason = "MS-CHAPv2-Challenge passt nicht zum TLS-Tunnel"
		return false
	}
	ident := resp[0]
	authResp, uname, why := s.verify(user, chal, resp[2:18], resp[26:50])
	if uname != "" {
		s.result.user = uname
	}
	if authResp == "" {
		s.result.reason = why
		return false
	}
	succ := appendAVP(nil, avpMSCHAP2Success, vendorMicrosoft, append([]byte{ident}, authResp...))
	if _, err := tc.Write(succ); err != nil {
		return false
	}
	// Client prüft die Serverantwort und bestätigt mit einer leeren Nachricht.
	d, err := s.conn.awaitInput()
	if err != nil {
		return false
	}
	if len(d) != 0 {
		s.result.reason = "Gerät hat die Serverantwort nicht akzeptiert"
		return false
	}
	return true
}

// ttlsInnerEAP führt EAP-MSCHAPv2 in EAP-Message-AVPs durch (wie bei PEAP).
func (s *tunnelSession) ttlsInnerEAP(tc *tls.Conn, list []avp) bool {
	e := findAVP(list, avpEAPMessage, 0)
	if len(e) < 5 || e[0] != eapResponse || int(binary.BigEndian.Uint16(e[2:4])) != len(e) || e[4] != eapTypeIdentity {
		s.result.reason = "Keine Identität im Tunnel erhalten"
		return false
	}
	s.innerID = e[1]
	user := string(e[5:])
	s.result.user = user
	req := func(typeData []byte) ([]byte, error) {
		s.innerID++
		l := 4 + len(typeData)
		msg := append([]byte{eapRequest, s.innerID, byte(l >> 8), byte(l)}, typeData...)
		if _, err := tc.Write(appendAVP(nil, avpEAPMessage, 0, msg)); err != nil {
			return nil, err
		}
		list, err := readAVPs(tc)
		if err != nil {
			return nil, err
		}
		r := findAVP(list, avpEAPMessage, 0)
		if len(r) < 5 || r[0] != eapResponse || r[1] != s.innerID || int(binary.BigEndian.Uint16(r[2:4])) != len(r) {
			return nil, errors.New("ungültige innere EAP-Antwort")
		}
		return r[4:], nil
	}
	uname, ok, why := s.eapMSCHAPv2(user, req)
	s.result.user = uname
	if !ok {
		s.result.reason = why
	}
	return ok
}
