// Package radiusd ist der eigentliche RADIUS-Server (Authentifizierung + Accounting).
package radiusd

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gottschalkfelix4-source/easyradius/internal/attrs"
	"github.com/gottschalkfelix4-source/easyradius/internal/certs"
	"github.com/gottschalkfelix4-source/easyradius/internal/directory"
	"github.com/gottschalkfelix4-source/easyradius/internal/events"
	"github.com/gottschalkfelix4-source/easyradius/internal/store"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2866"
	"layeh.com/radius/rfc2868"
	"layeh.com/radius/rfc2869"
	"layeh.com/radius/rfc3580"
	"layeh.com/radius/vendors/microsoft"
)

type Server struct {
	Store *store.Store
	Log   *events.Log
	Certs *certs.Manager

	mu        sync.Mutex
	conns     []net.PacketConn
	running   bool
	startedAt time.Time
	lastErr   string

	sessMu   sync.Mutex
	sessions map[string]*eapState

	cacheMu sync.Mutex
	cache   map[string]cachedResp

	unknownMu sync.Mutex
	unknown   map[string]time.Time // unbekannte Absender-IPs

	acctMu sync.Mutex
	online map[string]*OnlineSession

	sem chan struct{} // begrenzt gleichzeitig bearbeitete Pakete

	warnMu sync.Mutex
	warned map[string]time.Time // gedrosselte Warnungen

	locks lockouts // Sperre nach Fehlversuchen

	DataDir     string  // für Accounting-Protokoll und offene Sitzungen ("" = nur im Speicher)
	acct        acctLog // abgeschlossene Sitzungen
	onlineDirty bool    // geschützt durch acctMu

	dirs        dirCache
	dirOverride directory.Directory // nur für Tests
}

type cachedResp struct {
	data    []byte
	expires time.Time
}

type OnlineSession struct {
	Key      string    `json:"key"` // eindeutig, für Trennen
	ID       string    `json:"id"`  // Acct-Session-Id
	User     string    `json:"user"`
	Client   string    `json:"client"`
	ClientID string    `json:"clientId"`
	NASAddr  string    `json:"nasAddr"` // Absender-IP des Geräts
	NASIP    string    `json:"nasIp"`   // NAS-IP-Address
	NASID    string    `json:"nasId"`   // NAS-Identifier
	Station  string    `json:"station"`
	IP       string    `json:"ip"`
	Started  time.Time `json:"started"`
	Updated  time.Time `json:"updated"`
	InBytes  uint64    `json:"inBytes"`
	OutBytes uint64    `json:"outBytes"`
}

type eapState struct {
	mu        sync.Mutex
	tun       *tunnelSession // laufendes TLS-Verfahren; Austausch nur unter sessMu
	method    byte           // aktuell angebotenes EAP-Verfahren
	offered   []byte         // bereits angebotene Verfahren (gegen Endlosschleifen bei NAK)
	started   bool           // Client hat das angebotene Verfahren angenommen
	outerUser string
	station   string
	lastSeen  atomic.Int64 // UnixNano – vom Aufräumer ohne Sperre gelesen
	lastReqID byte         // ID der zuletzt gesendeten EAP-Anfrage
	ident     *identity
	dec       decision
}

// Grenzen gegen Überlastung durch (gefälschte) Pakete.
const (
	maxConcurrent     = 256  // gleichzeitig bearbeitete Pakete
	maxEAPSessions    = 2000 // offene EAP-Sitzungen insgesamt
	maxPerStation     = 4    // offene EAP-Sitzungen pro Endgerät
	maxUnknownClients = 1000
	maxEAPAccum       = 64 * 1024
)

func New(st *store.Store, lg *events.Log, cm *certs.Manager) *Server {
	s := &Server{
		Store: st, Log: lg, Certs: cm,
		sessions: map[string]*eapState{},
		cache:    map[string]cachedResp{},
		unknown:  map[string]time.Time{},
		online:   map[string]*OnlineSession{},
		sem:      make(chan struct{}, maxConcurrent),
		warned:   map[string]time.Time{},
	}
	go s.janitor()
	return s
}

// Start öffnet die UDP-Ports aus den Einstellungen.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil
	}
	cfg := s.Store.Settings()
	auth, err := net.ListenPacket("udp", fmt.Sprintf(":%d", cfg.AuthPort))
	if err != nil {
		s.lastErr = fmt.Sprintf("Port %d/UDP belegt oder gesperrt: %v", cfg.AuthPort, err)
		return fmt.Errorf("%s", s.lastErr)
	}
	acct, err := net.ListenPacket("udp", fmt.Sprintf(":%d", cfg.AcctPort))
	if err != nil {
		auth.Close()
		s.lastErr = fmt.Sprintf("Port %d/UDP belegt oder gesperrt: %v", cfg.AcctPort, err)
		return fmt.Errorf("%s", s.lastErr)
	}
	s.conns = []net.PacketConn{auth, acct}
	if s.DataDir != "" && s.acct.dir == "" {
		s.acct.dir = filepath.Join(s.DataDir, "accounting")
		s.loadOnline()
	}
	s.running = true
	s.startedAt = time.Now()
	s.lastErr = ""
	go s.serve(auth, false)
	go s.serve(acct, true)
	s.Log.Add(events.Event{Kind: events.Info, Message: fmt.Sprintf("RADIUS-Server gestartet (Ports %d/%d UDP)", cfg.AuthPort, cfg.AcctPort)})
	return nil
}

