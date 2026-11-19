package radiusd

// Gemeinsamer Unterbau für die TLS-basierten EAP-Verfahren (PEAP, EAP-TTLS, EAP-TLS):
// TLS-Handshake in einer Goroutine, Austausch über EAP-Nachrichten, Fragmentierung.

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
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

// tunnelResult ist das Ergebnis einer abgeschlossenen TLS-basierten EAP-Sitzung.
type tunnelResult struct {
	ok     bool
	user   string // innerer Benutzername
	reason string
	method string // genaue Bezeichnung fürs Protokoll, z. B. "EAP-TTLS/PAP" ("" = Standard)
	msk    []byte // 64 Byte: Recv-Key | Send-Key
}

// tunnelSession führt den TLS-Handshake und den verfahrensabhängigen Rest
// (phase) als Goroutine aus.
type tunnelSession struct {
	method   byte // eapTypePEAP, eapTypeTTLS oder eapTypeTLS
	conn     *tunnelConn
	done     chan struct{}
	result   tunnelResult
	innerID  byte
	tlsConf  *tls.Config
	serverID string
	keylog   keyLog

	phase      func(tc *tls.Conn) // Ablauf nach dem Handshake (setzt result)
	tlsHint    string             // Hinweis bei TLS-Fehlern
	certReason string             // Ablehnungsgrund aus der Zertifikatsprüfung (EAP-TLS)

	verify    MSCHAPVerifier // PEAP, EAP-TTLS
	verifyPAP papVerifier    // EAP-TTLS/PAP

	timedOut atomic.Bool // exchange hat aufgegeben – result nicht mehr lesen
	fragSize int         // aus Framed-MTU, 0 = Standard

	// Fragmentierung (nur vom Handler benutzt)
	pendingOut []byte // noch nicht verschickte TLS-Daten
	outTotal   int
	inAccum    []byte
}

// newTunnelSession bereitet eine Sitzung vor; gestartet wird sie mit start().
func newTunnelSession(cert *tls.Certificate, method byte, serverID string) *tunnelSession {
	s := &tunnelSession{
		method:   method,
		conn:     newTunnelConn(),
		done:     make(chan struct{}),
		serverID: serverID,
		tlsHint:  " (Zertifikat auf dem Gerät nicht akzeptiert?)",
		tlsConf: &tls.Config{
			Certificates:           []tls.Certificate{*cert},
			MinVersion:             tls.VersionTLS12,
			MaxVersion:             tls.VersionTLS12, // Schlüsselableitung nach RFC 5216/5281 mit TLS 1.2
			SessionTicketsDisabled: true,
		},
	}
	s.tlsConf.KeyLogWriter = &s.keylog
	var b [1]byte
	rand.Read(b[:])
	s.innerID = b[0]
	return s
}

// methodName liefert die Bezeichnung eines EAP-Verfahrens fürs Protokoll.
func methodName(t byte) string {
	switch t {
	case eapTypeTTLS:
		return "EAP-TTLS"
	case eapTypeTLS:
		return "EAP-TLS"
	}
	return "PEAP"
}

// start startet die TLS-Goroutine und wartet, bis sie auf den ClientHello wartet.
func (s *tunnelSession) start() bool {
	go s.run()
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
func (s *tunnelSession) exchange(data []byte) (out []byte, finished bool) {
	select {
	case s.conn.in <- data:
	case <-s.done:
		return nil, true
	case <-time.After(5 * time.Second):
		s.abort()
		s.timedOut.Store(true)
		return nil, true
	}
	select {
	case <-s.conn.waiting:
		return s.conn.takeOut(), false
	case <-s.done:
		return nil, true
	case <-time.After(10 * time.Second):
		s.abort()
		select {
		case <-s.done: // Goroutine hat das Ergebnis fertig geschrieben
		case <-time.After(5 * time.Second):
			s.timedOut.Store(true)
		}
		return nil, true
	}
}

func (s *tunnelSession) abort() {
	select {
	case <-s.conn.kill:
	default:
		close(s.conn.kill)
	}
}

func (s *tunnelSession) run() {
	defer close(s.done)
	defer func() {
		// Ein Fehler in einer einzelnen Anmeldung darf nie den ganzen Dienst beenden.
		if r := recover(); r != nil {
			log.Printf("%s: interner Fehler: %v", methodName(s.method), r)
			s.result = tunnelResult{reason: "Interner Fehler bei der Anmeldung"}
		}
	}()
	s.result.reason = "Verbindung abgebrochen"
	tc := tls.Server(s.conn, s.tlsConf)
	if err := tc.Handshake(); err != nil {
		switch {
		case s.certReason != "":
			s.result.reason = s.certReason
		case !errors.Is(err, io.EOF):
			s.result.reason = "TLS-Fehler: " + err.Error() + s.tlsHint
		}
		return
	}
	s.phase(tc)
}

func readRecord(tc *tls.Conn) ([]byte, error) {
	buf := make([]byte, 4096)
	n, err := tc.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// ---- Fragmentierung (RFC 5216 Abschnitt 2.1.5, gilt auch für PEAP und EAP-TTLS) ----

// nextFragment baut die nächste Request-Nutzlast (Flags + Daten).
func (s *tunnelSession) nextFragment() []byte {
	first := len(s.pendingOut) == s.outTotal
	n := len(s.pendingOut)
	size := s.fragSize
	if size <= 0 {
		size = fragmentSize
	}
	more := n > size
	if more {
		n = size
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
