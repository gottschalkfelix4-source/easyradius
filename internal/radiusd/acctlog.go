package radiusd

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// AcctRecord ist eine abgeschlossene Sitzung (aus Accounting-Stop).
type AcctRecord struct {
	User      string    `json:"user"`
	Client    string    `json:"client"`
	Station   string    `json:"station"`
	IP        string    `json:"ip,omitempty"`
	SessionID string    `json:"sessionId,omitempty"`
	Start     time.Time `json:"start"`
	Stop      time.Time `json:"stop"`
	Seconds   int64     `json:"seconds"`
	InBytes   uint64    `json:"inBytes"`
	OutBytes  uint64    `json:"outBytes"`
	Cause     string    `json:"cause,omitempty"`
}

// acctLog schreibt abgeschlossene Sitzungen monatsweise in accounting/JJJJ-MM.jsonl.
type acctLog struct {
	mu  sync.Mutex
	dir string
}

func (a *acctLog) write(r AcctRecord) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dir == "" {
		return
	}
	os.MkdirAll(a.dir, 0o700)
	f, err := os.OpenFile(filepath.Join(a.dir, r.Stop.Format("2006-01")+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(r)
	f.Write(append(b, '\n'))
}

// AccountingMonths listet die Monate mit Einträgen (neueste zuerst).
func (s *Server) AccountingMonths() []string {
	out := []string{}
	if s.acct.dir == "" {
		return out
	}
	files, _ := filepath.Glob(filepath.Join(s.acct.dir, "*.jsonl"))
	for _, f := range files {
		out = append(out, strings.TrimSuffix(filepath.Base(f), ".jsonl"))
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

// Accounting liefert die Sitzungen eines Monats ("JJJJ-MM"), neueste zuerst.
func (s *Server) Accounting(month string) []AcctRecord {
	out := []AcctRecord{}
	if _, err := time.Parse("2006-01", month); err != nil || s.acct.dir == "" {
		return out
	}
	s.acct.mu.Lock()
	defer s.acct.mu.Unlock()
	f, err := os.Open(filepath.Join(s.acct.dir, month+".jsonl"))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var r AcctRecord
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			out = append(out, r)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// pruneAccounting löscht Monate, die älter als 13 Monate sind.
func (s *Server) pruneAccounting() {
	limit := time.Now().AddDate(0, -13, 0).Format("2006-01")
	for _, m := range s.AccountingMonths() {
		if m < limit {
			os.Remove(filepath.Join(s.acct.dir, m+".jsonl"))
		}
	}
}

func terminateCause(v interface{ String() string }) string {
	s := v.String()
	if s == "" || s == "0" || strings.HasPrefix(s, "AcctTerminateCause(0") {
		return ""
	}
	return s
}

// ---- Offene Sitzungen über einen Neustart retten ----

func (s *Server) onlinePath() string {
	if s.DataDir == "" {
		return ""
	}
	return filepath.Join(s.DataDir, "online.json")
}

func (s *Server) saveOnline() {
	p := s.onlinePath()
	if p == "" {
		return
	}
	s.acctMu.Lock()
	if !s.onlineDirty {
		s.acctMu.Unlock()
		return
	}
	list := make([]*OnlineSession, 0, len(s.online))
	for _, o := range s.online {
		cp := *o
		list = append(list, &cp)
	}
	s.onlineDirty = false
	s.acctMu.Unlock()
	b, _ := json.Marshal(list)
	if os.WriteFile(p+".tmp", b, 0o600) == nil {
		os.Rename(p+".tmp", p)
	}
}

func (s *Server) loadOnline() {
	p := s.onlinePath()
	if p == "" {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var list []*OnlineSession
	if json.Unmarshal(b, &list) != nil {
		return
	}
	s.acctMu.Lock()
	defer s.acctMu.Unlock()
	for _, o := range list {
		if o != nil && o.Key != "" && time.Since(o.Updated) < 24*time.Hour {
			s.online[o.Key] = o
		}
	}
}
