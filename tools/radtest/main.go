// radtest schickt eine Test-Anmeldung (PAP oder MS-CHAPv2) an einen RADIUS-Server.
//
//	go run ./tools/radtest -server 127.0.0.1:1812 -secret geheim -user max -pass passwort
package main

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"layeh.com/radius"
	"layeh.com/radius/rfc2759"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2868"
	"layeh.com/radius/rfc2869"
	"layeh.com/radius/vendors/microsoft"
)

func main() {
	server := flag.String("server", "127.0.0.1:1812", "RADIUS-Server (host:port)")
	secret := flag.String("secret", "", "Shared Secret")
	user := flag.String("user", "", "Benutzername")
	pass := flag.String("pass", "", "Passwort")
	method := flag.String("method", "pap", "pap oder mschapv2")
	from := flag.String("from", "", "lokale Absender-IP (optional)")
	ssid := flag.String("ssid", "", "SSID, die der Access Point meldet (optional)")
	portType := flag.Int("porttype", -1, "NAS-Port-Type (15 = Ethernet, 19 = WLAN, 5 = Virtual/SSH)")
	noMA := flag.Bool("noma", false, "ohne Message-Authenticator senden (wie ältere Switches)")
	flag.Parse()
	if *secret == "" || *user == "" {
		flag.Usage()
		os.Exit(2)
	}

	p := radius.New(radius.CodeAccessRequest, []byte(*secret))
	rfc2865.UserName_SetString(p, *user)
	rfc2865.CallingStationID_SetString(p, "02-00-00-00-00-01")
	if *portType >= 0 {
		rfc2865.NASPortType_Set(p, rfc2865.NASPortType(*portType))
	}
	if *ssid != "" {
		rfc2865.CalledStationID_SetString(p, "02-00-00-00-00-AA:"+*ssid)
	}
	switch *method {
	case "pap":
		rfc2865.UserPassword_SetString(p, *pass)
	case "mschapv2":
		authC, peerC := make([]byte, 16), make([]byte, 16)
		rand.Read(authC)
		rand.Read(peerC)
		nt, _ := rfc2759.GenerateNTResponse(authC, peerC, []byte(*user), []byte(*pass))
		microsoft.MSCHAPChallenge_Add(p, authC)
		resp := append([]byte{1, 0}, peerC...)
		resp = append(resp, make([]byte, 8)...)
		microsoft.MSCHAP2Response_Add(p, append(resp, nt...))
	default:
		fmt.Println("unbekannte Methode")
		os.Exit(2)
	}

	if !*noMA {
		addMA(p)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	client := &radius.Client{Retry: time.Second}
	if *from != "" {
		client.Dialer = net.Dialer{LocalAddr: &net.UDPAddr{IP: net.ParseIP(*from)}}
	}
	r, err := client.Exchange(ctx, p, *server)
	if err != nil {
		fmt.Println("Keine Antwort:", err)
		os.Exit(1)
	}
	fmt.Println("Antwort:", r.Code)
	if m := rfc2865.ReplyMessage_GetString(r); m != "" {
		fmt.Println("Meldung:", m)
	}
	if _, v := rfc2868.TunnelPrivateGroupID_GetString(r); v != "" {
		fmt.Println("VLAN:   ", v)
	}
	fmt.Println("Attribute:")
	for _, a := range r.Attributes {
		if a.Type == rfc2865.VendorSpecific_Type {
			if id, v, err := radius.VendorSpecific(a.Attribute); err == nil && len(v) >= 2 {
				fmt.Printf("  VSA Hersteller %d, Nr. %d: %q\n", id, v[0], v[2:])
				continue
			}
		}
		if a.Type == 80 || a.Type == 79 || a.Type == 18 {
			continue
		}
		fmt.Printf("  Nr. %d: % x\n", a.Type, []byte(a.Attribute))
	}
	if r.Code != radius.CodeAccessAccept {
		os.Exit(1)
	}
}

// addMA fügt einen Message-Authenticator hinzu (RFC 3579), wie es aktuelle Access Points tun.
func addMA(p *radius.Packet) {
	p.Add(rfc2869.MessageAuthenticator_Type, make(radius.Attribute, 16))
	b, err := p.MarshalBinary()
	if err != nil {
		return
	}
	m := hmac.New(md5.New, p.Secret)
	m.Write(b)
	for _, a := range p.Attributes {
		if a.Type == rfc2869.MessageAuthenticator_Type {
			a.Attribute = m.Sum(nil)
		}
	}
}
