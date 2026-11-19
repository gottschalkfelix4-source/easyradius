package radiusd

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"hash"
	"strings"
	"sync"
)

// keyLog fängt das TLS-Master-Secret ab (Format NSS-Keylog, "CLIENT_RANDOM ...").
// Go stellt es sonst nicht bereit; für PEAP/EAP-TTLS/EAP-TLS brauchen wir es zur Schlüsselableitung
// auch bei Clients ohne "Extended Master Secret".
type keyLog struct {
	mu           sync.Mutex
	clientRandom []byte
	master       []byte
}

func (k *keyLog) Write(p []byte) (int, error) {
	for _, line := range strings.Split(string(p), "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && f[0] == "CLIENT_RANDOM" {
			cr, err1 := hex.DecodeString(f[1])
			ms, err2 := hex.DecodeString(f[2])
			if err1 == nil && err2 == nil {
				k.mu.Lock()
				k.clientRandom, k.master = cr, ms
				k.mu.Unlock()
			}
		}
	}
	return len(p), nil
}

// helloRandom liest das 32-Byte-Random aus einem Handshake-Record (ClientHello=1, ServerHello=2).
func helloRandom(rec []byte, msgType byte) ([]byte, error) {
	// Record-Header (5) + Handshake-Header (4) + Version (2) + Random (32)
	if len(rec) < 43 || rec[0] != 0x16 || rec[5] != msgType {
		return nil, errors.New("Hello-Nachricht nicht gefunden")
	}
	return rec[11:43], nil
}

// deriveMSK berechnet MSK/EMSK nach RFC 5216: PRF(master, "client EAP encryption", client_random || server_random).
func (s *tunnelSession) deriveMSK(tc *tls.Conn) ([]byte, error) {
	return s.exportKey(tc, "client EAP encryption", 128)
}

// exportKey berechnet PRF(master, label, client_random || server_random) mit n Byte
// (RFC 5216 für PEAP/EAP-TLS, RFC 5281 für EAP-TTLS).
func (s *tunnelSession) exportKey(tc *tls.Conn, label string, n int) ([]byte, error) {
	cr, err := helloRandom(s.conn.firstIn, 1)
	if err != nil {
		return nil, err
	}
	sr, err := helloRandom(s.conn.firstOut, 2)
	if err != nil {
		return nil, err
	}
	s.keylog.mu.Lock()
	master, logCR := s.keylog.master, s.keylog.clientRandom
	s.keylog.mu.Unlock()
	if master == nil || !bytes.Equal(logCR, cr) {
		return nil, errors.New("Master-Secret fehlt")
	}
	h := sha256.New
	if strings.HasSuffix(tls.CipherSuiteName(tc.ConnectionState().CipherSuite), "SHA384") {
		h = sha512.New384
	}
	return prf12(h, master, []byte(label), append(append([]byte{}, cr...), sr...), n), nil
}

// prf12 ist die TLS-1.2-PRF (RFC 5246 Abschnitt 5).
func prf12(h func() hash.Hash, secret, label, seed []byte, n int) []byte {
	seed = append(append([]byte{}, label...), seed...)
	out := make([]byte, 0, n)
	m := hmac.New(h, secret)
	m.Write(seed)
	a := m.Sum(nil)
	for len(out) < n {
		m.Reset()
		m.Write(a)
		m.Write(seed)
		out = append(out, m.Sum(nil)...)
		m.Reset()
		m.Write(a)
		a = m.Sum(nil)
	}
	return out[:n]
}