func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		c.Close()
	}
	s.conns = nil
	s.saveOnline()
	if s.running {
		s.Log.Add(events.Event{Kind: events.Info, Message: "RADIUS-Server gestoppt"})
	}
	s.running = false
}

func (s *Server) Restart() error {
	s.Stop()
	return s.Start()
}

type Status struct {
	Running   bool      `json:"running"`
	StartedAt time.Time `json:"startedAt"`
	Error     string    `json:"error"`
	AuthPort  int       `json:"authPort"`
	AcctPort  int       `json:"acctPort"`
}

func (s *Server) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.Store.Settings()
	return Status{Running: s.running, StartedAt: s.startedAt, Error: s.lastErr, AuthPort: cfg.AuthPort, AcctPort: cfg.AcctPort}
}

// UnknownClients liefert IPs, die zuletzt ohne Freigabe angefragt haben.
func (s *Server) UnknownClients() []map[string]any {
	s.unknownMu.Lock()
	defer s.unknownMu.Unlock()
	out := []map[string]any{}
	for ip, t := range s.unknown {
		if _, ok := s.Store.ClientFor(net.ParseIP(ip)); ok {
			delete(s.unknown, ip)
			continue
		}
		out = append(out, map[string]any{"ip": ip, "lastSeen": t})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["ip"].(string) < out[j]["ip"].(string) })
	return out
}

