// Package events führt das Anmeldeprotokoll (im Speicher + als Datei).
package events

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Kind string

const (
	Accept  Kind = "accept"
	Reject  Kind = "reject"
	Info    Kind = "info"
	Warning Kind = "warning"
	Acct    Kind = "acct"
)

type Event struct {
	Time     time.Time `json:"time"`
	Kind     Kind      `json:"kind"`
	User     string    `json:"user,omitempty"`
	Client   string    `json:"client,omitempty"`
	ClientIP string    `json:"clientIp,omitempty"`
	Station  string    `json:"station,omitempty"` // MAC des Endgeräts
	Method   string    `json:"method,omitempty"`
	Message  string    `json:"message"`
}

type Log struct {
	mu    sync.Mutex
	items []Event
	max   int
	file  *os.File
	subs  map[chan Event]struct{}
}

func Open(dir string, max int) (*Log, error) {
	l := &Log{max: max, subs: map[chan Event]struct{}{}}
	path := filepath.Join(dir, "events.jsonl")
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			var e Event
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				l.items = append(l.items, e)
			}
		}
		f.Close()
		if len(l.items) > max {
			l.items = l.items[len(l.items)-max:]
		}
		// Datei kompakt neu schreiben, damit sie nicht endlos wächst.
		if tmp, err := os.Create(path + ".tmp"); err == nil {
			w := bufio.NewWriter(tmp)
			enc := json.NewEncoder(w)
			for i := range l.items {
				enc.Encode(&l.items[i])
			}
			w.Flush()
			tmp.Close()
			os.Rename(path+".tmp", path)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	l.file = f
	return l, nil
}

func (l *Log) Add(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, e)
	if len(l.items) > l.max+l.max/10 {
		l.items = append([]Event(nil), l.items[len(l.items)-l.max:]...)
	}
	if l.file != nil {
		b, _ := json.Marshal(&e)
		l.file.Write(append(b, '\n'))
	}
	for ch := range l.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// Recent liefert die neuesten Einträge zuerst.
func (l *Log) Recent(n int) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n <= 0 || n > len(l.items) {
		n = len(l.items)
	}
	out := make([]Event, 0, n)
	for i := len(l.items) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, l.items[i])
	}
	return out
}

// Stats zählt Annahmen/Ablehnungen pro Stunde der letzten 24 Stunden.
type Stats struct {
	Accepted24h int    `json:"accepted24h"`
	Rejected24h int    `json:"rejected24h"`
	Hours       []Hour `json:"hours"`
}

type Hour struct {
	Start    time.Time `json:"start"`
	Accepted int       `json:"accepted"`
	Rejected int       `json:"rejected"`
}

func (l *Log) Stats(now time.Time) Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	start := now.Truncate(time.Hour).Add(-23 * time.Hour)
	s := Stats{Hours: make([]Hour, 24)}
	for i := range s.Hours {
		s.Hours[i].Start = start.Add(time.Duration(i) * time.Hour)
	}
	for i := len(l.items) - 1; i >= 0; i-- {
		e := l.items[i]
		if e.Time.Before(start) {
			break
		}
		idx := int(e.Time.Sub(start) / time.Hour)
		if idx < 0 || idx >= 24 {
			continue
		}
		switch e.Kind {
		case Accept:
			s.Accepted24h++
			s.Hours[idx].Accepted++
		case Reject:
			s.Rejected24h++
			s.Hours[idx].Rejected++
		}
	}
	return s
}

func (l *Log) Subscribe() chan Event {
	ch := make(chan Event, 64)
	l.mu.Lock()
	l.subs[ch] = struct{}{}
	l.mu.Unlock()
	return ch
}

func (l *Log) Unsubscribe(ch chan Event) {
	l.mu.Lock()
	delete(l.subs, ch)
	l.mu.Unlock()
}

func (l *Log) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
}
