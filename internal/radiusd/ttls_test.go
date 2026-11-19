package radiusd

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/gottschalkfelix4-source/easyradius/internal/events"
	"github.com/gottschalkfelix4-source/easyradius/internal/store"

	"layeh.com/radius"
	"layeh.com/radius/rfc2759"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2868"
	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

// ---- Test-Client für EAP-TTLS / EAP-TLS ----

type tlsTestClient struct {
	conn     *tunnelConn
	done     chan struct{}
	err      error
	conf     *tls.Config
	mskLabel string
	mskLen   int
	msk      []byte
	authOK   bool
	// inner läuft nach dem Handshake (innere Anmeldung bzw. leere Bestätigung).
	inner func(c *tlsTestClient, tc *tls.Conn) error
}

func newTLSTestClient(label string, n int, inner func(c *tlsTestClient, tc *tls.Conn) error) *tlsTestClient {
	return &tlsTestClient{
		conn:     newTunnelConn(),
		done:     make(chan struct{}),
		conf:     &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS12},
		mskLabel: label,
		mskLen:   n,
		inner:    inner,
	}
}

func (c *tlsTestClient) run() {
	defer close(c.done)
	tc := tls.Client(c.conn, c.conf)
	if err := tc.Handshake(); err != nil {
		c.err = err
		return
	}
	cs := tc.ConnectionState()
	c.msk, c.err = cs.ExportKeyingMaterial(c.mskLabel, nil, c.mskLen)
	if c.err != nil {
		return
	}
	if c.inner != nil {
		c.err = c.inner(c, tc)
	}
}

// ackEmpty schickt eine leere EAP-Antwort (ohne TLS-Daten) und wartet.
func (c *tlsTestClient) ackEmpty() {
	c.conn.awaitInput()
}

const testFrag = 300 // kleine Fragmente, damit auch die Zerlegung beim Server geprüft wird

// eapLogin spielt den Ablauf durch: Identity, ggf. NAK mit nak, dann das Verfahren want.
func eapLogin(t *testing.T, want byte, nak []byte, c *tlsTestClient) *radius.Packet {
	t.Helper()
	send := func(state []byte, eap []byte) *radius.Packet {
		p := radius.New(radius.CodeAccessRequest, []byte(testSecret))
		rfc2865.UserName_SetString(p, "anonymous")
		rfc2865.CallingStationID_SetString(p, "AA-BB-CC-DD-EE-01")
		if state != nil {
			rfc2865.State_Set(p, state)
		}
		setEAP(p, eap)
		addMA(p)
		return exchange(t, p)
	}
	r := send(nil, buildEAP(eapResponse, 1, eapTypeIdentity, []byte("anonymous")))
	var inAccum, pending []byte
	pendingTotal := 0
	nakSent := false
	for round := 0; round < 80; round++ {
		if r.Code != radius.CodeAccessChallenge {
			c.conn.abort()
			return r
		}
		eap := eapMessage(r)
		id, typ, data := eap[1], eap[4], eap[5:]
		state := rfc2865.State_Get(r)
		if typ != want {
			if nak != nil && !nakSent {
				nakSent = true
				r = send(state, buildEAP(eapResponse, id, eapTypeNak, nak))
				continue
			}
			t.Fatalf("unerwartetes EAP-Verfahren %d", typ)
		}
		flags, payload := data[0], data[1:]
		if flags&peapFlagLength != 0 {
			payload = payload[4:]
		}
		switch {
		case flags&peapFlagStart != 0:
			if flags&0x07 != 0 {
				t.Fatalf("Start mit Version %d", flags&0x07)
			}
			go c.run()
			select {
			case <-c.conn.waiting:
			case <-c.done:
				t.Fatalf("Client beendet: %v", c.err)
			}
			pending = c.conn.takeOut()
			pendingTotal = len(pending)
		case len(pending) > 0:
			// Server bestätigt unser Fragment.
			if len(payload) != 0 || flags != 0 {
				t.Fatalf("Fragment-Bestätigung erwartet: %x", data)
			}
		default:
			inAccum = append(inAccum, payload...)
			if flags&peapFlagMore != 0 {
				r = send(state, buildEAP(eapResponse, id, want, []byte{0}))
				continue
			}
			msg := inAccum
			inAccum = nil
			c.conn.in <- msg
			select {
			case <-c.conn.waiting:
			case <-c.done:
				t.Fatalf("Client beendet: %v", c.err)
			}
			pending = c.conn.takeOut()
			pendingTotal = len(pending)
		}
		// Nächstes (ggf. einziges) Fragment der Client-Nachricht schicken.
		n := len(pending)
		more := n > testFrag
		if more {
			n = testFrag
		}
		var flagsOut byte
		var hdr []byte
		if more {
			flagsOut |= peapFlagMore
			if len(pending) == pendingTotal {
				flagsOut |= peapFlagLength
				hdr = binary.BigEndian.AppendUint32(nil, uint32(pendingTotal))
			}
		}
		frag := append(append([]byte{flagsOut}, hdr...), pending[:n]...)
		pending = pending[n:]
		r = send(state, buildEAP(eapResponse, id, want, frag))
	}
	t.Fatal("zu viele Runden")
	return nil
}