func (s *Server) Online() []OnlineSession {
	s.acctMu.Lock()
	defer s.acctMu.Unlock()
	out := make([]OnlineSession, 0, len(s.online))
	for _, o := range s.online {
		out = append(out, *o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

func (s *Server) janitor() {
	for range time.Tick(15 * time.Second) {
		now := time.Now()
		s.cacheMu.Lock()
		for k, v := range s.cache {
			if now.After(v.expires) {
				delete(s.cache, k)
			}
		}
		s.cacheMu.Unlock()
		s.sessMu.Lock()
		for k, v := range s.sessions {
			if now.Sub(time.Unix(0, v.lastSeen.Load())) > 60*time.Second {
				if v.tun != nil {
					v.tun.abort()
				}
				delete(s.sessions, k)
			}
		}
		s.sessMu.Unlock()
		s.acctMu.Lock()
		for k, v := range s.online {
			if now.Sub(v.Updated) > 24*time.Hour {
				delete(s.online, k)
				s.onlineDirty = true
			}
		}
		s.acctMu.Unlock()
		s.unknownMu.Lock()
		for k, v := range s.unknown {
			if now.Sub(v) > time.Hour {
				delete(s.unknown, k)
			}
		}
		s.unknownMu.Unlock()
		s.pruneLocks()
		s.saveOnline()
		if now.Minute() == 0 && now.Second() < 15 {
			s.pruneAccounting()
		}
		s.warnMu.Lock()
		for k, v := range s.warned {
			if now.Sub(v) > 10*time.Minute {
				delete(s.warned, k)
			}
		}
		s.warnMu.Unlock()
	}
}

// warn schreibt eine Warnung höchstens alle 5 Minuten je Schlüssel ins Protokoll.
func (s *Server) warn(key string, ev events.Event) {
	s.warnMu.Lock()
	last, ok := s.warned[key]
	if ok && time.Since(last) < 5*time.Minute {
		s.warnMu.Unlock()
		return
	}
	if !ok && len(s.warned) > 5000 {
		s.warnMu.Unlock()
		return
	}
	s.warned[key] = time.Now()
	s.warnMu.Unlock()
	ev.Kind = events.Warning
	s.Log.Add(ev)
}

func (s *Server) serve(conn net.PacketConn, acct bool) {
	buf := make([]byte, radius.MaxPacketLength)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// Unter Windows liefert UDP nach ICMP "Port unreachable" Fehler – ignorieren,
			// aber nicht im Leerlauf drehen.
			time.Sleep(5 * time.Millisecond)
			continue
		}
		pkt := append([]byte(nil), buf[:n]...)
		select {
		case s.sem <- struct{}{}:
			go func() {
				defer func() { <-s.sem }()
				s.handle(conn, addr, pkt, acct)
			}()
		default:
			s.warn("overload", events.Event{Message: "Zu viele gleichzeitige Anfragen – Pakete werden verworfen"})
		}
	}
}

func (s *Server) handle(conn net.PacketConn, addr net.Addr, raw []byte, acctPort bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Fehler bei Anfrage von %v: %v", addr, r)
		}
	}()
	if len(raw) < 20 {
		return
	}
	if l := int(binary.BigEndian.Uint16(raw[2:4])); l < 20 || l > len(raw) {
		return
	}
	ua, ok := addr.(*net.UDPAddr)
	if !ok {
		return
	}
	ip := ua.IP
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	client, ok := s.Store.ClientFor(ip)
	if !ok {
		s.unknownMu.Lock()
		last, seen := s.unknown[ip.String()]
		if seen || len(s.unknown) < maxUnknownClients {
			s.unknown[ip.String()] = time.Now()
		}
		s.unknownMu.Unlock()
		if !seen || time.Since(last) > 10*time.Minute {
			s.warn("unknown|"+ip.String(), events.Event{ClientIP: ip.String(),
				Message: "Anfrage von unbekanntem Gerät " + ip.String() + " ignoriert – unter „Geräte“ freigeben"})
		}
		return
	}
	secret := []byte(client.Secret)
	code := radius.Code(raw[0])

	// Echtheit prüfen, bevor irgendetwas gespeichert wird.
	if !radius.IsAuthenticRequest(raw, secret) {
		s.warn("secret|"+ip.String(), events.Event{Client: client.Name, ClientIP: ip.String(),
			Message: "Accounting-Paket mit falschem Shared Secret verworfen – Secret auf dem Gerät prüfen"})
		return
	}
	present, valid := checkMessageAuthenticator(raw, secret)
	if present && !valid {
		s.warn("secret|"+ip.String(), events.Event{Client: client.Name, ClientIP: ip.String(),
			Message: "Falsches Shared Secret (Message-Authenticator ungültig) – Secret auf dem Gerät prüfen"})
		return
	}
	// Schutz vor Blast-RADIUS (CVE-2024-3596): Message-Authenticator verlangen.
	if !present && (code == radius.CodeStatusServer || (code == radius.CodeAccessRequest && client.RequireMA)) {
		s.warn("noma|"+ip.String(), events.Event{Client: client.Name, ClientIP: ip.String(),
			Message: "Anfrage ohne Message-Authenticator verworfen (Schutz vor Blast-RADIUS) – am Gerät aktivieren oder beim Gerät die Pflicht abschalten"})
		return
	}

	// Wiederholte Anfrage? Dann gespeicherte Antwort erneut senden.
	key := addr.String() + "|" + hex.EncodeToString(raw[1:2]) + hex.EncodeToString(raw[4:20])
	s.cacheMu.Lock()
	if c, ok := s.cache[key]; ok {
		s.cacheMu.Unlock()
		if c.data != nil {
			conn.WriteTo(c.data, addr)
		}
		return
	}
	s.cache[key] = cachedResp{expires: time.Now().Add(30 * time.Second)} // in Bearbeitung
	s.cacheMu.Unlock()

	p, err := radius.Parse(raw, secret)
	if err != nil {
		return
	}
	s.Store.TouchClient(client.ID)

	var resp *radius.Packet
	switch p.Code {
	case radius.CodeAccessRequest:
		if acctPort {
			return
		}
		resp = s.handleAccess(p, &client, ip, present)
	case radius.CodeAccountingRequest:
		resp = s.handleAccounting(p, &client, ip)
	case radius.CodeStatusServer: // RFC 5997
		if acctPort {
			resp = p.Response(radius.CodeAccountingResponse)
		} else {
			resp = p.Response(radius.CodeAccessAccept)
		}
	default:
		return
	}
	if resp == nil {
		return
	}
	// Proxy-State unverändert und in gleicher Reihenfolge zurückgeben (RFC 2865 5.33).
	for _, a := range p.Attributes {
		if a.Type == rfc2865.ProxyState_Type {
			resp.Add(rfc2865.ProxyState_Type, append(radius.Attribute(nil), a.Attribute...))
		}
	}
	out, err := encodeWithMA(resp, resp.Code != radius.CodeAccountingResponse)
	if err != nil {
		log.Printf("Antwort kodieren: %v", err)
		return
	}
	s.cacheMu.Lock()
	s.cache[key] = cachedResp{data: out, expires: time.Now().Add(30 * time.Second)}
	s.cacheMu.Unlock()
	conn.WriteTo(out, addr)
}

// ---- Access-Request ----

