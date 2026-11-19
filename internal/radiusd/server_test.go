package radiusd

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gottschalkfelix4-source/easyradius/internal/auth"
	"github.com/gottschalkfelix4-source/easyradius/internal/certs"
	"github.com/gottschalkfelix4-source/easyradius/internal/events"
	"github.com/gottschalkfelix4-source/easyradius/internal/store"

	"layeh.com/radius"
	"layeh.com/radius/rfc2759"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2868"
	"layeh.com/radius/rfc2869"
	"layeh.com/radius/vendors/microsoft"
)

const (
	testSecret = "testing123"
	testAddr   = "127.0.0.1:18120"
)

func setup(t *testing.T) *Server {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.UpdateSettings(func(s *store.Settings) { s.AuthPort, s.AcctPort = 18120, 18130 })
	lg, _ := events.Open(dir, 100)
	cm, err := certs.NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveClient(store.Client{Name: "Test-AP", Address: "127.0.0.0/8", Secret: testSecret}); err != nil {
		t.Fatal(err)
	}
	st.SaveUser(store.User{Username: "alice", NTHash: hex.EncodeToString(auth.NTHash("Geheim123!")), Enabled: true, VLAN: "20"})
	st.SaveUser(store.User{Username: "bob", NTHash: hex.EncodeToString(auth.NTHash("x")), Enabled: false})
	s := New(st, lg, cm)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop(); lg.Close() })
	return s
}

var lastReq *radius.Packet

func exchange(t *testing.T, p *radius.Packet) *radius.Packet {
	t.Helper()
	lastReq = p
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := radius.Exchange(ctx, p, testAddr)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if r.Code == radius.CodeAccessAccept || r.Code == radius.CodeAccessChallenge || r.Code == radius.CodeAccessReject {
		raw, _ := r.MarshalBinary()
		// Message-Authenticator der Antwort prüfen (Authenticator = Request-Authenticator).
		copy(raw[4:20], p.Authenticator[:])
		if ok, valid := checkMessageAuthenticator(raw, []byte(testSecret)); !ok || !valid {
			t.Fatalf("Antwort ohne gültigen Message-Authenticator (vorhanden=%v)", ok)
		}
	}
	return r
}

func TestPAP(t *testing.T) {
	setup(t)
	cases := []struct {
		user, pw string
		want     radius.Code
	}{
		{"alice", "Geheim123!", radius.CodeAccessAccept},
		{"ALICE", "Geheim123!", radius.CodeAccessAccept},
		{"alice", "falsch", radius.CodeAccessReject},
		{"bob", "x", radius.CodeAccessReject},
		{"nobody", "x", radius.CodeAccessReject},
	}
	for _, c := range cases {
		p := radius.New(radius.CodeAccessRequest, []byte(testSecret))
		rfc2865.UserName_SetString(p, c.user)
		rfc2865.UserPassword_SetString(p, c.pw)
		r := exchange(t, p)
		if r.Code != c.want {
			t.Errorf("%s/%s: %v, erwartet %v (%s)", c.user, c.pw, r.Code, c.want, rfc2865.ReplyMessage_GetString(r))
		}
		if r.Code == radius.CodeAccessAccept {
			if _, g := rfc2868.TunnelPrivateGroupID_GetString(r); g != "20" {
				t.Errorf("VLAN fehlt: %q", g)
			}
		}
	}
}

func TestMSCHAPv2(t *testing.T) {
	setup(t)
	for _, pw := range []string{"Geheim123!", "falsch"} {
		authC, peerC := make([]byte, 16), make([]byte, 16)
		rand.Read(authC)
		rand.Read(peerC)
		nt, _ := rfc2759.GenerateNTResponse(authC, peerC, []byte("alice"), []byte(pw))
		p := radius.New(radius.CodeAccessRequest, []byte(testSecret))
		rfc2865.UserName_SetString(p, "alice")
		microsoft.MSCHAPChallenge_Add(p, authC)
		resp := append([]byte{7, 0}, peerC...)
		resp = append(resp, make([]byte, 8)...)
		resp = append(resp, nt...)
		microsoft.MSCHAP2Response_Add(p, resp)
		r := exchange(t, p)
		if pw == "falsch" {
			if r.Code != radius.CodeAccessReject {
				t.Fatalf("falsches Passwort akzeptiert")
			}
			continue
		}
		if r.Code != radius.CodeAccessAccept {
			t.Fatalf("MS-CHAPv2: %v %s", r.Code, rfc2865.ReplyMessage_GetString(r))
		}
		want, _ := rfc2759.GenerateAuthenticatorResponse(authC, peerC, nt, []byte("alice"), []byte(pw))
		if got := microsoft.MSCHAP2Success_Get(r); string(got[1:]) != want {
			t.Fatalf("Success %q, erwartet %q", got[1:], want)
		}
	}
}

