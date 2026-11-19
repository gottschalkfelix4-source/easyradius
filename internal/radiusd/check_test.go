package radiusd

import (
	"encoding/hex"
	"net"
	"strings"
	"testing"

	"github.com/gottschalkfelix4-source/easyradius/internal/attrs"
	"github.com/gottschalkfelix4-source/easyradius/internal/auth"
	"github.com/gottschalkfelix4-source/easyradius/internal/store"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
)

func checkAttr(name, value string) attrs.Attr {
	for _, d := range attrs.Dictionary {
		if d.Name == name {
			return attrs.Attr{Kind: attrs.Check, Name: d.Name, VendorID: d.VendorID, Code: d.Code, Type: d.Type, Value: value}
		}
	}
	panic(name)
}

func TestCheckAttributes(t *testing.T) {
	s := setup(t)
	g, _ := s.Store.SaveGroup(store.Group{Name: "Switch-Admins", Enabled: true, Attributes: []attrs.Attr{
		checkAttr("NAS-Port-Type", "Virtual"),                // nur SSH/Web-Login, kein 802.1X
		checkAttr("Calling-Station-Id", "02-00-00-*"),        // nur bestimmte Geräte
		checkAttr("NAS-IP-Address", "127.0.0.0/8"),           // nur Switches aus diesem Netz
		attrs.Presets[1].Attrs[0], attrs.Presets[1].Attrs[1], // Zyxel-Admin (Reply)
	}})
	if _, err := s.Store.SaveUser(store.User{Username: "ops", NTHash: hex.EncodeToString(auth.NTHash("pw-ops")), Enabled: true, GroupID: g.ID}); err != nil {
		t.Fatal(err)
	}

	send := func(mod func(p *radius.Packet)) *radius.Packet {
		p := radius.New(radius.CodeAccessRequest, []byte(testSecret))
		rfc2865.UserName_SetString(p, "ops")
		rfc2865.UserPassword_SetString(p, "pw-ops")
		rfc2865.NASIPAddress_Set(p, net.IPv4(127, 0, 0, 1))
		rfc2865.CallingStationID_SetString(p, "02-00-00-11-22-33")
		rfc2865.NASPortType_Set(p, rfc2865.NASPortType_Value_Virtual)
		if mod != nil {
			mod(p)
		}
		return exchange(t, p)
	}

	r := send(nil)
	if r.Code != radius.CodeAccessAccept {
		t.Fatalf("passende Anfrage abgelehnt: %s", reason(r))
	}
	if rfc2865.ServiceType_Get(r) != rfc2865.ServiceType_Value_AdministrativeUser {
		t.Error("Reply-Attribut fehlt")
	}
	for _, c := range attrs.Dictionary {
		if c.Use == "check" && c.Name == "NAS-Port-Type" && rfc2865.NASPortType_Get(r) != 0 {
			t.Error("Check-Attribut wurde zurückgeschickt")
		}
	}

	r = send(func(p *radius.Packet) { rfc2865.NASPortType_Set(p, rfc2865.NASPortType_Value_Ethernet) })
	if r.Code != radius.CodeAccessReject || !strings.Contains(reason(r), "erhalten Ethernet") {
		t.Fatalf("falscher Port-Typ: %v %s", r.Code, reason(r))
	}
	r = send(func(p *radius.Packet) { rfc2865.CallingStationID_SetString(p, "AA-BB-CC-DD-EE-FF") })
	if r.Code != radius.CodeAccessReject {
		t.Fatal("falsche MAC akzeptiert")
	}
	r = send(func(p *radius.Packet) { rfc2865.NASPortType_Del(p) })
	if r.Code != radius.CodeAccessReject || !strings.Contains(reason(r), "nicht vorhanden") {
		t.Fatal("fehlendes Attribut akzeptiert")
	}

	// Benutzer-Check ersetzt den gleichnamigen der Gruppe; Verneinung mit „!“.
	u, _ := s.Store.FindUser("ops")
	u.Attributes = []attrs.Attr{checkAttr("NAS-Port-Type", "!Wireless-802.11")}
	if _, err := s.Store.SaveUser(u); err != nil {
		t.Fatal(err)
	}
	if r := send(func(p *radius.Packet) { rfc2865.NASPortType_Set(p, rfc2865.NASPortType_Value_Ethernet) }); r.Code != radius.CodeAccessAccept {
		t.Fatalf("Benutzer-Check überschreibt Gruppe nicht: %s", reason(r))
	}
	if r := send(func(p *radius.Packet) { rfc2865.NASPortType_Set(p, rfc2865.NASPortType_Value_Wireless80211) }); r.Code != radius.CodeAccessReject {
		t.Fatal("Verneinung greift nicht")
	}
}

func TestCheckValidation(t *testing.T) {
	if _, err := attrs.Normalize(attrs.Attr{Kind: attrs.Check, Name: "User-Password", Code: 2, Type: attrs.String, Value: "x"}); err == nil {
		t.Error("User-Password als Check akzeptiert")
	}
	if _, err := attrs.Normalize(checkAttr("Called-Station-Id", "*:Firma")); err != nil {
		t.Error(err)
	}
}