// ---- innere Verfahren des Test-Clients ----

func ttlsPAPClient(user, pw string) *tlsTestClient {
	return newTLSTestClient("ttls keying material", 64, func(c *tlsTestClient, tc *tls.Conn) error {
		avps := appendAVP(nil, avpUserName, 0, []byte(user))
		// Passwort mit NUL-Auffüllung wie bei manchen Clients
		avps = appendAVP(avps, avpUserPassword, 0, append([]byte(pw), 0, 0, 0))
		if _, err := tc.Write(avps); err != nil {
			return err
		}
		_, err := readRecord(tc) // kommt nicht mehr – der Server entscheidet sofort
		return err
	})
}

func ttlsMSCHAPClient(user, pw string, badChallenge bool) *tlsTestClient {
	return newTLSTestClient("ttls keying material", 64, func(c *tlsTestClient, tc *tls.Conn) error {
		cs := tc.ConnectionState()
		ch, err := cs.ExportKeyingMaterial("ttls challenge", nil, 17)
		if err != nil {
			return err
		}
		authC, ident := ch[:16], ch[16]
		if badChallenge {
			authC = make([]byte, 16)
			rand.Read(authC)
		}
		peerC := make([]byte, 16)
		rand.Read(peerC)
		nt, _ := rfc2759.GenerateNTResponse(authC, peerC, []byte(user), []byte(pw))
		resp := append([]byte{ident, 0}, peerC...)
		resp = append(resp, make([]byte, 8)...)
		resp = append(resp, nt...)
		avps := appendAVP(nil, avpUserName, 0, []byte(user))
		avps = appendAVP(avps, avpMSCHAPChallenge, vendorMicrosoft, authC)
		avps = appendAVP(avps, avpMSCHAP2Response, vendorMicrosoft, resp)
		if _, err := tc.Write(avps); err != nil {
			return err
		}
		list, err := readAVPs(tc)
		if err != nil {
			return err
		}
		succ := findAVP(list, avpMSCHAP2Success, vendorMicrosoft)
		want, _ := rfc2759.GenerateAuthenticatorResponse(authC, peerC, nt, []byte(user), []byte(pw))
		if len(succ) < 2 || succ[0] != ident || string(succ[1:]) != want {
			return fmt.Errorf("MS-CHAP2-Success falsch: %q, erwartet %q", succ, want)
		}
		c.authOK = true
		c.ackEmpty()
		return nil
	})
}