func (s *Server) handleAccess(p *radius.Packet, c *store.Client, ip net.IP, hasMA bool) *radius.Packet {
	ev := events.Event{
		Client:   c.Name,
		ClientIP: ip.String(),
		User:     rfc2865.UserName_GetString(p),
		Station:  rfc2865.CallingStationID_GetString(p),
	}

	if eap := eapMessage(p); eap != nil {
		if !hasMA {
			s.warn("eapnoma|"+ev.ClientIP, events.Event{Client: ev.Client, ClientIP: ev.ClientIP,
				Message: "EAP-Anfrage ohne Message-Authenticator verworfen"})
			return nil
		}
		return s.handleEAP(p, c, eap, ev)
	}

	rc := reqCtx{client: *c, ssid: ssidFromCalled(rfc2865.CalledStationID_GetString(p)), req: p}
	cr := cred{station: ev.Station}

	// MAC-Anmeldung (MAB) für Geräte ohne 802.1X.
	if id, reason, isMAB := s.macIdentity(p); isMAB {
		ev.Method = "MAC"
		if id == nil {
			return s.reject(p, ev, reason)
		}
		dec, reason := s.authorize(id, rc)
		if reason != "" {
			return s.reject(p, ev, reason)
		}
		return s.accept(p, ev, id, dec)
	}

	if pw, err := rfc2865.UserPassword_LookupString(p); err == nil {
		ev.Method = "PAP"
		id, reason := s.verifyPAP(ev.User, pw, cr)
		if id == nil {
			return s.reject(p, ev, reason)
		}
		dec, reason := s.authorize(id, rc)
		if reason != "" {
			return s.reject(p, ev, reason)
		}
		return s.accept(p, ev, id, dec)
	}

	if resp := microsoft.MSCHAP2Response_Get(p); resp != nil {
		ev.Method = "MS-CHAPv2"
		chal := microsoft.MSCHAPChallenge_Get(p)
		if len(resp) != 50 || len(chal) != 16 {
			return s.reject(p, ev, "Ungültige MS-CHAPv2-Daten")
		}
		ident := resp[0]
		id, res, reason := s.verifyMSCHAP(ev.User, chal, resp[2:18], resp[26:50], cr)
		if id == nil {
			r := s.reject(p, ev, reason)
			microsoft.MSCHAPError_Add(r, append([]byte{ident}, []byte("E=691 R=0 V=3")...))
			return r
		}
		dec, reason := s.authorize(id, rc)
		if reason != "" {
			r := s.reject(p, ev, reason)
			microsoft.MSCHAPError_Add(r, append([]byte{ident}, []byte("E=691 R=0 V=3")...))
			return r
		}
		r := s.accept(p, ev, id, dec)
		microsoft.MSCHAP2Success_Add(r, append([]byte{ident}, []byte(res.AuthenticatorResponse)...))
		microsoft.MSMPPESendKey_Add(r, res.SendKey)
		microsoft.MSMPPERecvKey_Add(r, res.RecvKey)
		microsoft.MSMPPEEncryptionPolicy_Add(r, microsoft.MSMPPEEncryptionPolicy_Value_EncryptionAllowed)
		microsoft.MSMPPEEncryptionTypes_Add(r, microsoft.MSMPPEEncryptionTypes_Value_RC4128bitAllowed)
		return r
	}

	if rfc2865.CHAPPassword_Get(p) != nil {
		ev.Method = "CHAP"
		return s.reject(p, ev, "CHAP wird nicht unterstützt – am Gerät PAP oder MS-CHAPv2 einstellen")
	}
	ev.Method = "?"
	return s.reject(p, ev, "Unbekanntes Anmeldeverfahren")
}

func (s *Server) reject(p *radius.Packet, ev events.Event, reason string) *radius.Packet {
	ev.Kind, ev.Message = events.Reject, reason
	s.Log.Add(ev)
	r := p.Response(radius.CodeAccessReject)
	// Nach außen nur eine allgemeine Meldung – Details stehen im Protokoll
	// (keine Rückschlüsse auf Benutzernamen, keine gespiegelten Anfragewerte).
	rfc2865.ReplyMessage_SetString(r, "Anmeldung abgelehnt")
	return r
}

func (s *Server) accept(p *radius.Packet, ev events.Event, id *identity, dec decision) *radius.Packet {
	ev.Kind, ev.User = events.Accept, id.Username
	if id.AD {
		ev.Method += " · AD"
	}
	ev.Message = "Anmeldung erfolgreich"
	var extra []string
	if dec.Group != "" {
		extra = append(extra, "Gruppe "+dec.Group)
	}
	if dec.VLAN != "" {
		extra = append(extra, "VLAN "+dec.VLAN)
	}
	if len(dec.Attrs) > 0 {
		extra = append(extra, fmt.Sprintf("%d Attribut(e)", len(dec.Attrs)))
	}
	if len(extra) > 0 {
		ev.Message += " (" + strings.Join(extra, ", ") + ")"
	}
	s.Log.Add(ev)
	if id.Local != nil {
		s.Store.TouchUser(id.Local.Username)
	}
	r := p.Response(radius.CodeAccessAccept)
	if dec.VLAN != "" && !hasTunnelAttrs(dec.Attrs) {
		rfc2868.TunnelType_Add(r, 0, rfc3580.TunnelType_Value_VLAN)
		rfc2868.TunnelMediumType_Add(r, 0, rfc2868.TunnelMediumType_Value_IEEE802)
		r.Add(rfc2868.TunnelPrivateGroupID_Type, radius.Attribute(dec.VLAN)) // ohne Tag-Byte, wie bei FreeRADIUS
	}
	addAttrs(r, dec.Attrs)
	return r
}

// ---- EAP ----

func eapMessage(p *radius.Packet) []byte {
	var out []byte
	found := false
	for _, a := range p.Attributes {
		if a.Type == rfc2869.EAPMessage_Type {
			out = append(out, a.Attribute...)
			found = true
		}
	}
	if !found {
		return nil
	}
	return out
}

