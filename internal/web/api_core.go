package web

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gottschalkfelix4-source/easyradius/internal/auth"
	"github.com/gottschalkfelix4-source/easyradius/internal/store"
)

// ---- Benutzer ----

type userOut struct {
	store.User
	NTHash  string `json:"ntHash,omitempty"`
	Expired bool   `json:"expired"`
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	out := []userOut{}
	for _, u := range s.Store.Users() {
		out = append(out, userOut{User: u, Expired: u.Expired(time.Now())})
	}
	writeJSON(w, 200, out)
}

func validVLAN(v string) error {
	if v == "" {
		return nil
	}
	if n, err := strconv.Atoi(v); err != nil || n < 1 || n > 4094 {
		return errors.New("VLAN muss eine Zahl zwischen 1 und 4094 sein")
	}
	return nil
}

func validDate(d string) error {
	if d == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", d); err != nil {
		return errors.New("Ablaufdatum ungültig (JJJJ-MM-TT)")
	}
	return nil
}

func (s *Server) saveUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		store.User
		Password string `json:"password"`
	}
	if readJSON(r, &in) != nil {
		fail(w, 400, "Ungültige Eingabe")
		return
	}
	u := in.User
	u.ID = r.PathValue("id")
	u.NTHash = ""
	if in.Password != "" {
		u.NTHash = hex.EncodeToString(auth.NTHash(in.Password))
	}
	for _, err := range []error{validVLAN(u.VLAN), validDate(u.ExpiresAt)} {
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	if u.GroupID != "" {
		if _, ok := s.Store.Group(u.GroupID); !ok {
			fail(w, 400, "Gruppe nicht gefunden")
			return
		}
	}
	saved, err := s.Store.SaveUser(u)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.audit(r, "Benutzer „"+saved.Username+"“ gespeichert")
	saved.NTHash = ""
	writeJSON(w, 200, saved)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("id")
	for _, u := range s.Store.Users() {
		if u.ID == name {
			name = u.Username
		}
	}
	if err := s.Store.DeleteUser(r.PathValue("id")); err != nil {
		fail(w, 404, err.Error())
		return
	}
	s.audit(r, "Benutzer „"+name+"“ gelöscht")
	ok(w)
}

// ---- Geräte ----

// clientOut liefert Geräte ohne Shared Secret – das gibt es nur einzeln auf Abruf.
type clientOut struct {
	store.Client
	Secret    string `json:"secret,omitempty"`
	HasSecret bool   `json:"hasSecret"`
}

func (s *Server) listClients(w http.ResponseWriter, r *http.Request) {
	out := []clientOut{}
	for _, c := range s.Store.Clients() {
		out = append(out, clientOut{Client: c, HasSecret: c.Secret != ""})
	}
	writeJSON(w, 200, out)
}

func (s *Server) clientSecret(w http.ResponseWriter, r *http.Request) {
	c, found := s.Store.Client(r.PathValue("id"))
	if !found {
		fail(w, 404, "Gerät nicht gefunden")
		return
	}
	s.audit(r, "Shared Secret von „"+c.Name+"“ angezeigt")
	writeJSON(w, 200, map[string]string{"secret": c.Secret})
}

func (s *Server) saveClient(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name, Address, Secret, Note string
		RequireMA                   *bool `json:"requireMA"`
		CoAPort                     int   `json:"coaPort"`
	}
	if readJSON(r, &in) != nil {
		fail(w, 400, "Ungültige Eingabe")
		return
	}
	c := store.Client{ID: r.PathValue("id"), Name: in.Name, Address: in.Address, Secret: in.Secret, Note: in.Note, CoAPort: in.CoAPort}
	switch {
	case in.RequireMA != nil:
		c.RequireMA = *in.RequireMA
	case c.ID == "":
		c.RequireMA = true // neue Geräte: Schutz vor Blast-RADIUS standardmäßig an
	default:
		if old, found := s.Store.Client(c.ID); found {
			c.RequireMA = old.RequireMA
		}
	}
	if c.Secret != "" && len(c.Secret) < 8 {
		fail(w, 400, "Das Shared Secret sollte mindestens 8 Zeichen haben")
		return
	}
	saved, err := s.Store.SaveClient(c)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.audit(r, "Gerät „"+saved.Name+"“ ("+saved.Address+") gespeichert")
	writeJSON(w, 200, clientOut{Client: saved, HasSecret: true})
}

