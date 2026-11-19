package radiusd

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// EAP-Konstanten
const (
	eapRequest  = 1
	eapResponse = 2
	eapSuccess  = 3
	eapFailure  = 4

	eapTypeIdentity = 1
	eapTypeNak      = 3
	eapTypeTLS      = 13
	eapTypeTTLS     = 21
	eapTypePEAP     = 25
	eapTypeMSCHAPv2 = 26
	eapTypeTLV      = 33

	// Flags im ersten Byte von PEAP/EAP-TTLS/EAP-TLS (identisch aufgebaut)
	peapFlagLength = 0x80
	peapFlagMore   = 0x40
	peapFlagStart  = 0x20

	fragmentSize = 1000
)

// MSCHAPVerifier prüft eine MS-CHAPv2-Antwort inkl. Gruppenregeln.
// Rückgabe: Authenticator-Response ("S=..."; leer = abgelehnt), Benutzername, Ablehnungsgrund.
type MSCHAPVerifier func(name string, authC, peerC, ntResp []byte) (authResp, user, reason string)

// innerExchange schickt einen inneren EAP-Request (Typ + Daten, ohne EAP-Kopf)
// und liefert Typ + Daten der Antwort.
type innerExchange func(typeData []byte) ([]byte, error)

func newPEAPSession(cert *tls.Certificate, verify MSCHAPVerifier, serverID string) *tunnelSession {
	s := newTunnelSession(cert, eapTypePEAP, serverID)
	s.verify = verify
	s.phase = s.peapPhase
	return s
}

// peapPhase: PEAPv0 nach dem Handshake – EAP-MSCHAPv2 im Tunnel und Result-TLV.
func (s *tunnelSession) peapPhase(tc *tls.Conn) {
	// Client bestätigt unser "Finished" mit einer leeren Nachricht.
	if _, err := s.conn.awaitInput(); err != nil {
		return
	}

	msk, err := s.deriveMSK(tc)
	if err != nil {
		s.result.reason = "Schlüsselableitung fehlgeschlagen: " + err.Error()
		return
	}

	user, ok, reason := s.phase2(tc)
	s.result.user = user
	s.result.reason = reason

	// Ergebnis per Result-TLV bestätigen lassen (vollständiger EAP-Header).
	status := byte(2)
	if ok {
		status = 1
	}
	s.innerID++
	tlv := []byte{eapRequest, s.innerID, 0, 11, eapTypeTLV, 0x80, 0x03, 0x00, 0x02, 0x00, status}
	if _, err := tc.Write(tlv); err != nil {
		return
	}
	resp, err := readRecord(tc)
	if err != nil {
		return
	}
	if !ok {
		return
	}
	if len(resp) < 11 || resp[0] != eapResponse || resp[4] != eapTypeTLV || resp[10] != 1 {
		s.result.reason = "Gerät hat das Ergebnis nicht bestätigt"
		return
	}
	s.result.ok = true
	s.result.msk = msk[:64]
}

// innerRequest sendet einen getunnelten EAP-Request (PEAPv0: ohne EAP-Header)
// und liefert Typ + Daten der Antwort.
func (s *tunnelSession) innerRequest(tc *tls.Conn, typeData []byte) ([]byte, error) {
	s.innerID++
	if _, err := tc.Write(typeData); err != nil {
		return nil, err
	}
	r, err := readRecord(tc)
	if err != nil {
		return nil, err
	}
	// Manche Clients schicken trotzdem einen vollständigen Header.
	if len(r) >= 5 && r[0] == eapResponse && r[1] == s.innerID && int(binary.BigEndian.Uint16(r[2:4])) == len(r) {
		r = r[4:]
	}
	if len(r) == 0 {
		return nil, errors.New("leere Antwort")
	}
	return r, nil
}

// phase2 führt Identität + EAP-MSCHAPv2 im Tunnel durch.
func (s *tunnelSession) phase2(tc *tls.Conn) (user string, ok bool, reason string) {
	req := func(d []byte) ([]byte, error) { return s.innerRequest(tc, d) }
	r, err := req([]byte{eapTypeIdentity})
	if err != nil || r[0] != eapTypeIdentity {
		return "", false, "Keine Identität im Tunnel erhalten"
	}
	return s.eapMSCHAPv2(string(r[1:]), req)
}

// eapMSCHAPv2 führt EAP-MSCHAPv2 (Challenge, Response, Success) über req durch.
// s.innerID ist die ID der zuletzt gesendeten inneren Anfrage.
func (s *tunnelSession) eapMSCHAPv2(user string, req innerExchange) (string, bool, string) {
	challenge := make([]byte, 16)
	rand.Read(challenge)
	msid := s.innerID + 1 // MS-CHAPv2-ID = nächste EAP-ID
	name := []byte(s.serverID)
	msLen := 4 + 1 + 16 + len(name)
	r := []byte{eapTypeMSCHAPv2, 1, msid, byte(msLen >> 8), byte(msLen), 16}
	r = append(r, challenge...)
	r = append(r, name...)
	r, err := req(r)
	if err != nil {
		return user, false, "Keine MS-CHAPv2-Antwort"
	}
	if r[0] == eapTypeNak {
		return user, false, "Gerät lehnt MS-CHAPv2 ab (innere Methode auf EAP-MSCHAPv2 stellen)"
	}
	// 26, OpCode 2, ID, Len(2), ValueSize 49, PeerChallenge 16, Reserved 8, NT-Response 24, Flags 1, Name
	if len(r) < 6+49 || r[0] != eapTypeMSCHAPv2 || r[1] != 2 || r[5] != 49 {
		return user, false, "Ungültige MS-CHAPv2-Antwort"
	}
	peerChallenge := r[6:22]
	ntResponse := r[30:54]
	respName := string(r[55:])
	if respName == "" {
		respName = user
	}

	authResp, uname, why := s.verify(respName, challenge, peerChallenge, ntResponse)
	if uname != "" {
		user = uname
	}
	if authResp == "" {
		mschapFailure(req, msid)
		return user, false, why
	}
	msg := []byte(authResp + " M=Willkommen")
	msLen = 4 + len(msg)
	succ := append([]byte{eapTypeMSCHAPv2, 3, msid, byte(msLen >> 8), byte(msLen)}, msg...)
	r, err = req(succ)
	if err != nil || r[0] != eapTypeMSCHAPv2 || len(r) < 2 || r[1] != 3 {
		return user, false, "Gerät hat die Serverantwort nicht akzeptiert"
	}
	return user, true, ""
}

func mschapFailure(req innerExchange, msid byte) {
	c := make([]byte, 16)
	rand.Read(c)
	msg := []byte(fmt.Sprintf("E=691 R=0 C=%s V=3 M=Anmeldung fehlgeschlagen", strings.ToUpper(hex.EncodeToString(c))))
	msLen := 4 + len(msg)
	r := append([]byte{eapTypeMSCHAPv2, 4, msid, byte(msLen >> 8), byte(msLen)}, msg...)
	req(r)
}

// stripRealm entfernt "DOMAIN\" und "@realm".
func stripRealm(u string) string {
	if i := strings.LastIndex(u, `\`); i >= 0 {
		u = u[i+1:]
	}
	if i := strings.Index(u, "@"); i > 0 {
		u = u[:i]
	}
	return u
}
