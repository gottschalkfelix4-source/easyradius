package radiusd

import (
	"encoding/hex"
	"testing"

	"github.com/gottschalkfelix4-source/easyradius/internal/attrs"
	"github.com/gottschalkfelix4-source/easyradius/internal/auth"
	"github.com/gottschalkfelix4-source/easyradius/internal/store"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2868"
)

func vsa(r *radius.Packet, vendor uint32, code byte) []byte {
	for _, a := range r.Attributes {
		if a.Type != rfc2865.VendorSpecific_Type {
			continue
		}
		id, v, err := radius.VendorSpecific(a.Attribute)
		if err == nil && id == vendor && len(v) >= 2 && v[0] == code {
			return v[2:v[1]]
		}
	}
	return nil
}

func TestReplyAttributes(t *testing.T) {
	s := setup(t)
	zyAdmin := attrs.Presets[1].Attrs // Zyxel-Switch: Administrator
	g, err := s.Store.SaveGroup(store.Group{Name: "Netzwerk-Admins", Enabled: true, Attributes: append([]attrs.Attr{},
		append(zyAdmin, attrs.Attr{Name: "Session-Timeout", Code: 27, Type: attrs.Integer, Value: "3600"})...)})
	if err != nil {
		t.Fatal(err)
	}
	// Benutzer überschreibt Session-Timeout und bringt eigenes VSA mit.
	_, err = s.Store.SaveUser(store.User{Username: "netadmin", NTHash: hex.EncodeToString(auth.NTHash("pw-na")), Enabled: true,
		GroupID: g.ID, VLAN: "99", Attributes: []attrs.Attr{
			{Name: "Session-Timeout", Code: 27, Type: attrs.Integer, Value: "600"},
			{Name: "Eigenes VSA", VendorID: 4242, Code: 7, Type: attrs.String, Value: "hallo"},
		}})
	if err != nil {
		t.Fatal(err)
	}
	r := papReq(t, "netadmin", "pw-na", "")
	if r.Code != radius.CodeAccessAccept {
		t.Fatalf("%v %s", r.Code, reason(r))
	}
	if st := rfc2865.ServiceType_Get(r); st != rfc2865.ServiceType_Value_AdministrativeUser {
		t.Errorf("Service-Type = %v", st)
	}
	if v := string(vsa(r, 890, 3)); v != "shell:priv-lvl=14" {
		t.Errorf("Zyxel-Privilege-AVPair = %q", v)
	}
	if v := string(vsa(r, 4242, 7)); v != "hallo" {
		t.Errorf("eigenes VSA = %q", v)
	}
	if n := count(r, rfc2865.SessionTimeout_Type); n != 1 || rfc2865.SessionTimeout_Get(r) != 600 {
		t.Errorf("Session-Timeout: %d Stück, Wert %v", n, rfc2865.SessionTimeout_Get(r))
	}
	if vlanOf(r) != "99" {
		t.Errorf("Benutzer-VLAN fehlt: %q", vlanOf(r))
	}

	// Eigene Tunnel-Attribute ersetzen das einfache VLAN-Feld.
	u, _ := s.Store.FindUser("netadmin")
	u.Attributes = attrs.Presets[0].Attrs // 802.1X VLAN 10
	if _, err := s.Store.SaveUser(u); err != nil {
		t.Fatal(err)
	}
	r = papReq(t, "netadmin", "pw-na", "")
	if vlanOf(r) != "10" || count(r, rfc2868.TunnelPrivateGroupID_Type) != 1 {
		t.Errorf("Tunnel-Attribute: vlan=%q", vlanOf(r))
	}
	if _, typ := rfc2868.TunnelType_Get(r); typ != 13 {
		t.Errorf("Tunnel-Type = %v", typ)
	}
}

func TestAttrValidation(t *testing.T) {
	bad := []attrs.Attr{
		{Name: "EAP", Code: 79, Type: attrs.Octets, Value: "00"},
		{Name: "Session-Timeout", Code: 27, Type: attrs.Integer, Value: "abc"},
		{Name: "Framed-IP-Address", Code: 8, Type: attrs.IPAddr, Value: "999.1.1.1"},
		{Name: "MPPE", VendorID: 311, Code: 16, Type: attrs.Octets, Value: "00"},
	}
	for _, a := range bad {
		if _, err := attrs.Normalize(a); err == nil {
			t.Errorf("%s wurde akzeptiert", a.Name)
		}
	}
	ok := attrs.Attr{Name: "Service-Type", Code: 6, Type: attrs.Integer, Value: "administrative-user"}
	if _, err := attrs.Normalize(ok); err != nil {
		t.Error(err)
	}
	if v, _ := ok.Encode(); hex.EncodeToString(v) != "00000006" {
		t.Errorf("Service-Type kodiert als %x", v)
	}
}

func count(r *radius.Packet, t radius.Type) int {
	n := 0
	for _, a := range r.Attributes {
		if a.Type == t {
			n++
		}
	}
	return n
}
