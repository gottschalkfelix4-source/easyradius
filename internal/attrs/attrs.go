// Package attrs beschreibt frei definierbare RADIUS-Antwortattribute
// (Standard- und Herstellerattribute) und kodiert sie für Access-Accept.
package attrs

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// Typen der Attributwerte.
const (
	String  = "string"
	Integer = "integer"
	IPAddr  = "ipaddr"
	Octets  = "octets"
)

// Def beschreibt ein Attribut aus dem Wörterbuch.
type Def struct {
	Name     string         `json:"name"`
	Vendor   string         `json:"vendor"`   // "" = Standard (RFC)
	VendorID uint32         `json:"vendorId"` // 0 = Standard
	Code     uint8          `json:"code"`
	Type     string         `json:"type"`
	Tagged   bool           `json:"tagged,omitempty"`
	Values   map[string]int `json:"values,omitempty"` // benannte Werte
	Help     string         `json:"help,omitempty"`
	Use      string         `json:"use"` // "reply", "check" oder "both"
}

// Arten wie bei TekRADIUS: Check = muss in der Anfrage passen, Reply = wird zurückgeschickt.
const (
	Check = "check"
	Reply = "reply"
)

// Attr ist ein konfiguriertes Antwortattribut an Benutzer oder Gruppe.
type Attr struct {
	Kind     string `json:"kind"`     // "check" oder "reply" (leer = reply)
	Name     string `json:"name"`     // Wörterbuchname oder "Eigenes VSA"
	VendorID uint32 `json:"vendorId"` // 0 = Standard
	Code     uint8  `json:"code"`
	Type     string `json:"type"`
	Tagged   bool   `json:"tagged,omitempty"`
	Tag      uint8  `json:"tag,omitempty"`
	Value    string `json:"value"`
}

// Key identifiziert das Attribut (für Überschreiben Gruppe → Benutzer).
func (a Attr) Key() string { return fmt.Sprintf("%s:%d:%d", a.kind(), a.VendorID, a.Code) }

func (a Attr) kind() string {
	if a.Kind == Check {
		return Check
	}
	return Reply
}

// IsCheck meldet, ob es ein Prüfattribut ist.
func (a Attr) IsCheck() bool { return a.Kind == Check }

// Split trennt Prüf- und Antwortattribute.
func Split(list []Attr) (check, reply []Attr) {
	for _, a := range list {
		if a.IsCheck() {
			check = append(check, a)
		} else {
			reply = append(reply, a)
		}
	}
	return
}

var tunnelTypes = map[string]int{"PPTP": 1, "L2F": 2, "L2TP": 3, "ATMP": 4, "VTP": 5, "AH": 6, "IP-IP": 7, "MIN-IP-IP": 8,
	"ESP": 9, "GRE": 10, "DVS": 11, "IP-in-IP": 12, "VLAN": 13}
var mediumTypes = map[string]int{"IPv4": 1, "IPv6": 2, "NSAP": 3, "HDLC": 4, "BBN-1822": 5, "IEEE-802": 6, "E.163": 7,
	"E.164": 8, "F.69": 9, "X.121": 10, "IPX": 11, "Appletalk": 12, "DecNet-IV": 13, "Banyan-Vines": 14, "E.164-NSAP": 15}