func ttlsEAPMSCHAPClient(user, pw string) *tlsTestClient {
	return newTLSTestClient("ttls keying material", 64, func(c *tlsTestClient, tc *tls.Conn) error {
		eapAVP := func(id byte, typeData []byte) []byte {
			l := 4 + len(typeData)
			return appendAVP(nil, avpEAPMessage, 0, append([]byte{eapResponse, id, byte(l >> 8), byte(l)}, typeData...))
		}
		if _, err := tc.Write(eapAVP(7, append([]byte{eapTypeIdentity}, user...))); err != nil {
			return err
		}
		next := func() ([]byte, error) {
			list, err := readAVPs(tc)
			if err != nil {
				return nil, err
			}
			e := findAVP(list, avpEAPMessage, 0)
			if len(e) < 5 || e[0] != eapRequest {
				return nil, fmt.Errorf("EAP-Request erwartet: %x", e)
			}
			return e, nil
		}
		e, err := next()
		if err != nil {
			return err
		}
		if e[4] != eapTypeMSCHAPv2 || e[5] != 1 {
			return fmt.Errorf("Challenge erwartet: %x", e)
		}
		msid, authC := e[6], e[10:26]
		peerC := make([]byte, 16)
		rand.Read(peerC)
		nt, _ := rfc2759.GenerateNTResponse(authC, peerC, []byte(user), []byte(pw))
		val := append(append(append(append([]byte{}, peerC...), make([]byte, 8)...), nt...), 0)
		msLen := 4 + 1 + 49 + len(user)
		resp := []byte{eapTypeMSCHAPv2, 2, msid, byte(msLen >> 8), byte(msLen), 49}
		resp = append(append(resp, val...), user...)
		if _, err := tc.Write(eapAVP(e[1], resp)); err != nil {
			return err
		}
		e, err = next()
		if err != nil {
			return err
		}
		if e[4] != eapTypeMSCHAPv2 || e[5] != 3 {
			tc.Write(eapAVP(e[1], []byte{eapTypeMSCHAPv2, 4}))
			readRecord(tc)
			return nil
		}
		want, _ := rfc2759.GenerateAuthenticatorResponse(authC, peerC, nt, []byte(user), []byte(pw))
		if !bytes.HasPrefix(e[9:], []byte(want)) {
			return fmt.Errorf("Server-Authenticator falsch: %q", e[9:])
		}
		c.authOK = true
		tc.Write(eapAVP(e[1], []byte{eapTypeMSCHAPv2, 3}))
		readRecord(tc)
		return nil
	})
}

func eapTLSClient(cert *tls.Certificate) *tlsTestClient {
	c := newTLSTestClient("client EAP encryption", 128, func(c *tlsTestClient, tc *tls.Conn) error {
		c.ackEmpty() // leere Bestätigung des Server-Finished
		return nil
	})
	if cert != nil {
		c.conf.Certificates = []tls.Certificate{*cert}
	}
	return c
}

// lastEvent liefert den neuesten Protokolleintrag.
func lastEvent(t *testing.T) events.Event {
	t.Helper()
	ev := testServer.Log.Recent(1)
	if len(ev) == 0 {
		t.Fatal("kein Protokolleintrag")
	}
	return ev[0]
}

func expectAccept(t *testing.T, r *radius.Packet, c *tlsTestClient, method string) {
	t.Helper()
	if r.Code != radius.CodeAccessAccept {
		t.Fatalf("%s: %v %s (Client-Fehler: %v)", method, r.Code, reason(r), c.err)
	}
	if e := eapMessage(r); len(e) != 4 || e[0] != eapSuccess {
		t.Fatalf("EAP-Success fehlt: %x", e)
	}
	if u := rfc2865.UserName_GetString(r); u != "alice" {
		t.Fatalf("User-Name %q", u)
	}
	if _, g := rfc2868.TunnelPrivateGroupID_GetString(r); g != "20" {
		t.Fatalf("VLAN %q", g)
	}
	if len(c.msk) < 64 {
		t.Fatal("Client-MSK fehlt")
	}
	checkKeys(t, r, c.msk)
	if ev := lastEvent(t); ev.Kind != events.Accept || ev.Method != method {
		t.Fatalf("Protokoll: %v %q, erwartet Methode %q", ev.Kind, ev.Method, method)
	}
}

func expectReject(t *testing.T, r *radius.Packet, wantReason string) {
	t.Helper()
	if r.Code != radius.CodeAccessReject {
		t.Fatalf("Ablehnung erwartet, erhalten %v", r.Code)
	}
	if e := eapMessage(r); len(e) != 4 || e[0] != eapFailure {
		t.Fatalf("EAP-Failure fehlt: %x", e)
	}
	if got := reason(r); !strings.Contains(got, wantReason) {
		t.Fatalf("Grund %q, erwartet „%s“", got, wantReason)
	}
}

// ---- EAP-TTLS ----

