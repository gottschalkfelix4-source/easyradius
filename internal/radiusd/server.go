// Package radiusd ist der eigentliche RADIUS-Server (Authentifizierung + Accounting).
package radiusd

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
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

	dirs        dirCache
	dirOverride directory.Directory // nur für Tests
}

type cachedResp struct {
	data    []byte
	expires time.Time
}

type OnlineSession struct {
	ID       string    `json:"id"`
	User     string    `json:"user"`
	Client   string    `json:"client"`
	Station  string    `json:"station"`
	IP       string    `json:"ip"`
	Started  time.Time `json:"started"`
	Updated  time.Time `json:"updated"`
	InBytes  uint64    `json:"inBytes"`
	OutBytes uint64    `json:"outBytes"`
}

type eapState struct {
	mu        sync.Mutex
	peap      *peapSession
	outerUser string
	lastSeen  time.Time
	ident     *identity
	dec       decision
}

func New(st *store.Store, lg *events.Log, cm *certs.Manager) *Server {
	s := &Server{
		Store: st, Log: lg, Certs: cm,
		sessions: map[string]*eapState{},
		cache:    map[string]cachedResp{},
		unknown:  map[string]time.Time{},
		online:   map[string]*OnlineSession{},
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
	s.running = true
	s.startedAt = time.Now()
	s.lastErr = ""
	go s.serve(auth)
	go s.serve(acct)
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
			if now.Sub(v.lastSeen) > 60*time.Second {
				if v.peap != nil {
					v.peap.abort()
				}
				delete(s.sessions, k)
			}
		}
		s.sessMu.Unlock()
		s.acctMu.Lock()
		for k, v := range s.online {
			if now.Sub(v.Updated) > 24*time.Hour {
				delete(s.online, k)
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
	}
}

func (s *Server) serve(conn net.PacketConn) {
	buf := make([]byte, radius.MaxPacketLength)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			if strings.Contains(err.Error(), "closed") {
				return
			}
			// Unter Windows liefert UDP nach ICMP "Port unreachable" Fehler – ignorieren.
			continue
		}
		pkt := append([]byte(nil), buf[:n]...)
		go s.handle(conn, addr, pkt)
	}
}