func (s *Server) deleteClient(w http.ResponseWriter, r *http.Request) {
	c, _ := s.Store.Client(r.PathValue("id"))
	if err := s.Store.DeleteClient(r.PathValue("id")); err != nil {
		fail(w, 404, err.Error())
		return
	}
	s.audit(r, "Gerät „"+c.Name+"“ gelöscht")
	ok(w)
}

// ---- MAC-Adressen ----

type macOut struct {
	store.MACEntry
	Expired bool `json:"expired"`
}

func (s *Server) listMACs(w http.ResponseWriter, r *http.Request) {
	out := []macOut{}
	for _, m := range s.Store.MACs() {
		out = append(out, macOut{MACEntry: m, Expired: m.Expired(time.Now())})
	}
	writeJSON(w, 200, out)
}

func (s *Server) saveMAC(w http.ResponseWriter, r *http.Request) {
	var m store.MACEntry
	if readJSON(r, &m) != nil {
		fail(w, 400, "Ungültige Eingabe")
		return
	}
	m.ID = r.PathValue("id")
	for _, err := range []error{validVLAN(m.VLAN), validDate(m.ExpiresAt)} {
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	if m.GroupID != "" {
		if _, ok := s.Store.Group(m.GroupID); !ok {
			fail(w, 400, "Gruppe nicht gefunden")
			return
		}
	}
	saved, err := s.Store.SaveMAC(m)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.audit(r, "MAC-Adresse "+saved.MAC+" gespeichert")
	writeJSON(w, 200, saved)
}

func (s *Server) deleteMAC(w http.ResponseWriter, r *http.Request) {
	mac := r.PathValue("id")
	for _, m := range s.Store.MACs() {
		if m.ID == mac {
			mac = m.MAC
		}
	}
	if err := s.Store.DeleteMAC(r.PathValue("id")); err != nil {
		fail(w, 404, err.Error())
		return
	}
	s.audit(r, "MAC-Adresse "+mac+" gelöscht")
	ok(w)
}

// ---- Sitzungen, Accounting, Sperren ----

func (s *Server) listOnline(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Radius.Online())
}

func (s *Server) disconnect(w http.ResponseWriter, r *http.Request) {
	var in struct{ Key string }
	if readJSON(r, &in) != nil || in.Key == "" {
		fail(w, 400, "Sitzung fehlt")
		return
	}
	if err := s.Radius.Disconnect(in.Key, principalOf(r).name); err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.audit(r, "Sitzung getrennt")
	ok(w)
}

func (s *Server) listAccounting(w http.ResponseWriter, r *http.Request) {
	months := s.Radius.AccountingMonths()
	month := r.URL.Query().Get("month")
	if month == "" && len(months) > 0 {
		month = months[0]
	}
	writeJSON(w, 200, map[string]any{"months": months, "month": month, "records": s.Radius.Accounting(month)})
}

func (s *Server) exportAccountingCSV(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	if _, err := time.Parse("2006-01", month); err != nil {
		fail(w, 400, "Monat fehlt (JJJJ-MM)")
		return
	}
	rows := [][]string{{"Benutzer", "Gerät", "Endgerät", "IP", "Beginn", "Ende", "Sekunden", "Empfangen (Byte)", "Gesendet (Byte)", "Grund"}}
	for _, a := range s.Radius.Accounting(month) {
		rows = append(rows, []string{a.User, a.Client, a.Station, a.IP, a.Start.Local().Format("2006-01-02 15:04:05"),
			a.Stop.Local().Format("2006-01-02 15:04:05"), strconv.FormatInt(a.Seconds, 10),
			strconv.FormatUint(a.InBytes, 10), strconv.FormatUint(a.OutBytes, 10), a.Cause})
	}
	writeCSV(w, "EasyRadius-Accounting-"+month+".csv", rows)
}