func setEAP(r *radius.Packet, eap []byte) {
	for len(eap) > 0 {
		n := len(eap)
		if n > 253 {
			n = 253
		}
		r.Add(rfc2869.EAPMessage_Type, radius.Attribute(append([]byte(nil), eap[:n]...)))
		eap = eap[n:]
	}
}

func buildEAP(code, id, typ byte, data []byte) []byte {
	l := 5 + len(data)
	if code == eapSuccess || code == eapFailure {
		return []byte{code, id, 0, 4}
	}
	b := []byte{code, id, byte(l >> 8), byte(l), typ}
	return append(b, data...)
}

func (s *Server) handleEAP(p *radius.Packet, c *store.Client, eap []byte, ev events.Event) *radius.Packet {
	ev.Method = "PEAP"
	if len(eap) < 5 || eap[0] != eapResponse {
		return s.eapReject(p, ev, 0, "Ungültige EAP-Nachricht")
	}
	l := int(binary.BigEndian.Uint16(eap[2:4]))
	if l < 5 || l > len(eap) {
		return nil // unstimmige Länge: still verwerfen
	}
	id, typ, data := eap[1], eap[4], eap[5:l]
	rc := reqCtx{client: *c, ssid: ssidFromCalled(rfc2865.CalledStationID_GetString(p)), req: p}

	state := rfc2865.State_Get(p)
	if typ == eapTypeIdentity || len(state) == 0 {
		if typ != eapTypeIdentity {
			return s.eapReject(p, ev, id, "EAP ohne Identität begonnen")
		}
		// Neue Sitzung: PEAP anbieten (mit Obergrenzen gegen Überlastung).
		station := rfc2865.CallingStationID_GetString(p)
		s.sessMu.Lock()
		total, perStation := len(s.sessions), 0
		for _, o := range s.sessions {
			if station != "" && o.station == station {
				perStation++
			}
		}
		s.sessMu.Unlock()
		if total >= maxEAPSessions || perStation >= maxPerStation {
			s.warn("eaplimit|"+station, events.Event{Client: ev.Client, ClientIP: ev.ClientIP, Station: station,
				Message: "Zu viele gleichzeitige WLAN-Anmeldungen – Anfrage verworfen"})
			return nil
		}
		st := &eapState{outerUser: string(data), station: station}
		st.lastSeen.Store(time.Now().UnixNano())
		ts, why := s.newTunnel(eapTypePEAP, st, rc, p)
		if ts == nil {
			return s.eapReject(p, ev, id, why)
		}
		st.tun, st.method, st.offered = ts, eapTypePEAP, []byte{eapTypePEAP}
		st.lastReqID = id + 1
		sid := newStateID()
		s.sessMu.Lock()
		s.sessions[sid] = st
		s.sessMu.Unlock()
		return s.challenge(p, sid, buildEAP(eapRequest, id+1, eapTypePEAP, []byte{peapFlagStart}))
	}

	sid := string(state)
	s.sessMu.Lock()
	st := s.sessions[sid]
	s.sessMu.Unlock()
	if st == nil {
		return s.eapReject(p, ev, id, "EAP-Sitzung abgelaufen")
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if id != st.lastReqID {
		return nil // Antwort passt nicht zur letzten Anfrage (Wiederholung/Fremdpaket): verwerfen
	}
	st.lastSeen.Store(time.Now().UnixNano())
	ev.Method = methodName(st.method)

	if typ == eapTypeNak {
		// Das Gerät möchte ein anderes Verfahren (Liste in Wunschreihenfolge).
		next := s.pickMethod(data, st)
		if next == 0 {
			s.dropSession(sid)
			return s.eapReject(p, ev, id, s.nakHint(data))
		}
		ev.Method = methodName(next)
		ts, why := s.newTunnel(next, st, rc, p)
		if ts == nil {
			s.dropSession(sid)
			return s.eapReject(p, ev, id, why)
		}
		s.sessMu.Lock()
		old := st.tun
		st.tun = ts
		s.sessMu.Unlock()
		if old != nil {
			old.abort()
		}
		st.method = next
		st.offered = append(st.offered, next)
		st.lastReqID = id + 1
		return s.challenge(p, sid, buildEAP(eapRequest, id+1, next, []byte{peapFlagStart}))
	}
	ps := st.tun
	if typ != st.method || len(data) < 1 {
		s.dropSession(sid)
		return s.eapReject(p, ev, id, fmt.Sprintf("Nicht unterstützte EAP-Methode %d", typ))
	}
	st.started = true
	flags := data[0]
	payload := data[1:]
	if flags&peapFlagLength != 0 && len(payload) >= 4 {
		payload = payload[4:]
	}

	// Client bestätigt ein Fragment von uns → nächstes schicken.
	if len(ps.pendingOut) > 0 && len(payload) == 0 {
		st.lastReqID = id + 1
		return s.challenge(p, sid, buildEAP(eapRequest, id+1, st.method, ps.nextFragment()))
	}

	if len(ps.inAccum)+len(payload) > maxEAPAccum {
		s.dropSession(sid)
		return s.eapReject(p, ev, id, "EAP-Nachricht zu groß")
	}
	ps.inAccum = append(ps.inAccum, payload...)
	if flags&peapFlagMore != 0 {
		st.lastReqID = id + 1
		return s.challenge(p, sid, buildEAP(eapRequest, id+1, st.method, []byte{0}))
	}
	msg := ps.inAccum
	ps.inAccum = nil

	out, finished := ps.exchange(msg)
	if finished {
		s.dropSession(sid)
		if ps.timedOut.Load() {
			return s.eapReject(p, ev, id, "Zeitüberschreitung bei der WLAN-Anmeldung")
		}
		res := ps.result
		if res.user != "" {
			ev.User = res.user
		}
		if res.method != "" {
			ev.Method = res.method
		}
		if !res.ok || st.ident == nil {
			return s.eapReject(p, ev, id, res.reason)
		}
		r := s.accept(p, ev, st.ident, st.dec)
		rfc2865.UserName_SetString(r, st.ident.Username)
		setEAP(r, buildEAP(eapSuccess, id, 0, nil))
		microsoft.MSMPPERecvKey_Add(r, res.msk[:32])
		microsoft.MSMPPESendKey_Add(r, res.msk[32:64])
		return r
	}
	ps.pendingOut, ps.outTotal = out, len(out)
	st.lastReqID = id + 1
	return s.challenge(p, sid, buildEAP(eapRequest, id+1, st.method, ps.nextFragment()))
}

// newTunnel erzeugt und startet die TLS-Sitzung für ein EAP-Verfahren.
// Bei Fehlern ist das Ergebnis nil und der zweite Wert enthält den Grund.
func (s *Server) newTunnel(method byte, st *eapState, rc reqCtx, p *radius.Packet) (*tunnelSession, string) {
	station := st.station
	serverName := s.Store.Settings().ServerName
	verify := func(name string, authC, peerC, ntResp []byte) (string, string, string) {
		ident, res, why := s.verifyMSCHAP(name, authC, peerC, ntResp, cred{station: station})
		if ident == nil {
			return "", "", why
		}
		dec, why := s.authorize(ident, rc)
		if why != "" {
			return "", ident.Username, why
		}
		st.ident, st.dec = ident, dec
		return res.AuthenticatorResponse, ident.Username, ""
	}
	var ts *tunnelSession
	switch method {
	case eapTypePEAP:
		ts = newPEAPSession(s.Certs.Certificate(), verify, serverName)
	case eapTypeTTLS:
		verifyPAP := func(name, pw string) (string, bool, string) {
			ident, why := s.verifyPAP(name, pw, cred{station: station})
			if ident == nil {
				return "", false, why
			}
			dec, why := s.authorize(ident, rc)
			if why != "" {
				return ident.Username, false, why
			}
			st.ident, st.dec = ident, dec
			return ident.Username, true, ""
		}
		ts = newTTLSSession(s.Certs.Certificate(), verify, verifyPAP, serverName)
	case eapTypeTLS:
		pool, err := s.Certs.ClientCAPool()
		if err != nil {
			return nil, "EAP-TLS nicht möglich: keine eigene Zertifizierungsstelle vorhanden (Benutzerzertifikate können nur mit der EasyRadius-CA geprüft werden)"
		}
		check := func(leaf *x509.Certificate) (string, string) {
			cn := leaf.Subject.CommonName
			if s.Certs.IsRevoked(certs.SerialOf(leaf)) {
				return cn, "Benutzerzertifikat wurde widerrufen"
			}
			if !validName(cn) {
				return "", "Benutzerzertifikat ohne gültigen Benutzernamen (CN)"
			}
			u, ok := s.Store.FindUser(cn)
			switch {
			case !ok:
				return cn, "Kein lokaler Benutzer zum Zertifikat „" + cn + "“"
			case !u.Enabled:
				return u.Username, "Benutzer ist deaktiviert"
			case u.Expired(time.Now()):
				return u.Username, "Benutzerkonto ist abgelaufen"
			}
			ident := &identity{Username: u.Username, Local: &u}
			dec, why := s.authorize(ident, rc)
			if why != "" {
				return u.Username, why
			}
			st.ident, st.dec = ident, dec
			return u.Username, ""
		}
		ts = newEAPTLSSession(s.Certs.Certificate(), pool, check, serverName)
	default:
		return nil, fmt.Sprintf("Nicht unterstützte EAP-Methode %d", method)
	}
	ts.fragSize = fragSizeFor(p)
	if !ts.start() {
		ts.abort()
		return nil, methodName(method) + " konnte nicht gestartet werden"
	}
	return ts, ""
}

// pickMethod wählt aus der NAK-Liste des Geräts das erste eingeschaltete,
// noch nicht angebotene Verfahren (0 = keines). Nur vor Beginn des Austauschs erlaubt.
func (s *Server) pickMethod(wanted []byte, st *eapState) byte {
	if st.started {
		return 0
	}
	set := s.Store.Settings().EAP
	for _, t := range wanted {
		if bytes.IndexByte(st.offered, t) >= 0 {
			continue
		}
		if t == eapTypePEAP || (t == eapTypeTTLS && set.TTLS) || (t == eapTypeTLS && set.TLS) {
			return t
		}
	}
	return 0
}

// nakHint erklärt, welche Verfahren im WLAN-Profil eingestellt werden können.
func (s *Server) nakHint(wanted []byte) string {
	set := s.Store.Settings().EAP
	var off []string
	for _, t := range wanted {
		if t == eapTypeTTLS && !set.TTLS {
			off = append(off, "EAP-TTLS")
		}
		if t == eapTypeTLS && !set.TLS {
			off = append(off, "EAP-TLS")
		}
	}
	extra := ""
	if len(off) > 0 {
		extra = " (" + strings.Join(off, " und ") + " in den Einstellungen ausgeschaltet)"
	}
	switch {
	case set.TTLS && set.TLS:
		return "Gerät möchte weder PEAP noch EAP-TTLS oder EAP-TLS – im WLAN-Profil „PEAP“ mit „MSCHAPv2“, „TTLS“ mit „PAP“/„MSCHAPv2“ oder „TLS“ mit Benutzerzertifikat wählen" + extra
	case set.TTLS:
		return "Gerät möchte weder PEAP noch EAP-TTLS – im WLAN-Profil „PEAP“ mit „MSCHAPv2“ oder „TTLS“ mit „PAP“/„MSCHAPv2“ wählen" + extra
	case set.TLS:
		return "Gerät möchte weder PEAP noch EAP-TLS – im WLAN-Profil „PEAP“ mit „MSCHAPv2“ oder „TLS“ mit Benutzerzertifikat wählen" + extra
	}
	return "Gerät möchte kein PEAP – im WLAN-Profil „PEAP“ mit „MSCHAPv2“ wählen" + extra
}

func (s *Server) challenge(p *radius.Packet, sid string, eap []byte) *radius.Packet {
	r := p.Response(radius.CodeAccessChallenge)
	rfc2865.State_Set(r, []byte(sid))
	setEAP(r, eap)
	return r
}

func (s *Server) eapReject(p *radius.Packet, ev events.Event, id byte, reason string) *radius.Packet {
	r := s.reject(p, ev, reason)
	setEAP(r, buildEAP(eapFailure, id, 0, nil))
	return r
}

func (s *Server) dropSession(sid string) {
	s.sessMu.Lock()
	if st := s.sessions[sid]; st != nil && st.tun != nil {
		st.tun.abort()
	}
	delete(s.sessions, sid)
	s.sessMu.Unlock()
}

// fragSizeFor passt die TLS-Fragmente an die Framed-MTU des Access Points an
// (EAP- und RADIUS-Kopf abgezogen), höchstens 1000 Byte.
func fragSizeFor(p *radius.Packet) int {
	mtu := int(rfc2865.FramedMTU_Get(p))
	if mtu == 0 {
		return fragmentSize
	}
	n := mtu - 60
	if n < 200 {
		n = 200
	}
	if n > fragmentSize {
		n = fragmentSize
	}
	return n
}

func newStateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return "ER" + hex.EncodeToString(b)
}

