package web

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"golang.org/x/crypto/scrypt"
)

// Die Sicherung enthält Konfiguration und Zertifikate – inklusive aller Geheimnisse.
// Sie ist deshalb immer mit einem Passwort verschlüsselt (scrypt + AES-256-GCM)
// und lässt sich auf einem anderen Rechner wiederherstellen.

const backupMagic = "EasyRadius-Sicherung"

type backupFile struct {
	Magic   string    `json:"magic"`
	Version string    `json:"version"`
	Created time.Time `json:"created"`
	Salt    []byte    `json:"salt"`
	Nonce   []byte    `json:"nonce"`
	Data    []byte    `json:"data"`
}

type backupContent struct {
	Config json.RawMessage   `json:"config"`
	Certs  map[string][]byte `json:"certs"`
}

func backupKey(password string, salt []byte) ([]byte, error) {
	return scrypt.Key([]byte(password), salt, 1<<15, 8, 1, 32)
}

func (s *Server) backup(w http.ResponseWriter, r *http.Request) {
	var in struct{ Password string }
	if readJSON(r, &in) != nil {
		fail(w, 400, "Ungültige Eingabe")
		return
	}
	if len(in.Password) < 8 {
		fail(w, 400, "Bitte ein Passwort mit mindestens 8 Zeichen für die Sicherung wählen")
		return
	}
	cfg, err := s.Store.Export()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	plain, _ := json.Marshal(backupContent{Config: cfg, Certs: s.Certs.Files()})
	salt := make([]byte, 16)
	nonce := make([]byte, 12)
	rand.Read(salt)
	rand.Read(nonce)
	key, err := backupKey(in.Password, salt)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	blk, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(blk)
	out, _ := json.MarshalIndent(backupFile{Magic: backupMagic, Version: s.Version, Created: time.Now(),
		Salt: salt, Nonce: nonce, Data: gcm.Seal(nil, nonce, plain, []byte(backupMagic))}, "", " ")
	s.audit(r, "Sicherung heruntergeladen")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="EasyRadius-Sicherung-`+time.Now().Format("2006-01-02")+`.erbackup"`)
	w.Write(out)
}

func openBackup(raw []byte, password string) (*backupContent, error) {
	var f backupFile
	if json.Unmarshal(raw, &f) != nil || f.Magic != backupMagic {
		return nil, errors.New("Das ist keine EasyRadius-Sicherung")
	}
	key, err := backupKey(password, f.Salt)
	if err != nil {
		return nil, err
	}
	blk, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(blk)
	if len(f.Nonce) != gcm.NonceSize() {
		return nil, errors.New("Die Sicherung ist beschädigt")
	}
	plain, err := gcm.Open(nil, f.Nonce, f.Data, []byte(backupMagic))
	if err != nil {
		return nil, errors.New("Falsches Passwort oder beschädigte Sicherung")
	}
	var c backupContent
	if err := json.Unmarshal(plain, &c); err != nil {
		return nil, errors.New("Die Sicherung ist beschädigt")
	}
	return &c, nil
}

func (s *Server) restore(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxUploadBody); err != nil {
		fail(w, 400, "Upload fehlgeschlagen")
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		fail(w, 400, "Bitte die Sicherungsdatei auswählen")
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxUploadBody))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	c, err := openBackup(raw, r.FormValue("password"))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	// Erst die Zertifikate (prüfbar), dann die Konfiguration.
	if len(c.Certs) > 0 {
		if err := s.Certs.Restore(c.Certs); err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	if err := s.Store.Import(c.Config); err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.audit(r, "Sicherung wiederhergestellt")
	if err := s.Radius.Restart(); err != nil {
		s.Log.Add(eventWarn("RADIUS nach Wiederherstellung: " + err.Error()))
	}
	// Konten können sich geändert haben: alle Sitzungen beenden.
	s.sessMu.Lock()
	s.sessions = map[string]*session{}
	s.sessMu.Unlock()
	ok(w)
}