// ---- PEAP-Client für den Test ----

type peapClient struct {
	t      *testing.T
	conn   *tunnelConn
	done   chan struct{}
	err    error
	msk    []byte
	user   string
	pw     string
	authOK bool
}

func (c *peapClient) run() {
	defer close(c.done)
	tc := tls.Client(c.conn, &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS12})
	if err := tc.Handshake(); err != nil {
		c.err = err
		return
	}
	cs := tc.ConnectionState()
	c.msk, _ = cs.ExportKeyingMaterial("client EAP encryption", nil, 128)
	buf := make([]byte, 4096)
	read := func() []byte {
		n, err := tc.Read(buf)
		if err != nil {
			c.err = err
			return nil
		}
		return append([]byte(nil), buf[:n]...)
	}
	// Identität
	r := read()
	if r == nil || r[0] != eapTypeIdentity {
		c.err = fmt.Errorf("Identity-Request erwartet: %x", r)
		return
	}
	tc.Write(append([]byte{eapTypeIdentity}, c.user...))
	// MS-CHAPv2 Challenge
	r = read()
	if r == nil || r[0] != eapTypeMSCHAPv2 || r[1] != 1 {
		c.err = fmt.Errorf("Challenge erwartet: %x", r)
		return
	}
	msid, authC := r[2], r[6:22]
	peerC := make([]byte, 16)
	rand.Read(peerC)
	nt, _ := rfc2759.GenerateNTResponse(authC, peerC, []byte(hashName(c.user)), []byte(c.pw))
	val := append(append(append(append([]byte{}, peerC...), make([]byte, 8)...), nt...), 0)
	msLen := 4 + 1 + 49 + len(c.user)
	resp := []byte{eapTypeMSCHAPv2, 2, msid, byte(msLen >> 8), byte(msLen), 49}
	resp = append(append(resp, val...), c.user...)
	tc.Write(resp)
	r = read()
	if r == nil {
		return
	}
	switch r[1] {
	case 3:
		want, _ := rfc2759.GenerateAuthenticatorResponse(authC, peerC, nt, []byte(hashName(c.user)), []byte(c.pw))
		if !bytes.HasPrefix(r[5:], []byte(want)) {
			c.err = fmt.Errorf("Server-Authenticator falsch: %q", r[5:])
			return
		}
		c.authOK = true
		tc.Write([]byte{eapTypeMSCHAPv2, 3})
	case 4:
		tc.Write([]byte{eapTypeMSCHAPv2, 4})
	}
	// Result-TLV
	r = read()
	if r == nil || r[4] != eapTypeTLV {
		c.err = fmt.Errorf("TLV erwartet: %x", r)
		return
	}
	tc.Write([]byte{eapResponse, r[1], 0, 11, eapTypeTLV, 0x80, 3, 0, 2, 0, r[10]})
	read() // wartet, bis der Server das Ergebnis schickt (endet mit Abbruch)
}

func addMA(p *radius.Packet) {
	p.Add(rfc2869.MessageAuthenticator_Type, make(radius.Attribute, 16))
	b, _ := p.MarshalBinary()
	m := hmac.New(md5.New, p.Secret)
	m.Write(b)
	for i, a := range p.Attributes {
		if a.Type == rfc2869.MessageAuthenticator_Type {
			p.Attributes[i].Attribute = m.Sum(nil)
		}
	}
}

