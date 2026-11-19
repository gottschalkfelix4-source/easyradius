package auth

import (
	"encoding/hex"
	"testing"
)

func h(s string) []byte { b, _ := hex.DecodeString(s); return b }

// Testvektoren aus RFC 2759, Abschnitt 9.2
func TestMSCHAPv2RFCVectors(t *testing.T) {
	nt := NTHash("clientPass")
	if hex.EncodeToString(nt) != "44ebba8d5312b8d611474411f56989ae" {
		t.Fatalf("NT-Hash falsch: %x", nt)
	}
	res, err := VerifyMSCHAPv2(
		h("5B5D7C7D7B3F2F3E3C2C602132262628"),
		h("21402324255E262A28295F2B3A337C7E"),
		h("82309ECD8D708B5EA08FAA3981CD83544233114A3D85D6DF"),
		"User", nt)
	if err != nil {
		t.Fatal(err)
	}
	if res.AuthenticatorResponse != "S=407A5589115FD0D6209F510FE9C04566932CDA56" {
		t.Fatalf("AuthenticatorResponse falsch: %s", res.AuthenticatorResponse)
	}
	if _, err := VerifyMSCHAPv2(h("5B5D7C7D7B3F2F3E3C2C602132262628"), h("21402324255E262A28295F2B3A337C7E"),
		h("82309ECD8D708B5EA08FAA3981CD83544233114A3D85D6DF"), "User", NTHash("falsch")); err == nil {
		t.Fatal("falsches Passwort wurde akzeptiert")
	}
	if !CheckPAP("clientPass", nt) || CheckPAP("x", nt) {
		t.Fatal("PAP-Prüfung falsch")
	}
}