// ---- Accounting ----

func (s *Server) handleAccounting(p *radius.Packet, c *store.Client, ip net.IP) *radius.Packet {
	typ := rfc2866.AcctStatusType_Get(p)
	sid := rfc2866.AcctSessionID_GetString(p)
	user := rfc2865.UserName_GetString(p)
	station := rfc2865.CallingStationID_GetString(p)
	key := c.ID + "|" + sid
	if sid == "" {
		key = c.ID + "|" + station + "|" + user
	}
	now := time.Now()
	in := uint64(rfc2866.AcctInputOctets_Get(p)) + uint64(rfc2869.AcctInputGigawords_Get(p))<<32
	out := uint64(rfc2866.AcctOutputOctets_Get(p)) + uint64(rfc2869.AcctOutputGigawords_Get(p))<<32
	framed := ""
	if fip := rfc2865.FramedIPAddress_Get(p); fip != nil {
		framed = fip.String()
	}

	s.acctMu.Lock()
	switch typ {
	case rfc2866.AcctStatusType_Value_Start, rfc2866.AcctStatusType_Value_InterimUpdate:
		o := s.online[key]
		if o == nil {
			if len(s.online) >= 50000 {
				s.acctMu.Unlock()
				return p.Response(radius.CodeAccountingResponse)
			}
			started := now
			if t := rfc2866.AcctSessionTime_Get(p); t > 0 {
				started = now.Add(-time.Duration(t) * time.Second)
			}
			o = &OnlineSession{Key: key, ID: sid, User: user, Client: c.Name, ClientID: c.ID, Station: station, Started: started}
			s.online[key] = o
		}
		o.Updated, o.NASAddr = now, ip.String()
		if nip := rfc2865.NASIPAddress_Get(p); nip != nil {
			o.NASIP = nip.String()
		}
		o.NASID = rfc2865.NASIdentifier_GetString(p)
		if framed != "" {
			o.IP = framed
		}
		o.InBytes, o.OutBytes = in, out
		s.onlineDirty = true
	case rfc2866.AcctStatusType_Value_Stop:
		started := now.Add(-time.Duration(rfc2866.AcctSessionTime_Get(p)) * time.Second)
		if o := s.online[key]; o != nil && framed == "" {
			framed = o.IP
		}
		delete(s.online, key)
		s.onlineDirty = true
		s.acct.write(AcctRecord{User: user, Client: c.Name, Station: station, IP: framed, SessionID: sid,
			Start: started, Stop: now, Seconds: int64(rfc2866.AcctSessionTime_Get(p)), InBytes: in, OutBytes: out,
			Cause: terminateCause(rfc2866.AcctTerminateCause_Get(p))})
	case rfc2866.AcctStatusType_Value_AccountingOn, rfc2866.AcctStatusType_Value_AccountingOff:
		for k := range s.online {
			if strings.HasPrefix(k, c.ID+"|") {
				delete(s.online, k)
				s.onlineDirty = true
			}
		}
	}
	s.acctMu.Unlock()

	switch typ {
	case rfc2866.AcctStatusType_Value_Start:
		s.Log.Add(events.Event{Kind: events.Acct, User: user, Client: c.Name, ClientIP: ip.String(), Station: station, Message: "Sitzung begonnen"})
	case rfc2866.AcctStatusType_Value_Stop:
		dur := time.Duration(rfc2866.AcctSessionTime_Get(p)) * time.Second
		s.Log.Add(events.Event{Kind: events.Acct, User: user, Client: c.Name, ClientIP: ip.String(), Station: station,
			Message: "Sitzung beendet nach " + dur.String()})
	}
	return p.Response(radius.CodeAccountingResponse)
}

