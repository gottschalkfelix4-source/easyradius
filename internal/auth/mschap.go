// Package auth enthält die Krypto-Bausteine für PAP und MS-CHAPv2.
package auth

import (
	"crypto/sha1"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf16"

	"golang.org/x/crypto/md4"
	"layeh.com/radius/rfc2759"
	"layeh.com/radius/rfc3079"
)

// NTHash berechnet den NT-Hash (MD4 über UTF-16LE) eines Passworts.
func NTHash(password string) []byte {
	u := utf16.Encode([]rune(password))
	b := make([]byte, len(u)*2)
	for i, c := range u {
		b[2*i] = byte(c)
		b[2*i+1] = byte(c >> 8)
	}
	h := md4.New()
	h.Write(b)
	return h.Sum(nil)
}

// CheckPAP prüft ein Klartextpasswort gegen den gespeicherten NT-Hash.
func CheckPAP(password string, ntHash []byte) bool {
	return subtle.ConstantTimeCompare(NTHash(password), ntHash) == 1
}

// stripDomain entfernt "DOMAIN\" vor dem Benutzernamen, wie es RFC 2759 verlangt.
func stripDomain(username string) string {
	if i := strings.LastIndex(username, `\`); i >= 0 {
		return username[i+1:]
	}
	return username
}

// MSCHAPv2Result enthält alles, was nach erfolgreicher Prüfung gebraucht wird.
type MSCHAPv2Result struct {
	AuthenticatorResponse string // "S=..." (40 Hex-Zeichen, Großbuchstaben)
	SendKey, RecvKey      []byte // MPPE-Schlüssel (16 Byte) für Nicht-EAP-MSCHAPv2
}

// VerifyMSCHAPv2 prüft eine NT-Response gegen den gespeicherten NT-Hash.
// username ist der Name, den der Client in seiner Antwort mitgeschickt hat.
func VerifyMSCHAPv2(authChallenge, peerChallenge, ntResponse []byte, username string, ntHash []byte) (*MSCHAPv2Result, error) {
	if len(authChallenge) != 16 || len(peerChallenge) != 16 || len(ntResponse) != 24 || len(ntHash) != 16 {
		return nil, fmt.Errorf("ungültige MS-CHAPv2-Feldlängen")
	}
	challenge := ChallengeHash(peerChallenge, authChallenge, username)
	expected := rfc2759.ChallengeResponse(challenge, ntHash)
	if subtle.ConstantTimeCompare(expected, ntResponse) != 1 {
		return nil, fmt.Errorf("falsches Passwort")
	}
	return MSCHAPv2Success(md4Sum(ntHash), ntResponse, peerChallenge, authChallenge, username), nil
}

// ChallengeHash liefert die 8-Byte-Challenge nach RFC 2759 (für die Prüfung beim Domänencontroller).
func ChallengeHash(peerChallenge, authChallenge []byte, username string) []byte {
	return rfc2759.ChallengeHash(peerChallenge, authChallenge, []byte(stripDomain(username)))
}

// MSCHAPv2Success berechnet Authenticator-Response und MPPE-Schlüssel aus dem
// Hash des NT-Hashes (so liefert ihn auch der Domänencontroller zurück).
func MSCHAPv2Success(hashHash, ntResponse, peerChallenge, authChallenge []byte, username string) *MSCHAPv2Result {
	challenge := ChallengeHash(peerChallenge, authChallenge, username)
	// RFC 2759 8.7 GenerateAuthenticatorResponse
	sha := sha1.New()
	sha.Write(hashHash)
	sha.Write(ntResponse)
	sha.Write(magic1)
	digest := sha.Sum(nil)
	sha = sha1.New()
	sha.Write(digest)
	sha.Write(challenge)
	sha.Write(magic2)
	authResp := "S=" + strings.ToUpper(hex.EncodeToString(sha.Sum(nil)))

	master := rfc3079.GetMasterKey(hashHash, ntResponse)
	send, _ := rfc3079.GetAsymmetricStartKey(master, rfc3079.KeyLength128Bit, true)
	recv, _ := rfc3079.GetAsymmetricStartKey(master, rfc3079.KeyLength128Bit, false)
	return &MSCHAPv2Result{AuthenticatorResponse: authResp, SendKey: send, RecvKey: recv}
}

// NTHashHash ist MD4 über den NT-Hash.
func NTHashHash(ntHash []byte) []byte { return md4Sum(ntHash) }

// NTResponse berechnet die Client-Antwort (nur für Selbsttests).
func NTResponse(challenge8, ntHash []byte) []byte {
	return rfc2759.ChallengeResponse(challenge8, ntHash)
}

func md4Sum(b []byte) []byte {
	h := md4.New()
	h.Write(b)
	return h.Sum(nil)
}

var (
	magic1 = []byte("Magic server to client signing constant")
	magic2 = []byte("Pad to make it do more than one iteration")
)
