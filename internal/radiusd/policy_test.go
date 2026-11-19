package radiusd

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/gottschalkfelix4-source/easyradius/internal/auth"
	"github.com/gottschalkfelix4-source/easyradius/internal/directory"
	"github.com/gottschalkfelix4-source/easyradius/internal/store"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2868"
)

// fakeAD verhält sich wie ein Domänencontroller mit einem Konto.
type fakeAD struct{}

const adPass = "AD-Passwort-1"

func (fakeAD) Describe() string { return "Test-AD" }
func (fakeAD) AuthPAP(name, pw string) (*directory.User, error) {
	if u, _, _ := directory.SplitName(name); !strings.EqualFold(u, "erika") || pw != adPass {
		return nil, directory.ErrBadCredentials
	}
	return &directory.User{Name: "erika", Domain: "FIRMA", Groups: []string{`FIRMA\Domänen-Benutzer`, `FIRMA\WLAN-Mitarbeiter`}}, nil
}
func (fakeAD) AuthMSCHAPv2(name string, ch8, nt []byte) (*directory.User, []byte, error) {
	if u, _, _ := directory.SplitName(name); !strings.EqualFold(u, "erika") {
		return nil, nil, directory.ErrBadCredentials
	}
	h := auth.NTHash(adPass)
	if hex.EncodeToString(auth.NTResponse(ch8, h)) != hex.EncodeToString(nt) {
		return nil, nil, directory.ErrBadCredentials
	}
	return &directory.User{Name: "erika", Groups: []string{`FIRMA\WLAN-Mitarbeiter`}}, auth.NTHashHash(h), nil
}

func papReq(t *testing.T, user, pw, called string) *radius.Packet {
	p := radius.New(radius.CodeAccessRequest, []byte(testSecret))
	rfc2865.UserName_SetString(p, user)
	rfc2865.UserPassword_SetString(p, pw)
	if called != "" {
		rfc2865.CalledStationID_SetString(p, called)
	}
	return exchange(t, p)
}

func vlanOf(r *radius.Packet) string { _, v := rfc2868.TunnelPrivateGroupID_GetString(r); return v }

func TestGroups(t *testing.T) {
	s := setup(t)
	g, err := s.Store.SaveGroup(store.Group{Name: "Mitarbeiter", Enabled: true, VLAN: "30", AllowedSSIDs: []string{"Firma"}})
	if err != nil {
		t.Fatal(err)
	}
	s.Store.SaveUser(store.User{Username: "carla", NTHash: hex.EncodeToString(auth.NTHash("pw-carla")), Enabled: true, GroupID: g.ID})

	if r := papReq(t, "carla", "pw-carla", "AA-BB-CC-DD-EE-FF:Firma"); r.Code != radius.CodeAccessAccept || vlanOf(r) != "30" {
		t.Fatalf("Gruppen-VLAN: %v vlan=%q %s", r.Code, vlanOf(r), rfc2865.ReplyMessage_GetString(r))
	}
	if r := papReq(t, "carla", "pw-carla", "aa:bb:cc:dd:ee:ff:Gaeste"); r.Code != radius.CodeAccessReject {
		t.Fatal("falsche SSID wurde akzeptiert")
	}
	if r := papReq(t, "carla", "pw-carla", ""); r.Code != radius.CodeAccessReject {
		t.Fatal("fehlende SSID wurde akzeptiert")
	}

	// Benutzer-VLAN hat Vorrang, Gerätebeschränkung greift.
	g.AllowedSSIDs = nil
	g.AllowedClients = []string{"anderes-geraet"}
	s.Store.SaveGroup(g)
	if r := papReq(t, "carla", "pw-carla", ""); r.Code != radius.CodeAccessReject || !strings.Contains(rfc2865.ReplyMessage_GetString(r), "Test-AP") {
		t.Fatalf("Gerätebeschränkung greift nicht: %v %s", r.Code, rfc2865.ReplyMessage_GetString(r))
	}
	g.AllowedClients = nil
	g.Enabled = false
	s.Store.SaveGroup(g)
	if r := papReq(t, "carla", "pw-carla", ""); r.Code != radius.CodeAccessReject || !strings.Contains(rfc2865.ReplyMessage_GetString(r), "gesperrt") {
		t.Fatal("gesperrte Gruppe wurde akzeptiert")
	}
	g.Enabled, g.ExpiresAt = true, "2000-01-01"
	s.Store.SaveGroup(g)
	if r := papReq(t, "carla", "pw-carla", ""); r.Code != radius.CodeAccessReject {
		t.Fatal("abgelaufene Gruppe wurde akzeptiert")
	}
	// Gruppe löschen → Benutzer ohne Gruppe wieder erlaubt.
	s.Store.DeleteGroup(g.ID)
	if r := papReq(t, "carla", "pw-carla", ""); r.Code != radius.CodeAccessAccept {
		t.Fatal("nach Löschen der Gruppe abgelehnt")
	}
}