func TestTTLSPAP(t *testing.T) {
	setup(t)
	c := ttlsPAPClient("alice", "Geheim123!")
	r := eapLogin(t, eapTypeTTLS, []byte{eapTypeTTLS}, c)
	expectAccept(t, r, c, "EAP-TTLS/PAP")

	c = ttlsPAPClient("alice", "falsch")
	expectReject(t, eapLogin(t, eapTypeTTLS, []byte{eapTypeTTLS}, c), "Falsches Passwort")
	if ev := lastEvent(t); ev.Method != "EAP-TTLS/PAP" || ev.User != "alice" {
		t.Fatalf("Protokoll: %q %q", ev.Method, ev.User)
	}

	c = ttlsPAPClient("bob", "x")
	expectReject(t, eapLogin(t, eapTypeTTLS, []byte{eapTypeTTLS}, c), "deaktiviert")
}

func TestTTLSMSCHAPv2(t *testing.T) {
	setup(t)
	c := ttlsMSCHAPClient("alice", "Geheim123!", false)
	r := eapLogin(t, eapTypeTTLS, []byte{eapTypeTTLS}, c)
	expectAccept(t, r, c, "EAP-TTLS/MSCHAPv2")
	if !c.authOK {
		t.Fatal("Client hat den Server nicht verifiziert")
	}

	c = ttlsMSCHAPClient("alice", "falsch", false)
	expectReject(t, eapLogin(t, eapTypeTTLS, []byte{eapTypeTTLS}, c), "Falsches Passwort")
	if c.authOK {
		t.Fatal("Client meldet Erfolg trotz falschem Passwort")
	}

	// Challenge, die nicht aus dem Tunnel abgeleitet ist, muss abgelehnt werden.
	c = ttlsMSCHAPClient("alice", "Geheim123!", true)
	expectReject(t, eapLogin(t, eapTypeTTLS, []byte{eapTypeTTLS}, c), "Challenge passt nicht")
}

func TestTTLSInnerEAPMSCHAPv2(t *testing.T) {
	setup(t)
	c := ttlsEAPMSCHAPClient("alice", "Geheim123!")
	r := eapLogin(t, eapTypeTTLS, []byte{eapTypeTTLS}, c)
	expectAccept(t, r, c, "EAP-TTLS/EAP-MSCHAPv2")
	if !c.authOK {
		t.Fatal("Client hat den Server nicht verifiziert")
	}
	c = ttlsEAPMSCHAPClient("alice", "falsch")
	expectReject(t, eapLogin(t, eapTypeTTLS, []byte{eapTypeTTLS}, c), "Falsches Passwort")
}

func TestNAKSwitchesToTTLS(t *testing.T) {
	setup(t)
	// Ablauf von Hand: Identity → PEAP-Start → NAK(21) → TTLS-Start
	send := func(state, eap []byte) *radius.Packet {
		p := radius.New(radius.CodeAccessRequest, []byte(testSecret))
		rfc2865.UserName_SetString(p, "anonymous")
		if state != nil {
			rfc2865.State_Set(p, state)
		}
		setEAP(p, eap)
		addMA(p)
		return exchange(t, p)
	}
	r := send(nil, buildEAP(eapResponse, 5, eapTypeIdentity, []byte("anonymous")))
	e := eapMessage(r)
	if r.Code != radius.CodeAccessChallenge || e[4] != eapTypePEAP || e[5] != peapFlagStart {
		t.Fatalf("PEAP-Start erwartet: %v %x", r.Code, e)
	}
	// Bevorzugt EAP-TLS (aus), dann TTLS.
	r = send(rfc2865.State_Get(r), buildEAP(eapResponse, e[1], eapTypeNak, []byte{eapTypeTLS, eapTypeTTLS}))
	e = eapMessage(r)
	if r.Code != radius.CodeAccessChallenge || len(e) != 6 || e[4] != eapTypeTTLS || e[5] != 0x20 || e[1] != 7 {
		t.Fatalf("TTLS-Start (Flags 0x20, Version 0, ID 7) erwartet: %v %x", r.Code, e)
	}
	// Ein zweites NAK (nichts Passendes mehr) führt zur Ablehnung mit Hinweis.
	r = send(rfc2865.State_Get(r), buildEAP(eapResponse, e[1], eapTypeNak, []byte{eapTypeTTLS}))
	expectReject(t, r, "TTLS")

	// TTLS ausgeschaltet: NAK(21) wird abgelehnt.
	testServer.Store.UpdateSettings(func(s *store.Settings) { s.EAP.TTLS = false })
	c := ttlsPAPClient("alice", "Geheim123!")
	r = eapLogin(t, eapTypeTTLS, []byte{eapTypeTTLS}, c)
	expectReject(t, r, "EAP-TTLS in den Einstellungen ausgeschaltet")
	if strings.Contains(reason(r), "„TTLS“") {
		t.Fatalf("Hinweis nennt ausgeschaltetes TTLS: %q", reason(r))
	}
}