func (s *Server) handle(conn net.PacketConn, addr net.Addr, raw []byte) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Fehler bei Anfrage von %v: %v", addr, r)
		}
	}()
	if len(raw) < 20 {
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
		s.unknown[ip.String()] = time.Now()
		s.unknownMu.Unlock()
		if !seen || time.Since(last) > 10*time.Minute {
			s.Log.Add(events.Event{Kind: events.Warning, ClientIP: ip.String(),
				Message: "Anfrage von unbekanntem Gerät " + ip.String() + " ignoriert – unter „Geräte“ freigeben"})
		}
		return
	}
	secret := []byte(client.Secret)

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

	if !radius.IsAuthenticRequest(raw, secret) {
		s.Log.Add(events.Event{Kind: events.Warning, Client: client.Name, ClientIP: ip.String(),
			Message: "Accounting-Paket mit falschem Shared Secret verworfen"})
		return
	}
	present, valid := checkMessageAuthenticator(raw, secret)
	if present && !valid {
		s.Log.Add(events.Event{Kind: events.Warning, Client: client.Name, ClientIP: ip.String(),
			Message: "Falsches Shared Secret (Message-Authenticator ungültig) – Secret auf dem Gerät prüfen"})
		return
	}
	p, err := radius.Parse(raw, secret)
	if err != nil {
		return
	}
	s.Store.TouchClient(client.ID)

	var resp *radius.Packet
	switch p.Code {
	case radius.CodeAccessRequest:
		resp = s.handleAccess(p, &client, ip, present)
	case radius.CodeAccountingRequest:
		resp = s.handleAccounting(p, &client, ip)
	case radius.CodeStatusServer:
		resp = p.Response(radius.CodeAccessAccept)
	default:
		return
	}
	if resp == nil {
		return
	}
	out, err := encodeWithMA(resp, p.Code != radius.CodeAccountingRequest)
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
			ev.Kind, ev.Message = events.Reject, "EAP-Anfrage ohne Message-Authenticator abgelehnt"
			s.Log.Add(ev)
			return p.Response(radius.CodeAccessReject)
		}
		return s.handleEAP(p, c, eap, ev)
	}

	rc := reqCtx{client: *c, ssid: ssidFromCalled(rfc2865.CalledStationID_GetString(p)), req: p}

	if pw, err := rfc2865.UserPassword_LookupString(p); err == nil {
		ev.Method = "PAP"
		id, reason := s.verifyPAP(ev.User, pw)
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
		id, res, reason := s.verifyMSCHAP(ev.User, chal, resp[2:18], resp[26:50])
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
	rfc2865.ReplyMessage_SetString(r, reason)
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
		rfc2868.TunnelPrivateGroupID_AddString(r, 0, dec.VLAN)
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
	id, typ, data := eap[1], eap[4], eap[5:]
	if l := int(binary.BigEndian.Uint16(eap[2:4])); l <= len(eap) && l >= 5 {
		data = eap[5:l]
	}

	state := rfc2865.State_Get(p)
	if typ == eapTypeIdentity || len(state) == 0 {
		if typ != eapTypeIdentity {
			return s.eapReject(p, ev, id, "EAP ohne Identität begonnen")
		}
		// Neue Sitzung: PEAP anbieten.
		st := &eapState{outerUser: string(data), lastSeen: time.Now()}
		rc := reqCtx{client: *c, ssid: ssidFromCalled(rfc2865.CalledStationID_GetString(p)), req: p}
		verify := func(name string, authC, peerC, ntResp []byte) (string, string, string) {
			ident, res, why := s.verifyMSCHAP(name, authC, peerC, ntResp)
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
		ps := newPEAPSession(s.Certs.Certificate(), verify, s.Store.Settings().ServerName)
		if !ps.start() {
			return s.eapReject(p, ev, id, "PEAP konnte nicht gestartet werden")
		}
		st.peap = ps
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
	st.lastSeen = time.Now()
	ps := st.peap

	if typ == eapTypeNak {
		s.dropSession(sid)
		return s.eapReject(p, ev, id, "Gerät möchte kein PEAP – im WLAN-Profil „PEAP“ mit „MSCHAPv2“ wählen")
	}
	if typ != eapTypePEAP || len(data) < 1 {
		s.dropSession(sid)
		return s.eapReject(p, ev, id, fmt.Sprintf("Nicht unterstützte EAP-Methode %d", typ))
	}
	flags := data[0]
	payload := data[1:]
	if flags&peapFlagLength != 0 && len(payload) >= 4 {
		payload = payload[4:]
	}

	// Client bestätigt ein Fragment von uns → nächstes schicken.
	if len(ps.pendingOut) > 0 && len(payload) == 0 {
		return s.challenge(p, sid, buildEAP(eapRequest, id+1, eapTypePEAP, ps.nextFragment()))
	}

	ps.inAccum = append(ps.inAccum, payload...)
	if flags&peapFlagMore != 0 {
		return s.challenge(p, sid, buildEAP(eapRequest, id+1, eapTypePEAP, []byte{0}))
	}
	msg := ps.inAccum
	ps.inAccum = nil

	out, finished := ps.exchange(msg)
	if finished {
		s.dropSession(sid)
		res := ps.result
		if res.user != "" {
			ev.User = res.user
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
	return s.challenge(p, sid, buildEAP(eapRequest, id+1, eapTypePEAP, ps.nextFragment()))
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
	if st := s.sessions[sid]; st != nil && st.peap != nil {
		st.peap.abort()
	}
	delete(s.sessions, sid)
	s.sessMu.Unlock()
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
	key := c.ID + "|" + sid
	user := rfc2865.UserName_GetString(p)
	station := rfc2865.CallingStationID_GetString(p)
	now := time.Now()

	s.acctMu.Lock()
	switch typ {
	case rfc2866.AcctStatusType_Value_Start, rfc2866.AcctStatusType_Value_InterimUpdate:
		o := s.online[key]
		if o == nil {
			o = &OnlineSession{ID: sid, User: user, Client: c.Name, Station: station, Started: now}
			s.online[key] = o
		}
		o.Updated = now
		if fip := rfc2865.FramedIPAddress_Get(p); fip != nil {
			o.IP = fip.String()
		}
		o.InBytes = uint64(rfc2866.AcctInputOctets_Get(p)) + uint64(rfc2869.AcctInputGigawords_Get(p))<<32
		o.OutBytes = uint64(rfc2866.AcctOutputOctets_Get(p)) + uint64(rfc2869.AcctOutputGigawords_Get(p))<<32
	case rfc2866.AcctStatusType_Value_Stop:
		delete(s.online, key)
	case rfc2866.AcctStatusType_Value_AccountingOn, rfc2866.AcctStatusType_Value_AccountingOff:
		for k := range s.online {
			if strings.HasPrefix(k, c.ID+"|") {
				delete(s.online, k)
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
	r.Add(rfc2869.MessageAuthenticator_Type, make(radius.Attribute, 16))
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