func peapLogin(t *testing.T, user, pw string) (*radius.Packet, *peapClient) {
	c := &peapClient{t: t, conn: newTunnelConn(), done: make(chan struct{}), user: user, pw: pw}
	send := func(state []byte, eap []byte) *radius.Packet {
		p := radius.New(radius.CodeAccessRequest, []byte(testSecret))
		rfc2865.UserName_SetString(p, "anonymous")
		rfc2865.CallingStationID_SetString(p, "AA-BB-CC-DD-EE-FF")
		if state != nil {
			rfc2865.State_Set(p, state)
		}
		setEAP(p, eap)
		addMA(p)
		return exchange(t, p)
	}
	r := send(nil, buildEAP(eapResponse, 1, eapTypeIdentity, []byte("anonymous")))
	var inAccum []byte
	started := false
	for round := 0; round < 50; round++ {
		if r.Code != radius.CodeAccessChallenge {
			c.conn.abort()
			return r, c
		}
		eap := eapMessage(r)
		id, data := eap[1], eap[5:]
		flags, payload := data[0], data[1:]
		if flags&peapFlagLength != 0 {
			payload = payload[4:]
		}
		state := rfc2865.State_Get(r)
		var out []byte
		if flags&peapFlagStart != 0 {
			go c.run()
			<-c.conn.waiting
			started = true
			out = c.conn.takeOut()
		} else {
			inAccum = append(inAccum, payload...)
			if flags&peapFlagMore != 0 {
				r = send(state, buildEAP(eapResponse, id, eapTypePEAP, []byte{0}))
				continue
			}
			if !started {
				t.Fatal("kein Start")
			}
			msg := inAccum
			inAccum = nil
			c.conn.in <- msg
			select {
			case <-c.conn.waiting:
			case <-c.done:
				t.Fatalf("Client beendet: %v", c.err)
			}
			out = c.conn.takeOut()
		}
		if len(out) > fragmentSize {
			t.Fatalf("Client-Nachricht zu groß für Test: %d", len(out))
		}
		r = send(state, buildEAP(eapResponse, id, eapTypePEAP, append([]byte{0}, out...)))
	}
	t.Fatal("zu viele Runden")
	return nil, nil
}

func (c *tunnelConn) abort() {
	select {
	case <-c.kill:
	default:
		close(c.kill)
	}
}

func TestPEAP(t *testing.T) {
	setup(t)
	r, c := peapLogin(t, "alice", "Geheim123!")
	if r.Code != radius.CodeAccessAccept {
		t.Fatalf("PEAP: %v %s (Client-Fehler: %v)", r.Code, rfc2865.ReplyMessage_GetString(r), c.err)
	}
	if !c.authOK {
		t.Fatal("Client hat den Server nicht verifiziert")
	}
	eap := eapMessage(r)
	if len(eap) != 4 || eap[0] != eapSuccess {
		t.Fatalf("EAP-Success fehlt: %x", eap)
	}
	// Schlüssel müssen mit der Client-Seite übereinstimmen.
	if len(c.msk) != 128 {
		t.Fatal("Client-MSK fehlt")
	}
	if u := rfc2865.UserName_GetString(r); u != "alice" {
		t.Fatalf("User-Name %q", u)
	}
	if _, g := rfc2868.TunnelPrivateGroupID_GetString(r); g != "20" {
		t.Fatalf("VLAN %q", g)
	}
	checkKeys(t, r, c.msk)

	r, c = peapLogin(t, "alice", "falsch")
	if r.Code != radius.CodeAccessReject {
		t.Fatalf("falsches Passwort per PEAP akzeptiert")
	}
	if c.authOK {
		t.Fatal("Client meldet Erfolg trotz falschem Passwort")
	}
	if e := eapMessage(r); len(e) != 4 || e[0] != eapFailure {
		t.Fatalf("EAP-Failure fehlt: %x", e)
	}
}

// checkKeys entschlüsselt MS-MPPE-Recv/Send-Key und vergleicht mit der MSK des Clients.
func checkKeys(t *testing.T, r *radius.Packet, msk []byte) {
	t.Helper()
	recv, err1 := microsoft.MSMPPERecvKey_Lookup(r, lastReq)
	send, err2 := microsoft.MSMPPESendKey_Lookup(r, lastReq)
	if err1 != nil || err2 != nil {
		t.Fatalf("MPPE-Schlüssel: %v %v", err1, err2)
	}
	if !bytes.Equal(recv, msk[:32]) || !bytes.Equal(send, msk[32:64]) {
		t.Fatalf("MPPE-Schlüssel passen nicht zur MSK: recv=%x msk=%x", recv, msk[:64])
	}
}

func TestMessageAuthenticatorRoundtrip(t *testing.T) {
	p := radius.New(radius.CodeAccessRequest, []byte("abc"))
	rfc2865.UserName_SetString(p, "x")
	addMA(p)
	b, _ := p.Encode()
	if ok, valid := checkMessageAuthenticator(b, []byte("abc")); !ok || !valid {
		t.Fatal("MA-Prüfung schlägt fehl")
	}
	b[len(b)-1] ^= 1
	if _, valid := checkMessageAuthenticator(b, []byte("abc")); valid {
		t.Fatal("manipuliertes Paket akzeptiert")
	}
	if binary.BigEndian.Uint16(b[2:4]) != uint16(len(b)) {
		t.Fatal("Länge")
	}
	_ = errors.New
}

// hashName: Windows rechnet MS-CHAPv2 mit dem Namen ohne "DOMÄNE\" (RFC 2759).
func hashName(u string) string {
	if i := strings.LastIndex(u, `\`); i >= 0 {
		return u[i+1:]
	}
	return u
}