func (s *Server) listLockouts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Radius.Lockouts())
}

func (s *Server) unlock(w http.ResponseWriter, r *http.Request) {
	var in struct{ Key string }
	readJSON(r, &in)
	s.Radius.Unlock(in.Key)
	if in.Key == "" {
		s.audit(r, "Alle Sperren aufgehoben")
	} else {
		s.audit(r, "Sperre „"+in.Key+"“ aufgehoben")
	}
	ok(w)
}

// ---- CSV ----

// writeCSV schreibt eine CSV-Datei, die Excel ohne Nachfrage richtig öffnet
// (UTF-8 mit BOM, Semikolon).
func writeCSV(w http.ResponseWriter, name string, rows [][]string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	for _, row := range rows {
		for i, v := range row { // keine Formeln in Excel einschleusen
			if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
				row[i] = "'" + v
			}
		}
		cw.Write(row)
	}
	cw.Flush()
}

// readCSV liest eine hochgeladene CSV-Datei (Trennzeichen ; oder , automatisch).
// Die erste Zeile enthält die Spaltennamen.
func readCSV(r *http.Request) ([]map[string]string, error) {
	f, _, err := r.FormFile("file")
	if err != nil {
		return nil, errors.New("Bitte eine CSV-Datei auswählen")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxUploadBody))
	if err != nil {
		return nil, err
	}
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	first, _, _ := bufio.NewReader(bytes.NewReader(raw)).ReadLine()
	cr := csv.NewReader(bytes.NewReader(raw))
	cr.Comma = ';'
	if strings.Count(string(first), ",") > strings.Count(string(first), ";") {
		cr.Comma = ','
	}
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	records, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("CSV nicht lesbar: %v", err)
	}
	if len(records) < 2 {
		return nil, errors.New("Die Datei enthält keine Einträge")
	}
	if len(records) > 20001 {
		return nil, errors.New("Höchstens 20000 Einträge pro Import")
	}
	head := make([]string, len(records[0]))
	for i, h := range records[0] {
		head[i] = csvKey(h)
	}
	var out []map[string]string
	for _, rec := range records[1:] {
		row := map[string]string{}
		empty := true
		for i, v := range rec {
			if i < len(head) && head[i] != "" {
				v = strings.TrimSpace(strings.TrimPrefix(v, "'"))
				row[head[i]] = v
				if v != "" {
					empty = false
				}
			}
		}
		if !empty {
			out = append(out, row)
		}
	}
	return out, nil
}

// csvKey ordnet deutsche und englische Spaltennamen zu.
func csvKey(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	switch h {
	case "benutzername", "username", "user", "benutzer":
		return "username"
	case "name", "vollständiger name", "fullname", "full name", "anzeigename", "bezeichnung":
		return "name"
	case "passwort", "password", "kennwort":
		return "password"
	case "gruppe", "group":
		return "group"
	case "vlan":
		return "vlan"
	case "aktiv", "enabled", "active":
		return "enabled"
	case "läuft ab", "ablaufdatum", "expires", "expiresat", "gültig bis":
		return "expires"
	case "notiz", "note", "bemerkung":
		return "note"
	case "mac", "mac-adresse", "macadresse", "mac address":
		return "mac"
	}
	return ""
}

func csvBool(v string, def bool) bool {
	switch strings.ToLower(v) {
	case "":
		return def
	case "ja", "yes", "true", "1", "x", "aktiv":
		return true
	}
	return false
}

func (s *Server) csvGroup(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	g, ok := s.Store.GroupByName(name)
	if !ok {
		return "", errors.New("Gruppe „" + name + "“ gibt es nicht")
	}
	return g.ID, nil
}

