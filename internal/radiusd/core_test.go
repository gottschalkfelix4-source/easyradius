package radiusd

import (
	"context"
	"encoding/hex"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gottschalkfelix4-source/easyradius/internal/auth"
	"github.com/gottschalkfelix4-source/easyradius/internal/store"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2866"
	"layeh.com/radius/rfc2868"
	"layeh.com/radius/rfc2869"
)

// noAnswer erwartet, dass der Server das Paket still verwirft.
func noAnswer(t *testing.T, p *radius.Packet) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	if r, err := radius.Exchange(ctx, p, testAddr); err == nil {
		t.Fatalf("Antwort erhalten, erwartet war keine: %v", r.Code)
	}
}

func TestControlCharactersInName(t *testing.T) {
	setup(t)
	if r := papReq(t, "ali\x00ce", "Geheim123!", ""); r.Code != radius.CodeAccessReject {
		t.Fatal("Name mit NUL akzeptiert")
	}
	if r := papReq(t, "alice", "Geheim123!", ""); r.Code != radius.CodeAccessAccept {
		t.Fatal("Server nach NUL-Name nicht mehr funktionsfähig")
	}
}

func TestGenericRejectMessage(t *testing.T) {
	setup(t)
	r := papReq(t, "nobody", "x", "")
	if got := rfc2865.ReplyMessage_GetString(r); got != "Anmeldung abgelehnt" {
		t.Fatalf("Reply-Message verrät Details: %q", got)
	}
	if !strings.Contains(reason(r), "unbekannt") {
		t.Fatalf("Grund fehlt im Protokoll: %q", reason(r))
	}
}

// Ein AD-Benutzer in einer gesperrten, verknüpften Gruppe darf nicht durchrutschen,
// auch wenn „nur mit verknüpfter Gruppe“ ausgeschaltet ist.
func TestADBlockedGroupWithoutRequireGroup(t *testing.T) {
	s := setup(t)
	s.dirOverride = fakeAD{}
	s.Store.UpdateSettings(func(c *store.Settings) { c.AD.Mode, c.AD.RequireGroup = "domain", false })
	if r := papReq(t, `FIRMA\erika`, adPass, ""); r.Code != radius.CodeAccessAccept {
		t.Fatalf("ohne verknüpfte Gruppe sollte Zugang erlaubt sein: %s", reason(r))
	}
	s.Store.SaveGroup(store.Group{Name: "WLAN", Enabled: false, ADGroups: []string{"WLAN-Mitarbeiter"}})
	if r := papReq(t, `FIRMA\erika`, adPass, ""); r.Code != radius.CodeAccessReject {
		t.Fatal("gesperrte AD-Gruppe wurde umgangen")
	}
}

func TestLockout(t *testing.T) {
	s := setup(t)
	s.Store.UpdateSettings(func(c *store.Settings) { c.Lockout = store.LockoutSettings{Enabled: true, Attempts: 3, Minutes: 5} })
	for i := 0; i < 3; i++ {
		papReq(t, "alice", "falsch", "")
	}
	r := papReq(t, "alice", "Geheim123!", "")
	if r.Code != radius.CodeAccessReject || !strings.Contains(reason(r), "gesperrt") {
		t.Fatalf("Sperre greift nicht: %v %s", r.Code, reason(r))
	}
	if len(s.Lockouts()) == 0 {
		t.Fatal("Sperre nicht gelistet")
	}
	s.Unlock("")
	if r := papReq(t, "alice", "Geheim123!", ""); r.Code != radius.CodeAccessAccept {
		t.Fatal("nach Entsperren abgelehnt")
	}
	// Selbsttest zählt nicht.
	for i := 0; i < 5; i++ {
		s.TestLogin("alice", "falsch")
	}
	if len(s.Lockouts()) != 0 {
		t.Fatal("Selbsttest hat gesperrt")
	}
}

func TestMACAuthentication(t *testing.T) {
	s := setup(t)
	g, _ := s.Store.SaveGroup(store.Group{Name: "Drucker", Enabled: true, VLAN: "50"})
	if _, err := s.Store.SaveMAC(store.MACEntry{MAC: "00:11:22:AA:BB:CC", Name: "Drucker Flur", Enabled: true, GroupID: g.ID}); err != nil {
		t.Fatal(err)
	}
	mab := func(user, station string) *radius.Packet {
		p := radius.New(radius.CodeAccessRequest, []byte(testSecret))
		rfc2865.UserName_SetString(p, user)
		rfc2865.UserPassword_SetString(p, user)
		rfc2865.ServiceType_Set(p, rfc2865.ServiceType_Value_CallCheck)
		if station != "" {
			rfc2865.CallingStationID_SetString(p, station)
		}
		return exchange(t, p)
	}
	if r := mab("001122aabbcc", "00-11-22-AA-BB-CC"); r.Code != radius.CodeAccessAccept || vlanOf(r) != "50" {
		t.Fatalf("MAB: %v vlan=%q %s", r.Code, vlanOf(r), reason(r))
	}
	if r := mab("001122aabbcc", "00-11-22-AA-BB-CD"); r.Code != radius.CodeAccessReject {
		t.Fatal("MAB mit abweichender Calling-Station-Id akzeptiert")
	}
	if r := mab("001122aabbcd", ""); r.Code != radius.CodeAccessReject {
		t.Fatal("unbekannte MAC akzeptiert")
	}
}