// Dictionary enthält die gängigen Antwortattribute.
var Dictionary = []Def{
	// --- Anfrage-Attribute (für Check) ---
	{Name: "User-Name", Code: 1, Type: String, Use: "check", Help: "Anmeldename, wie er vom Gerät kommt"},
	{Name: "NAS-IP-Address", Code: 4, Type: IPAddr, Use: "check", Help: "IP des Switches/Access Points; auch Netz möglich, z. B. 10.0.0.0/24"},
	{Name: "NAS-Port", Code: 5, Type: Integer, Use: "check", Help: "Portnummer am Switch"},
	{Name: "Called-Station-Id", Code: 30, Type: String, Use: "check", Help: "MAC des Access Points und SSID, z. B. *:Firma"},
	{Name: "Calling-Station-Id", Code: 31, Type: String, Use: "check", Help: "MAC des Endgeräts, z. B. AA-BB-CC-*"},
	{Name: "NAS-Identifier", Code: 32, Type: String, Use: "check", Help: "Name des Switches/Access Points"},
	{Name: "NAS-Port-Type", Code: 61, Type: Integer, Use: "check", Values: map[string]int{"Async": 0, "Sync": 1, "ISDN": 2,
		"ISDN-V120": 3, "ISDN-V110": 4, "Virtual": 5, "PIAFS": 6, "HDLC-Clear-Channel": 7, "X.25": 8, "X.75": 9,
		"G.3-Fax": 10, "SDSL": 11, "ADSL-CAP": 12, "ADSL-DMT": 13, "IDSL": 14, "Ethernet": 15, "xDSL": 16, "Cable": 17,
		"Wireless-Other": 18, "Wireless-802.11": 19}, Help: "Ethernet = Kabel (Switch), Wireless-802.11 = WLAN, Virtual = SSH/Telnet/VPN"},
	{Name: "Connect-Info", Code: 77, Type: String, Use: "check"},
	{Name: "NAS-Port-Id", Code: 87, Type: String, Use: "check", Help: "Portname am Switch, z. B. ge1/0/5"},
	{Name: "MS-RAS-Version", Vendor: "Microsoft", VendorID: 311, Code: 18, Type: String, Use: "check"},
	{Name: "MS-RAS-Vendor", Vendor: "Microsoft", VendorID: 311, Code: 9, Type: Integer, Use: "check"},

	// --- RFC 2865 / 2866 / 2868 / 2869 / 4675 ---
	{Name: "Service-Type", Code: 6, Type: Integer, Use: "both", Values: map[string]int{"Login-User": 1, "Framed-User": 2, "Callback-Login-User": 3,
		"Callback-Framed-User": 4, "Outbound-User": 5, "Administrative-User": 6, "NAS-Prompt-User": 7, "Authenticate-Only": 8,
		"Callback-NAS-Prompt": 9, "Call-Check": 10, "Callback-Administrative": 11},
		Help: "Switch-/Router-Login: Administrative-User = Admin, NAS-Prompt-User = nur lesen"},
	{Name: "Framed-Protocol", Code: 7, Type: Integer, Use: "both", Values: map[string]int{"PPP": 1, "SLIP": 2, "ARAP": 3, "GANDALF-SLML": 4, "Xylogics-IPX-SLIP": 5, "X.75-Synchronous": 6}},
	{Name: "Framed-IP-Address", Code: 8, Use: "reply", Type: IPAddr, Help: "Feste IP-Adresse für VPN/PPP"},
	{Name: "Framed-IP-Netmask", Code: 9, Use: "reply", Type: IPAddr},
	{Name: "Filter-Id", Code: 11, Use: "reply", Type: String, Help: "Name einer ACL/Firewall-Regel auf dem Gerät"},
	{Name: "Framed-MTU", Code: 12, Use: "reply", Type: Integer},
	{Name: "Reply-Message", Code: 18, Use: "reply", Type: String},
	{Name: "Framed-Route", Code: 22, Use: "reply", Type: String},
	{Name: "Class", Code: 25, Use: "reply", Type: String},
	{Name: "Session-Timeout", Code: 27, Use: "reply", Type: Integer, Help: "Sekunden bis zur erneuten Anmeldung"},
	{Name: "Idle-Timeout", Code: 28, Use: "reply", Type: Integer, Help: "Sekunden Inaktivität bis zur Trennung"},
	{Name: "Termination-Action", Code: 29, Use: "reply", Type: Integer, Values: map[string]int{"Default": 0, "RADIUS-Request": 1},
		Help: "RADIUS-Request = bei Session-Timeout neu authentifizieren statt trennen"},
	{Name: "Egress-VLANID", Code: 56, Use: "reply", Type: Integer, Help: "RFC 4675: 0x31000000 + VLAN = getaggt, 0x32000000 + VLAN = ungetaggt"},
	{Name: "Egress-VLAN-Name", Code: 58, Use: "reply", Type: String, Help: "RFC 4675: „1“ (getaggt) oder „2“ (ungetaggt) + VLAN-Name, z. B. 1Voice"},
	{Name: "Port-Limit", Code: 62, Use: "reply", Type: Integer},
	{Name: "Tunnel-Type", Code: 64, Use: "reply", Type: Integer, Tagged: true, Values: tunnelTypes, Help: "Für dynamisches VLAN (802.1X): VLAN"},
	{Name: "Tunnel-Medium-Type", Code: 65, Use: "reply", Type: Integer, Tagged: true, Values: mediumTypes, Help: "Für dynamisches VLAN (802.1X): IEEE-802"},
	{Name: "Tunnel-Private-Group-Id", Code: 81, Use: "reply", Type: String, Tagged: true, Help: "Für dynamisches VLAN (802.1X): VLAN-Nummer oder -Name"},
	{Name: "Acct-Interim-Interval", Code: 85, Use: "reply", Type: Integer, Help: "Sekunden zwischen Accounting-Zwischenmeldungen"},
	{Name: "Framed-Pool", Code: 88, Use: "reply", Type: String},
	{Name: "NAS-Filter-Rule", Code: 92, Use: "reply", Type: String},

	// --- Hersteller ---
	{Name: "Zyxel-Privilege-AVPair", Vendor: "Zyxel", VendorID: 890, Code: 3, Use: "reply", Type: String,
		Help: "Berechtigung am Zyxel-Switch, z. B. shell:priv-lvl=14 (Admin) – Wert laut Switch-Handbuch"},
	{Name: "Cisco-AVPair", Vendor: "Cisco", VendorID: 9, Code: 1, Use: "reply", Type: String, Help: "z. B. shell:priv-lvl=15"},
	{Name: "Cisco-NAS-Port", Vendor: "Cisco", VendorID: 9, Code: 2, Use: "reply", Type: String},
	{Name: "Mikrotik-Recv-Limit", Vendor: "MikroTik", VendorID: 14988, Code: 1, Use: "reply", Type: Integer},
	{Name: "Mikrotik-Xmit-Limit", Vendor: "MikroTik", VendorID: 14988, Code: 2, Use: "reply", Type: Integer},
	{Name: "Mikrotik-Group", Vendor: "MikroTik", VendorID: 14988, Code: 3, Use: "reply", Type: String, Help: "Benutzergruppe auf dem Router, z. B. full oder read"},
	{Name: "Mikrotik-Rate-Limit", Vendor: "MikroTik", VendorID: 14988, Code: 8, Use: "reply", Type: String, Help: "z. B. 10M/10M"},
	{Name: "Mikrotik-Address-List", Vendor: "MikroTik", VendorID: 14988, Code: 19, Use: "reply", Type: String},
	{Name: "Mikrotik-Wireless-VLANID", Vendor: "MikroTik", VendorID: 14988, Code: 26, Use: "reply", Type: Integer},
	{Name: "Aruba-User-Role", Vendor: "Aruba", VendorID: 14823, Code: 1, Use: "reply", Type: String},
	{Name: "Aruba-User-Vlan", Vendor: "Aruba", VendorID: 14823, Code: 2, Use: "reply", Type: Integer},
	{Name: "Fortinet-Group-Name", Vendor: "Fortinet", VendorID: 12356, Code: 1, Use: "reply", Type: String},
	{Name: "Fortinet-Access-Profile", Vendor: "Fortinet", VendorID: 12356, Code: 6, Use: "reply", Type: String},
	{Name: "Juniper-Local-User-Name", Vendor: "Juniper", VendorID: 2636, Code: 1, Use: "reply", Type: String},
	{Name: "WISPr-Bandwidth-Max-Up", Vendor: "WISPr", VendorID: 14122, Code: 7, Use: "reply", Type: Integer, Help: "Bit/s"},
	{Name: "WISPr-Bandwidth-Max-Down", Vendor: "WISPr", VendorID: 14122, Code: 8, Use: "reply", Type: Integer, Help: "Bit/s"},
	{Name: "WISPr-Session-Terminate-Time", Vendor: "WISPr", VendorID: 14122, Code: 9, Use: "reply", Type: String},
}

