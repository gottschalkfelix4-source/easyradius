package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Geheimnisse (Shared Secrets, Passwort-Hashes, Zugangsdaten) liegen in der
// config.json verschlüsselt. Der Schlüssel steht in einer eigenen Datei –
// unter Windows zusätzlich per DPAPI an diesen Rechner gebunden. Eine kopierte
// config.json allein verrät damit keine Geheimnisse.

const sealPrefix = "enc:v1:"

func loadKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, "secret.key")
	b, err := os.ReadFile(path)
	if err == nil {
		key, err := unprotectKey(b)
		if err != nil {
			return nil, err
		}
		if len(key) != 32 {
			return nil, errors.New("secret.key ist beschädigt")
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	blob, err := protectKey(key)
	if err != nil {
		return nil, err
	}
	if err := writeFileSync(path, blob); err != nil {
		return nil, err
	}
	return key, nil
}

func seal(key []byte, plain string) (string, error) {
	if plain == "" || strings.HasPrefix(plain, sealPrefix) {
		return plain, nil
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(blk)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	rand.Read(nonce)
	return sealPrefix + base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

func unseal(key []byte, v string) (string, error) {
	if !strings.HasPrefix(v, sealPrefix) {
		return v, nil // ältere Datei im Klartext
	}
	raw, err := base64.StdEncoding.DecodeString(v[len(sealPrefix):])
	if err != nil {
		return "", err
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(blk)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("verschlüsselter Wert zu kurz")
	}
	out, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("Geheimnisse lassen sich nicht entschlüsseln – gehört secret.key zu dieser config.json?")
	}
	return string(out), nil
}

// secretFields liefert Zeiger auf alle Felder, die verschlüsselt gespeichert werden.
func secretFields(d *data) []*string {
	st := &d.Settings
	out := []*string{&st.SessionKey, &st.AD.BindPassword, &st.ACME.AccountKey, &st.ACME.APIToken}
	for _, c := range d.Clients {
		out = append(out, &c.Secret)
	}
	for _, u := range d.Users {
		out = append(out, &u.NTHash)
	}
	return out
}

func sealAll(d *data, key []byte) error {
	for _, p := range secretFields(d) {
		v, err := seal(key, *p)
		if err != nil {
			return err
		}
		*p = v
	}
	for k, v := range d.Settings.ACME.Credentials {
		sv, err := seal(key, v)
		if err != nil {
			return err
		}
		d.Settings.ACME.Credentials[k] = sv
	}
	return nil
}

func unsealAll(d *data, key []byte) error {
	for _, p := range secretFields(d) {
		v, err := unseal(key, *p)
		if err != nil {
			return err
		}
		*p = v
	}
	for k, v := range d.Settings.ACME.Credentials {
		sv, err := unseal(key, v)
		if err != nil {
			return err
		}
		d.Settings.ACME.Credentials[k] = sv
	}
	return nil
}
