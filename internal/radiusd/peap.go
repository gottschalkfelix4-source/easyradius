package radiusd

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// EAP-Konstanten
const (
	eapRequest  = 1
	eapResponse = 2
	eapSuccess  = 3
	eapFailure  = 4

	eapTypeIdentity = 1
	eapTypeNak      = 3
	eapTypePEAP     = 25
	eapTypeMSCHAPv2 = 26
	eapTypeTLV      = 33

	peapFlagLength = 0x80
	peapFlagMore   = 0x40
	peapFlagStart  = 0x20

	fragmentSize = 1000
)

var errAborted = errors.New("Sitzung abgebrochen")

// tunnelConn verbindet crypto/tls mit den EAP-Nachrichten.
// Die TLS-Goroutine meldet über "waiting", dass sie auf Daten vom Client wartet –
// dann ist ihre Antwort vollständig in "out" und kann verschickt werden.
type tunnelConn struct {
	firstIn  []byte // ClientHello (für client_random)
	firstOut []byte // ServerHello (für server_random)
	in       chan []byte
	waiting  chan struct{}
	kill     chan struct{}
	inbuf    []byte
	mu       sync.Mutex
	out      []byte
}

func newTunnelConn() *tunnelConn {
	return &tunnelConn{in: make(chan []byte), waiting: make(chan struct{}), kill: make(chan struct{})}
}

// awaitInput gibt die Kontrolle an den RADIUS-Handler ab und wartet auf die nächste Client-Nachricht.
func (c *tunnelConn) awaitInput() ([]byte, error) {
	select {
	case c.waiting <- struct{}{}:
	case <-c.kill:
		return nil, errAborted
	}
	select {
	case d := <-c.in:
		return d, nil
	case <-c.kill:
		return nil, errAborted
	}
}

func (c *tunnelConn) Read(b []byte) (int, error) {
	for len(c.inbuf) == 0 {
		d, err := c.awaitInput()
		if err != nil {
			return 0, io.EOF
		}
		c.inbuf = d
		if c.firstIn == nil {
			c.firstIn = append([]byte(nil), d...)
		}
	}
	n := copy(b, c.inbuf)
	c.inbuf = c.inbuf[n:]
	return n, nil
}

func (c *tunnelConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	if c.firstOut == nil {
		c.firstOut = append([]byte(nil), b...)
	}
	c.out = append(c.out, b...)
	c.mu.Unlock()
	return len(b), nil
}

func (c *tunnelConn) takeOut() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	o := c.out
	c.out = nil
	return o
}

func (c *tunnelConn) Close() error                       { return nil }
func (c *tunnelConn) LocalAddr() net.Addr                { return dummyAddr{} }
func (c *tunnelConn) RemoteAddr() net.Addr               { return dummyAddr{} }
func (c *tunnelConn) SetDeadline(t time.Time) error      { return nil }
func (c *tunnelConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *tunnelConn) SetWriteDeadline(t time.Time) error { return nil }

type dummyAddr struct{}

func (dummyAddr) Network() string { return "eap" }
func (dummyAddr) String() string  { return "eap" }

// MSCHAPVerifier prüft eine MS-CHAPv2-Antwort inkl. Gruppenregeln.
// Rückgabe: Authenticator-Response ("S=..."; leer = abgelehnt), Benutzername, Ablehnungsgrund.
type MSCHAPVerifier func(name string, authC, peerC, ntResp []byte) (authResp, user, reason string)

// peapResult ist das Ergebnis einer abgeschlossenen PEAP-Sitzung.
type peapResult struct {
	ok     bool
	user   string // innerer Benutzername
	reason string
	msk    []byte // 64 Byte: Recv-Key | Send-Key
}

// peapSession führt den kompletten PEAPv0/EAP-MSCHAPv2-Ablauf als Goroutine aus.
type peapSession struct {
	conn     *tunnelConn
	done     chan struct{}
	result   peapResult
	innerID  byte
	tlsConf  *tls.Config
	verify   MSCHAPVerifier
	serverID string
	keylog   keyLog

	// Fragmentierung (nur vom Handler benutzt)
	pendingOut []byte // noch nicht verschickte TLS-Daten
	outTotal   int
	inAccum    []byte
}

func newPEAPSession(cert *tls.Certificate, verify MSCHAPVerifier, serverID string) *peapSession {
	s := &peapSession{
		conn:     newTunnelConn(),
		done:     make(chan struct{}),
		verify:   verify,
		serverID: serverID,
		tlsConf: &tls.Config{
			Certificates:           []tls.Certificate{*cert},
			MinVersion:             tls.VersionTLS12,
			MaxVersion:             tls.VersionTLS12, // PEAP-Schlüsselableitung nach RFC 5216/TLS 1.2
			SessionTicketsDisabled: true,
		},
	}
	s.tlsConf.KeyLogWriter = &s.keylog
	b := make([]byte, 1)
	rand.Read(b)
	s.innerID = b[0]
	go s.run()
	return s
}