// Geschützte Attribute, die EasyRadius selbst setzt.
var protected = map[string]bool{
	"0:79": true, "0:80": true, "0:24": true, "0:1": true, // EAP-Message, Message-Authenticator, State, User-Name
	"311:16": true, "311:17": true, "311:26": true, // MS-MPPE-Keys, MS-CHAP2-Success
}

// In Prüfungen nicht erlaubt (Passwörter und Protokolldaten).
var protectedCheck = map[string]bool{
	"0:2": true, "0:3": true, "0:60": true, "0:79": true, "0:80": true, "0:24": true,
	"311:1": true, "311:11": true, "311:25": true,
}

// Preset ist eine Vorlage mit mehreren Attributen.
type Preset struct {
	Name  string `json:"name"`
	Help  string `json:"help"`
	Attrs []Attr `json:"attrs"`
}

func find(name string) Def {
	for _, d := range Dictionary {
		if d.Name == name {
			return d
		}
	}
	panic("unbekanntes Attribut " + name)
}

func fromDef(name, value string) Attr {
	d := find(name)
	return Attr{Name: d.Name, VendorID: d.VendorID, Code: d.Code, Type: d.Type, Tagged: d.Tagged, Value: value}
}

// Presets sind fertige Vorlagen für typische Geräte.
var Presets = []Preset{
	{Name: "802.1X: dynamisches VLAN", Help: "Standard für Switches und Access Points (auch Zyxel). VLAN-Nummer anpassen.",
		Attrs: []Attr{fromDef("Tunnel-Type", "VLAN"), fromDef("Tunnel-Medium-Type", "IEEE-802"), fromDef("Tunnel-Private-Group-Id", "10")}},
	{Name: "Zyxel-Switch: Administrator", Help: "Admin-Zugang am Zyxel-Switch (Web/SSH). Privilegstufe laut Handbuch prüfen.",
		Attrs: []Attr{fromDef("Service-Type", "Administrative-User"), fromDef("Zyxel-Privilege-AVPair", "shell:priv-lvl=14")}},
	{Name: "Zyxel-Switch: nur lesen", Help: "Lesezugriff am Zyxel-Switch.",
		Attrs: []Attr{fromDef("Service-Type", "NAS-Prompt-User"), fromDef("Zyxel-Privilege-AVPair", "shell:priv-lvl=0")}},
	{Name: "Cisco: Administrator", Help: "Privileg 15 an Cisco-Geräten.",
		Attrs: []Attr{fromDef("Service-Type", "Administrative-User"), fromDef("Cisco-AVPair", "shell:priv-lvl=15")}},
	{Name: "HPE/Aruba-Switch: Manager", Help: "Vollzugriff an HPE-/Aruba-Switches.",
		Attrs: []Attr{fromDef("Service-Type", "Administrative-User")}},
	{Name: "HPE/Aruba-Switch: Operator", Help: "Lesezugriff an HPE-/Aruba-Switches.",
		Attrs: []Attr{fromDef("Service-Type", "NAS-Prompt-User")}},
	{Name: "MikroTik: Vollzugriff", Help: "Router-Login mit Gruppe „full“.",
		Attrs: []Attr{fromDef("Mikrotik-Group", "full")}},
	{Name: "Regelmäßig neu anmelden", Help: "Nach 8 Stunden neu authentifizieren, ohne die Verbindung zu trennen.",
		Attrs: []Attr{fromDef("Session-Timeout", "28800"), fromDef("Termination-Action", "RADIUS-Request")}},
}

