package radiusd

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/gottschalkfelix4-source/easyradius/internal/events"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2866"
	"layeh.com/radius/rfc2869"
	"layeh.com/radius/rfc3576"
)

// Disconnect trennt eine laufende Sitzung per Disconnect-Request (RFC 5176).
// Das Gerät muss „Dynamic Authorization“/CoA aktiviert haben (meist Port 3799).
func (s *Server) Disconnect(key, by string) error {
	s.acctMu.Lock()
	o, ok := s.online[key]
	var sess OnlineSession
	if ok {
		sess = *o
	}
	s.acctMu.Unlock()
	if !ok {
		return errors.New("Sitzung nicht gefunden – vielleicht schon beendet")
	}
	c, ok := s.Store.Client(sess.ClientID)
	if !ok {
		return errors.New("Das Gerät dieser Sitzung ist nicht mehr eingetragen")
	}
	port := c.CoAPort
	if port == 0 {
		port = 3799
	}

	p := radius.New(radius.CodeDisconnectRequest, []byte(c.Secret))
	if sess.User != "" {
		rfc2865.UserName_SetString(p, sess.User)
	}
	if sess.ID != "" {
		rfc2866.AcctSessionID_SetString(p, sess.ID)
	}
	if sess.Station != "" {
		rfc2865.CallingStationID_SetString(p, sess.Station)
	}
	if ip := net.ParseIP(sess.NASIP).To4(); ip != nil {
		rfc2865.NASIPAddress_Set(p, ip)
	}
	if sess.NASID != "" {
		rfc2865.NASIdentifier_SetString(p, sess.NASID)
	}
	if ip := net.ParseIP(sess.IP).To4(); ip != nil {
		rfc2865.FramedIPAddress_Set(p, ip)
	}
	rfc2869.EventTimestamp_Set(p, time.Now())
	// Message-Authenticator über das Paket mit genulltem Authenticator (RFC 5176 3.3).
	p.Add(rfc2869.MessageAuthenticator_Type, make(radius.Attribute, 16))
	p.Authenticator = [16]byte{}
	raw, err := p.MarshalBinary()
	if err != nil {
		return err
	}
	m := hmac.New(md5.New, p.Secret)
	m.Write(raw)
	for _, a := range p.Attributes {
		if a.Type == rfc2869.MessageAuthenticator_Type {
			a.Attribute = m.Sum(nil)
		}
	}

	host := sess.NASAddr
	if host == "" {
		host = sess.NASIP
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	cl := radius.Client{Retry: 2 * time.Second}
	r, err := cl.Exchange(ctx, p, net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("Keine Antwort von %s auf Port %d – ist CoA/Disconnect am Gerät aktiviert und EasyRadius als Quelle erlaubt?", c.Name, port)
	}
	ev := events.Event{Kind: events.Info, User: sess.User, Client: c.Name, ClientIP: host, Station: sess.Station}
	switch r.Code {
	case radius.CodeDisconnectACK:
		s.acctMu.Lock()
		delete(s.online, key)
		s.onlineDirty = true
		s.acctMu.Unlock()
		ev.Message = "Sitzung getrennt von " + by
		s.Log.Add(ev)
		return nil
	case radius.CodeDisconnectNAK:
		cause := rfc3576.ErrorCause_Get(r)
		msg := "Das Gerät hat das Trennen abgelehnt"
		if cause != 0 {
			msg += " (" + cause.String() + ")"
		}
		ev.Kind, ev.Message = events.Warning, msg
		s.Log.Add(ev)
		return errors.New(msg)
	}
	return fmt.Errorf("Unerwartete Antwort %v", r.Code)
}