func (s *Server) exportUsersCSV(w http.ResponseWriter, r *http.Request) {
	groups := map[string]string{}
	for _, g := range s.Store.Groups() {
		groups[g.ID] = g.Name
	}
	rows := [][]string{{"Benutzername", "Name", "Gruppe", "VLAN", "Aktiv", "Läuft ab", "Notiz"}}
	for _, u := range s.Store.Users() {
		rows = append(rows, []string{u.Username, u.FullName, groups[u.GroupID], u.VLAN, yesNo(u.Enabled), u.ExpiresAt, u.Note})
	}
	writeCSV(w, "EasyRadius-Benutzer.csv", rows)
}

func yesNo(b bool) string {
	if b {
		return "ja"
	}
	return "nein"
}

func (s *Server) importUsersCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := readCSV(r)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	existing := map[string]store.User{}
	for _, u := range s.Store.Users() {
		existing[strings.ToLower(u.Username)] = u
	}
	var list []store.User
	for i, row := range rows {
		line := fmt.Sprintf("Zeile %d: ", i+2)
		u, known := existing[strings.ToLower(row["username"])]
		u.ID = ""
		u.NTHash = ""
		u.Username = row["username"]
		if v, ok := row["name"]; ok {
			u.FullName = v
		}
		if v, ok := row["password"]; ok && v != "" {
			u.NTHash = hex.EncodeToString(auth.NTHash(v))
		}
		if v, ok := row["group"]; ok {
			gid, err := s.csvGroup(v)
			if err != nil {
				fail(w, 400, line+err.Error())
				return
			}
			u.GroupID = gid
		}
		if v, ok := row["vlan"]; ok {
			u.VLAN = v
		}
		u.Enabled = csvBool(row["enabled"], !known || u.Enabled)
		if v, ok := row["expires"]; ok {
			u.ExpiresAt = v
		}
		if v, ok := row["note"]; ok {
			u.Note = v
		}
		for _, err := range []error{validVLAN(u.VLAN), validDate(u.ExpiresAt)} {
			if err != nil {
				fail(w, 400, line+err.Error())
				return
			}
		}
		list = append(list, u)
	}
	created, updated, err := s.Store.ImportUsers(list)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.audit(r, fmt.Sprintf("Benutzer importiert: %d neu, %d aktualisiert", created, updated))
	writeJSON(w, 200, map[string]int{"created": created, "updated": updated})
}

func (s *Server) exportMACsCSV(w http.ResponseWriter, r *http.Request) {
	groups := map[string]string{}
	for _, g := range s.Store.Groups() {
		groups[g.ID] = g.Name
	}
	rows := [][]string{{"MAC", "Name", "Gruppe", "VLAN", "Aktiv", "Läuft ab", "Notiz"}}
	for _, m := range s.Store.MACs() {
		rows = append(rows, []string{m.MAC, m.Name, groups[m.GroupID], m.VLAN, yesNo(m.Enabled), m.ExpiresAt, m.Note})
	}
	writeCSV(w, "EasyRadius-MAC-Adressen.csv", rows)
}

func (s *Server) importMACsCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := readCSV(r)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	var list []store.MACEntry
	for i, row := range rows {
		line := fmt.Sprintf("Zeile %d: ", i+2)
		m := store.MACEntry{MAC: row["mac"], Name: row["name"], VLAN: row["vlan"], Enabled: csvBool(row["enabled"], true),
			ExpiresAt: row["expires"], Note: row["note"]}
		if store.NormalizeMAC(m.MAC) == "" {
			fail(w, 400, line+"MAC-Adresse ungültig")
			return
		}
		gid, err := s.csvGroup(row["group"])
		if err != nil {
			fail(w, 400, line+err.Error())
			return
		}
		m.GroupID = gid
		for _, err := range []error{validVLAN(m.VLAN), validDate(m.ExpiresAt)} {
			if err != nil {
				fail(w, 400, line+err.Error())
				return
			}
		}
		list = append(list, m)
	}
	created, updated, err := s.Store.ImportMACs(list)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.audit(r, fmt.Sprintf("MAC-Adressen importiert: %d neu, %d aktualisiert", created, updated))
	writeJSON(w, 200, map[string]int{"created": created, "updated": updated})
}