// ---- EAP-TLS ----

func issueTestCert(t *testing.T, s *Server, user string) (*tls.Certificate, string) {
	t.Helper()
	p12, serial, err := s.Certs.IssueClientCert(user, 30, "Export!1")
	if err != nil {
		t.Fatal(err)
	}
	key, cert, cas, err := pkcs12.DecodeChain(p12, "Export!1")
	if err != nil {
		t.Fatal(err)
	}
	if cert.Subject.CommonName != user || len(cas) != 1 || !cas[0].IsCA {
		t.Fatalf("PKCS#12-Inhalt: CN=%q, %d CA-Zertifikate", cert.Subject.CommonName, len(cas))
	}
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Fatalf("EKU %v", cert.ExtKeyUsage)
	}
	return &tls.Certificate{Certificate: [][]byte{cert.Raw}, PrivateKey: key, Leaf: cert}, serial
}

func TestEAPTLS(t *testing.T) {
	s := setup(t)
	nak := []byte{eapTypeTLS}

	// Ausgeschaltet (Standard): Ablehnung mit Hinweis.
	cert, serial := issueTestCert(t, s, "alice")
	expectReject(t, eapLogin(t, eapTypeTLS, nak, eapTLSClient(cert)), "EAP-TLS in den Einstellungen ausgeschaltet")

	s.Store.UpdateSettings(func(x *store.Settings) { x.EAP.TLS = true })
	c := eapTLSClient(cert)
	r := eapLogin(t, eapTypeTLS, nak, c)
	expectAccept(t, r, c, "EAP-TLS")

	// Ohne Zertifikat
	expectReject(t, eapLogin(t, eapTypeTLS, nak, eapTLSClient(nil)), "TLS-Fehler")

	// Unbekannter Benutzer
	other, _ := issueTestCert(t, s, "mallory")
	expectReject(t, eapLogin(t, eapTypeTLS, nak, eapTLSClient(other)), "Kein lokaler Benutzer")

	// Deaktivierter Benutzer
	bobCert, _ := issueTestCert(t, s, "bob")
	expectReject(t, eapLogin(t, eapTypeTLS, nak, eapTLSClient(bobCert)), "deaktiviert")

	// Widerrufen
	if err := s.Certs.RevokeClientCert(serial); err != nil {
		t.Fatal(err)
	}
	if !s.Certs.IsRevoked(serial) {
		t.Fatal("IsRevoked meldet nicht gesperrt")
	}
	expectReject(t, eapLogin(t, eapTypeTLS, nak, eapTLSClient(cert)), "widerrufen")

	list := s.Certs.ClientCerts()
	if len(list) != 3 {
		t.Fatalf("%d Zertifikate gelistet", len(list))
	}
	revoked := 0
	for _, ci := range list {
		if ci.Revoked {
			revoked++
			if ci.Serial != serial || ci.User != "alice" {
				t.Fatalf("falsches Zertifikat gesperrt: %+v", ci)
			}
		}
	}
	if revoked != 1 {
		t.Fatalf("%d gesperrte Zertifikate", revoked)
	}

	// Ein neues Zertifikat für denselben Benutzer funktioniert weiterhin.
	cert2, _ := issueTestCert(t, s, "alice")
	c = eapTLSClient(cert2)
	expectAccept(t, eapLogin(t, eapTypeTLS, nak, c), c, "EAP-TLS")
}
