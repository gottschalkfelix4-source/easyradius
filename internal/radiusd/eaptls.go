package radiusd

// EAP-TLS (RFC 5216): Anmeldung mit Benutzerzertifikat der eigenen CA.

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
)

// certChecker prüft das (bereits gegen die CA verifizierte) Benutzerzertifikat
// inkl. Sperrliste und Gruppenregeln. Rückgabe: Benutzername, Ablehnungsgrund ("" = in Ordnung).
type certChecker func(leaf *x509.Certificate) (user, reason string)

func newEAPTLSSession(cert *tls.Certificate, clientCAs *x509.CertPool, check certChecker, serverID string) *tunnelSession {
	s := newTunnelSession(cert, eapTypeTLS, serverID)
	s.tlsHint = " (Benutzerzertifikat fehlt oder stammt nicht von der EasyRadius-CA?)"
	s.tlsConf.ClientAuth = tls.RequireAndVerifyClientCert
	s.tlsConf.ClientCAs = clientCAs
	s.tlsConf.VerifyConnection = func(cs tls.ConnectionState) error {
		// Nur direkt von der eigenen CA ausgestellte Zertifikate (keine Zwischen-CAs).
		if len(cs.VerifiedChains) == 0 || len(cs.VerifiedChains[0]) != 2 || len(cs.PeerCertificates) == 0 {
			s.certReason = "Benutzerzertifikat wurde nicht von der EasyRadius-CA ausgestellt"
			return errors.New(s.certReason)
		}
		user, why := check(cs.PeerCertificates[0])
		if user != "" {
			s.result.user = user
		}
		if why != "" {
			s.certReason = why
			return errors.New(why)
		}
		return nil
	}
	s.phase = s.tlsPhase
	return s
}

// tlsPhase: Das Zertifikat ist bereits im Handshake geprüft; der Client bestätigt
// unser "Finished" mit einer leeren Nachricht, danach folgt EAP-Success.
func (s *tunnelSession) tlsPhase(tc *tls.Conn) {
	s.result.method = "EAP-TLS"
	d, err := s.conn.awaitInput()
	if err != nil {
		return
	}
	if len(d) != 0 {
		s.result.reason = "Unerwartete Daten nach dem TLS-Handshake"
		return
	}
	msk, err := s.deriveMSK(tc)
	if err != nil {
		s.result.reason = "Schlüsselableitung fehlgeschlagen: " + err.Error()
		return
	}
	s.result.ok = true
	s.result.reason = ""
	s.result.msk = msk[:64]
}