// ---- Message-Authenticator (RFC 3579) ----

func checkMessageAuthenticator(raw, secret []byte) (present, valid bool) {
	l := int(binary.BigEndian.Uint16(raw[2:4]))
	if l > len(raw) || l < 20 {
		return false, false
	}
	for i := 20; i+2 <= l; {
		t, al := raw[i], int(raw[i+1])
		if al < 2 || i+al > l {
			return false, false
		}
		if radius.Type(t) == rfc2869.MessageAuthenticator_Type && al == 18 {
			cp := append([]byte(nil), raw[:l]...)
			given := append([]byte(nil), cp[i+2:i+18]...)
			for j := i + 2; j < i+18; j++ {
				cp[j] = 0
			}
			switch radius.Code(cp[0]) {
			case radius.CodeAccountingRequest, radius.CodeDisconnectRequest, radius.CodeCoARequest:
				for j := 4; j < 20; j++ { // RFC 5176 / FreeRADIUS: Request-Authenticator genullt
					cp[j] = 0
				}
			}
			m := hmac.New(md5.New, secret)
			m.Write(cp)
			return true, hmac.Equal(m.Sum(nil), given)
		}
		i += al
	}
	return false, false
}

// encodeWithMA kodiert die Antwort und fügt (falls gewünscht) einen
// Message-Authenticator hinzu – Pflicht für EAP, schützt aber auch sonst.
func encodeWithMA(r *radius.Packet, withMA bool) ([]byte, error) {
	if !withMA {
		return r.Encode()
	}
	rfc2869.MessageAuthenticator_Del(r)
	// An erster Stelle (Empfehlung gegen Blast-RADIUS).
	r.Attributes = append(radius.Attributes{&radius.AVP{Type: rfc2869.MessageAuthenticator_Type, Attribute: make(radius.Attribute, 16)}}, r.Attributes...)
	b, err := r.MarshalBinary() // Authenticator = Request-Authenticator
	if err != nil {
		return nil, err
	}
	m := hmac.New(md5.New, r.Secret)
	m.Write(b)
	sum := m.Sum(nil)
	for i, a := range r.Attributes {
		if a.Type == rfc2869.MessageAuthenticator_Type {
			r.Attributes[i].Attribute = radius.Attribute(sum)
		}
	}
	out, err := r.Encode()
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(out[:2], b[:2]) {
		return nil, fmt.Errorf("interner Kodierfehler")
	}
	return out, nil
}

// hasTunnelAttrs: eigene Tunnel-/VLAN-Attribute ersetzen das einfache VLAN-Feld.
func hasTunnelAttrs(list []attrs.Attr) bool {
	for _, a := range list {
		if a.VendorID == 0 && (a.Code == 64 || a.Code == 65 || a.Code == 81) {
			return true
		}
	}
	return false
}

// addAttrs hängt die konfigurierten Antwortattribute an.
func addAttrs(r *radius.Packet, list []attrs.Attr) {
	for _, a := range list {
		v, err := a.Encode()
		if err != nil {
			continue
		}
		if a.VendorID == 0 {
			r.Add(radius.Type(a.Code), radius.Attribute(v))
			continue
		}
		vsa, err := radius.NewVendorSpecific(a.VendorID, append([]byte{a.Code, byte(len(v) + 2)}, v...))
		if err == nil {
			r.Add(rfc2865.VendorSpecific_Type, vsa)
		}
	}
}