// start wartet, bis die TLS-Goroutine auf den ClientHello wartet.
func (s *peapSession) start() bool {
	select {
	case <-s.conn.waiting:
		return true
	case <-s.done:
		return false
	case <-time.After(5 * time.Second):
		return false
	}
}

// exchange übergibt eine vollständige Client-Nachricht und liefert die Antwort.
// finished=true heißt: die Sitzung ist beendet, s.result ist gesetzt.
func (s *peapSession) exchange(data []byte) (out []byte, finished bool) {
	select {
	case s.conn.in <- data:
	case <-s.done:
		return nil, true
	case <-time.After(5 * time.Second):
		return nil, true
	}
	select {
	case <-s.conn.waiting:
		return s.conn.takeOut(), false
	case <-s.done:
		return nil, true
	case <-time.After(10 * time.Second):
		s.abort()
		return nil, true
	}
}

func (s *peapSession) abort() {
	select {
	case <-s.conn.kill:
	default:
		close(s.conn.kill)
	}
}

func (s *peapSession) run() {
	defer close(s.done)
	s.result.reason = "Verbindung abgebrochen"
	tc := tls.Server(s.conn, s.tlsConf)
	if err := tc.Handshake(); err != nil {
		if !errors.Is(err, io.EOF) {
			s.result.reason = "TLS-Fehler: " + err.Error() + " (Zertifikat auf dem Gerät nicht akzeptiert?)"
		}
		return
	}
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

func readRecord(tc *tls.Conn) ([]byte, error) {
	buf := make([]byte, 4096)
	n, err := tc.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// innerRequest sendet einen getunnelten EAP-Request (PEAPv0: ohne EAP-Header)
// und liefert Typ + Daten der Antwort.
func (s *peapSession) innerRequest(tc *tls.Conn, typeData []byte) ([]byte, error) {
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
func (s *peapSession) phase2(tc *tls.Conn) (user string, ok bool, reason string) {
	r, err := s.innerRequest(tc, []byte{eapTypeIdentity})
	if err != nil || r[0] != eapTypeIdentity {
		return "", false, "Keine Identität im Tunnel erhalten"
	}
	user = string(r[1:])

	challenge := make([]byte, 16)
	rand.Read(challenge)
	msid := s.innerID + 1 // MS-CHAPv2-ID = nächste EAP-ID
	name := []byte(s.serverID)
	msLen := 4 + 1 + 16 + len(name)
	req := []byte{eapTypeMSCHAPv2, 1, msid, byte(msLen >> 8), byte(msLen), 16}
	req = append(req, challenge...)
	req = append(req, name...)
	r, err = s.innerRequest(tc, req)
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
		s.mschapFailure(tc, msid)
		return user, false, why
	}
	msg := []byte(authResp + " M=Willkommen")
	msLen = 4 + len(msg)
	succ := append([]byte{eapTypeMSCHAPv2, 3, msid, byte(msLen >> 8), byte(msLen)}, msg...)
	r, err = s.innerRequest(tc, succ)
	if err != nil || r[0] != eapTypeMSCHAPv2 || len(r) < 2 || r[1] != 3 {
		return user, false, "Gerät hat die Serverantwort nicht akzeptiert"
	}
	return user, true, ""
}

func (s *peapSession) mschapFailure(tc *tls.Conn, msid byte) {
	c := make([]byte, 16)
	rand.Read(c)
	msg := []byte(fmt.Sprintf("E=691 R=0 C=%s V=3 M=Anmeldung fehlgeschlagen", strings.ToUpper(hex.EncodeToString(c))))
	msLen := 4 + len(msg)
	req := append([]byte{eapTypeMSCHAPv2, 4, msid, byte(msLen >> 8), byte(msLen)}, msg...)
	s.innerRequest(tc, req)
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

// ---- Fragmentierung (RFC 5216 Abschnitt 2.1.5) ----

// nextFragment baut die nächste PEAP-Request-Nutzlast (Flags + Daten).
func (s *peapSession) nextFragment() []byte {
	first := len(s.pendingOut) == s.outTotal
	n := len(s.pendingOut)
	more := n > fragmentSize
	if more {
		n = fragmentSize
	}
	var flags byte
	var hdr []byte
	if more {
		flags |= peapFlagMore
	}
	if first && more {
		flags |= peapFlagLength
		hdr = make([]byte, 4)
		binary.BigEndian.PutUint32(hdr, uint32(s.outTotal))
	}
	p := append([]byte{flags}, hdr...)
	p = append(p, s.pendingOut[:n]...)
	s.pendingOut = s.pendingOut[n:]
	return p
}