func TestRequireMessageAuthenticator(t *testing.T) {
	s := setup(t)
	c := s.Store.Clients()[0]
	c.RequireMA = true
	s.Store.SaveClient(c)

	p := radius.New(radius.CodeAccessRequest, []byte(testSecret))
	rfc2865.UserName_SetString(p, "alice")
	rfc2865.UserPassword_SetString(p, "Geheim123!")
	noAnswer(t, p)

	p = radius.New(radius.CodeAccessRequest, []byte(testSecret))
	rfc2865.UserName_SetString(p, "alice")
	rfc2865.UserPassword_SetString(p, "Geheim123!")
	rfc2865.ProxyState_Add(p, []byte("ps-1"))
	addMA(p)
	r := exchange(t, p)
	if r.Code != radius.CodeAccessAccept {
		t.Fatalf("mit Message-Authenticator abgelehnt: %s", reason(r))
	}
	if r.Attributes[0].Type != rfc2869.MessageAuthenticator_Type {
		t.Error("Message-Authenticator steht nicht an erster Stelle")
	}
	if string(rfc2865.ProxyState_Get(r)) != "ps-1" {
		t.Error("Proxy-State nicht zurückgegeben")
	}
}

func TestStatusServerNeedsMA(t *testing.T) {
	setup(t)
	p := radius.New(radius.CodeStatusServer, []byte(testSecret))
	noAnswer(t, p)
	p = radius.New(radius.CodeStatusServer, []byte(testSecret))
	addMA(p)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, err := radius.Exchange(ctx, p, testAddr)
	if err != nil || r.Code != radius.CodeAccessAccept {
		t.Fatalf("Status-Server: %v %v", r, err)
	}
}

func TestVLANWithoutTagByte(t *testing.T) {
	setup(t)
	r := papReq(t, "alice", "Geheim123!", "")
	var raw []byte
	for _, a := range r.Attributes {
		if a.Type == rfc2868.TunnelPrivateGroupID_Type {
			raw = a.Attribute
		}
	}
	if string(raw) != "20" {
		t.Fatalf("Tunnel-Private-Group-Id = %q, erwartet \"20\" ohne Tag-Byte", raw)
	}
}

func TestStoreSecretsEncrypted(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.SaveClient(store.Client{Name: "AP", Address: "10.0.0.1", Secret: "super-geheim-123"})
	st.SaveUser(store.User{Username: "u", NTHash: hex.EncodeToString(auth.NTHash("pw")), Enabled: true})
	b, _ := readFile(dir + "/config.json")
	if strings.Contains(b, "super-geheim-123") || strings.Contains(b, hex.EncodeToString(auth.NTHash("pw"))) {
		t.Fatal("Geheimnisse stehen im Klartext in config.json")
	}
	st2, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st2.Clients()[0].Secret != "super-geheim-123" {
		t.Fatal("Secret nach erneutem Laden falsch")
	}
}

func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}

func TestAccountingAndDisconnect(t *testing.T) {
	s := setup(t)
	// Gerät, das Disconnect-Requests beantwortet.
	nas, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer nas.Close()
	gotMA := make(chan bool, 1)
	go func() {
		buf := make([]byte, 4096)
		n, addr, err := nas.ReadFrom(buf)
		if err != nil {
			return
		}
		if !radius.IsAuthenticRequest(buf[:n], []byte(testSecret)) {
			return
		}
		_, valid := checkMessageAuthenticator(buf[:n], []byte(testSecret))
		gotMA <- valid
		req, _ := radius.Parse(buf[:n], []byte(testSecret))
		out, _ := req.Response(radius.CodeDisconnectACK).Encode()
		nas.WriteTo(out, addr)
	}()
	c := s.Store.Clients()[0]
	c.CoAPort = nas.LocalAddr().(*net.UDPAddr).Port
	s.Store.SaveClient(c)

	acct := func(typ rfc2866.AcctStatusType) {
		p := radius.New(radius.CodeAccountingRequest, []byte(testSecret))
		rfc2865.UserName_SetString(p, "alice")
		rfc2866.AcctSessionID_SetString(p, "S1")
		rfc2866.AcctStatusType_Set(p, typ)
		rfc2866.AcctSessionTime_Set(p, 90)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := radius.Exchange(ctx, p, "127.0.0.1:18130"); err != nil {
			t.Fatal(err)
		}
	}
	acct(rfc2866.AcctStatusType_Value_Start)
	on := s.Online()
	if len(on) != 1 {
		t.Fatalf("online = %d", len(on))
	}
	if err := s.Disconnect(on[0].Key, "Test"); err != nil {
		t.Fatal(err)
	}
	if !<-gotMA {
		t.Error("Disconnect-Request ohne gültigen Message-Authenticator")
	}
	if len(s.Online()) != 0 {
		t.Error("Sitzung nach Trennen noch online")
	}
	acct(rfc2866.AcctStatusType_Value_Stop)
}

func TestAccountingLog(t *testing.T) {
	s := setup(t)
	s.DataDir = t.TempDir()
	s.Restart()
	p := radius.New(radius.CodeAccountingRequest, []byte(testSecret))
	rfc2865.UserName_SetString(p, "alice")
	rfc2866.AcctSessionID_SetString(p, "S2")
	rfc2866.AcctStatusType_Set(p, rfc2866.AcctStatusType_Value_Stop)
	rfc2866.AcctSessionTime_Set(p, 60)
	rfc2866.AcctInputOctets_Set(p, 1234)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := radius.Exchange(ctx, p, "127.0.0.1:18130"); err != nil {
		t.Fatal(err)
	}
	m := s.AccountingMonths()
	if len(m) != 1 {
		t.Fatalf("Monate: %v", m)
	}
	recs := s.Accounting(m[0])
	if len(recs) != 1 || recs[0].User != "alice" || recs[0].InBytes != 1234 || recs[0].Seconds != 60 {
		t.Fatalf("Einträge: %+v", recs)
	}
}