// Normalize prüft und vereinheitlicht ein Attribut.
func Normalize(a Attr) (Attr, error) {
	a.Value = strings.TrimSpace(a.Value)
	a.Kind = a.kind()
	if a.Code == 0 {
		return a, errors.New("Attributnummer fehlt")
	}
	id := fmt.Sprintf("%d:%d", a.VendorID, a.Code)
	if a.IsCheck() {
		if protectedCheck[id] {
			return a, fmt.Errorf("„%s“ kann nicht geprüft werden – Passwörter werden über das Passwortfeld geprüft", a.Name)
		}
		if a.Name == "" {
			a.Name = fmt.Sprintf("Attribut %d", a.Code)
		}
		if a.Value == "" {
			return a, fmt.Errorf("%s: Wert fehlt", a.Name)
		}
		switch a.Type {
		case String, Integer, IPAddr, Octets:
		default:
			return a, fmt.Errorf("unbekannter Typ %q", a.Type)
		}
		return a, nil
	}
	if protected[id] {
		return a, fmt.Errorf("„%s“ wird von EasyRadius selbst gesetzt und kann nicht überschrieben werden", a.Name)
	}
	if a.VendorID == 0 && a.Code == 26 {
		return a, errors.New("Herstellerattribute bitte als „Eigenes Herstellerattribut“ mit Hersteller-ID anlegen")
	}
	switch a.Type {
	case String, Integer, IPAddr, Octets:
	default:
		return a, fmt.Errorf("unbekannter Typ %q", a.Type)
	}
	if a.Tag > 31 {
		return a, errors.New("Tag muss zwischen 0 und 31 liegen")
	}
	if a.Name == "" {
		a.Name = fmt.Sprintf("Attribut %d", a.Code)
		if a.VendorID != 0 {
			a.Name = fmt.Sprintf("VSA %d/%d", a.VendorID, a.Code)
		}
	}
	if _, err := a.Encode(); err != nil {
		return a, fmt.Errorf("%s: %w", a.Name, err)
	}
	return a, nil
}