func TestActiveDirectory(t *testing.T) {
	s := setup(t)
	s.dirOverride = fakeAD{}
	s.Store.UpdateSettings(func(c *store.Settings) { c.AD.Mode, c.AD.RequireGroup = "domain", true })

	// Ohne verknüpfte Gruppe abgelehnt.
	if r := papReq(t, `FIRMA\erika`, adPass, ""); r.Code != radius.CodeAccessReject || !strings.Contains(rfc2865.ReplyMessage_GetString(r), "verknüpft") {
		t.Fatalf("ohne Gruppe: %v %s", r.Code, rfc2865.ReplyMessage_GetString(r))
	}
	s.Store.SaveGroup(store.Group{Name: "WLAN", Enabled: true, VLAN: "40", ADGroups: []string{"WLAN-Mitarbeiter"}})

	if r := papReq(t, `FIRMA\erika`, adPass, ""); r.Code != radius.CodeAccessAccept || vlanOf(r) != "40" {
		t.Fatalf("AD-PAP: %v vlan=%q %s", r.Code, vlanOf(r), rfc2865.ReplyMessage_GetString(r))
	}
	if r := papReq(t, "erika@firma.local", "falsch", ""); r.Code != radius.CodeAccessReject {
		t.Fatal("falsches AD-Passwort akzeptiert")
	}

	// WLAN (PEAP-MSCHAPv2) mit AD-Konto.
	r, c := peapLogin(t, `FIRMA\erika`, adPass)
	if r.Code != radius.CodeAccessAccept || vlanOf(r) != "40" {
		t.Fatalf("AD-PEAP: %v vlan=%q %s (%v)", r.Code, vlanOf(r), rfc2865.ReplyMessage_GetString(r), c.err)
	}
	if !c.authOK {
		t.Fatal("Client hat die Serverantwort nicht verifiziert")
	}
	checkKeys(t, r, c.msk)
	if r, _ := peapLogin(t, `FIRMA\erika`, "falsch"); r.Code != radius.CodeAccessReject {
		t.Fatal("falsches AD-Passwort per PEAP akzeptiert")
	}

	// Lokale Benutzer haben Vorrang vor dem AD.
	if r := papReq(t, "alice", "Geheim123!", ""); r.Code != radius.CodeAccessAccept {
		t.Fatal("lokaler Benutzer abgelehnt")
	}

	// Selbsttest der Oberfläche.
	res := s.TestLogin(`FIRMA\erika`, adPass)
	if !res.OK || !res.WLANOK || res.Group != "WLAN" || res.VLAN != "40" {
		t.Fatalf("TestLogin: %+v", res)
	}
}

func TestSSIDParsing(t *testing.T) {
	for in, want := range map[string]string{
		"AA-BB-CC-DD-EE-FF:Firma":     "Firma",
		"aa:bb:cc:dd:ee:ff:Gast WLAN": "Gast WLAN",
		"AABBCCDDEEFF:x":              "x",
		"aa:bb:cc:dd:ee:ff":           "",
		"192.168.1.1":                 "",
		"AA-BB-CC-DD-EE-FF;Mit;Semi":  "Mit;Semi",
	} {
		if got := ssidFromCalled(in); got != want {
			t.Errorf("%q → %q, erwartet %q", in, got, want)
		}
	}
}