// Encode liefert den Attributwert (ohne Typ/Länge) im RADIUS-Format.
func (a Attr) Encode() ([]byte, error) {
	var v []byte
	switch a.Type {
	case String:
		v = []byte(a.Value)
		if a.Tagged {
			v = append([]byte{a.Tag}, v...)
		}
	case Integer:
		n, err := a.intValue()
		if err != nil {
			return nil, err
		}
		v = make([]byte, 4)
		binary.BigEndian.PutUint32(v, n)
		if a.Tagged {
			v[0] = a.Tag
		}
	case IPAddr:
		ip := net.ParseIP(a.Value).To4()
		if ip == nil {
			return nil, errors.New("IPv4-Adresse erwartet")
		}
		v = ip
	case Octets:
		h := strings.TrimPrefix(strings.ReplaceAll(a.Value, " ", ""), "0x")
		b, err := hex.DecodeString(h)
		if err != nil {
			return nil, errors.New("Hex-Wert erwartet, z. B. 0A1B2C")
		}
		v = b
	}
	if len(v) == 0 && a.Type != String {
		return nil, errors.New("Wert fehlt")
	}
	max := 253
	if a.VendorID != 0 {
		max = 253 - 6 - 2
	}
	if len(v) > max {
		return nil, fmt.Errorf("Wert zu lang (max. %d Byte)", max)
	}
	return v, nil
}

func (a Attr) intValue() (uint32, error) {
	for _, d := range Dictionary {
		if d.VendorID == a.VendorID && d.Code == a.Code {
			for name, n := range d.Values {
				if strings.EqualFold(name, a.Value) {
					return uint32(n), nil
				}
			}
		}
	}
	s := strings.ToLower(a.Value)
	base := 10
	if strings.HasPrefix(s, "0x") {
		s, base = s[2:], 16
	}
	n, err := strconv.ParseUint(s, base, 32)
	if err != nil {
		return 0, errors.New("Zahl erwartet")
	}
	if a.Tagged && n > 0xFFFFFF {
		return 0, errors.New("Zahl zu groß für ein Tunnel-Attribut")
	}
	return uint32(n), nil
}

// Merge kombiniert Gruppen- und Benutzerattribute: Ein Benutzerattribut ersetzt
// alle gleichnamigen Attribute der Gruppe.
func Merge(group, user []Attr) []Attr {
	override := map[string]bool{}
	for _, a := range user {
		override[a.Key()] = true
	}
	out := []Attr{}
	for _, a := range group {
		if !override[a.Key()] {
			out = append(out, a)
		}
	}
	return append(out, user...)
}

// Vendors liefert die Hersteller im Wörterbuch (für die Oberfläche).
func Vendors() []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range Dictionary {
		if d.Vendor != "" && !seen[d.Vendor] {
			seen[d.Vendor] = true
			out = append(out, d.Vendor)
		}
	}
	sort.Strings(out)
	return out
}

// ---- Prüfung (Check) ----

// Matches prüft, ob einer der Werte aus der Anfrage zur Vorgabe passt.
// Vorgaben: Platzhalter * und ?, Netze (10.0.0.0/24) bei IP-Adressen, benannte
// Werte bei Zahlen und „!“ am Anfang für „darf nicht“.
func (a Attr) Matches(values [][]byte) bool {
	want, negate := a.Value, false
	if strings.HasPrefix(want, "!") {
		want, negate = strings.TrimSpace(want[1:]), true
	}
	hit := false
	for _, v := range values {
		if a.matchOne(want, v) {
			hit = true
			break
		}
	}
	return hit != negate
}

func (a Attr) matchOne(want string, v []byte) bool {
	switch a.Type {
	case Integer:
		if len(v) != 4 {
			return false
		}
		n := binary.BigEndian.Uint32(v)
		if a.Tagged {
			n &= 0xFFFFFF
		}
		w := a
		w.Value = want
		exp, err := w.intValue()
		return err == nil && exp == n
	case IPAddr:
		if len(v) != 4 {
			return false
		}
		ip := net.IP(v)
		if strings.Contains(want, "/") {
			_, n, err := net.ParseCIDR(want)
			return err == nil && n.Contains(ip)
		}
		return wildcard(want, ip.String())
	case Octets:
		return strings.EqualFold(strings.TrimPrefix(strings.ReplaceAll(want, " ", ""), "0x"), hex.EncodeToString(v))
	default:
		if a.Tagged && len(v) > 0 && v[0] <= 0x1F {
			v = v[1:]
		}
		return wildcard(want, string(v))
	}
}

// wildcard vergleicht ohne Groß-/Kleinschreibung mit * und ?.
func wildcard(pattern, s string) bool {
	p, t := []rune(strings.ToLower(pattern)), []rune(strings.ToLower(s))
	var pi, ti, star, mark = 0, 0, -1, 0
	for ti < len(t) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == t[ti]):
			pi++
			ti++
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, ti
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			ti = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

// Display zeigt einen Anfragewert lesbar an (für Fehlermeldungen).
func (a Attr) Display(values [][]byte) string {
	if len(values) == 0 {
		return "nicht vorhanden"
	}
	v := values[0]
	switch a.Type {
	case Integer:
		if len(v) == 4 {
			n := binary.BigEndian.Uint32(v)
			for _, d := range Dictionary {
				if d.VendorID == a.VendorID && d.Code == a.Code {
					for name, x := range d.Values {
						if uint32(x) == n {
							return name
						}
					}
				}
			}
			return strconv.FormatUint(uint64(n), 10)
		}
	case IPAddr:
		if len(v) == 4 {
			return net.IP(v).String()
		}
	case Octets:
		return hex.EncodeToString(v)
	}
	return string(v)
}
