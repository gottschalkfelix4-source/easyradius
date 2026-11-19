// EasyRadius – ein einfacher RADIUS-Server mit Weboberfläche (Windows und Linux).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/gottschalkfelix4-source/easyradius/internal/certs"
	"github.com/gottschalkfelix4-source/easyradius/internal/events"
	"github.com/gottschalkfelix4-source/easyradius/internal/letsencrypt"
	"github.com/gottschalkfelix4-source/easyradius/internal/radiusd"
	"github.com/gottschalkfelix4-source/easyradius/internal/store"
	"github.com/gottschalkfelix4-source/easyradius/internal/web"
)

var version = "dev"

const serviceName = "EasyRadius"

type app struct {
	store  *store.Store
	log    *events.Log
	radius *radiusd.Server
	http   *http.Server
}

func defaultDataDir() string {
	if runtime.GOOS == "windows" {
		if pd := os.Getenv("ProgramData"); pd != "" {
			return filepath.Join(pd, "EasyRadius")
		}
		return "data"
	}
	// systemd setzt STATE_DIRECTORY (StateDirectory=easyradius).
	if sd := os.Getenv("STATE_DIRECTORY"); sd != "" {
		return sd
	}
	return "/var/lib/easyradius"
}

func start(dataDir string) (*app, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" {
		secureDir(dataDir)
	}
	if f, err := os.OpenFile(filepath.Join(dataDir, "easyradius.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		if runtime.GOOS == "windows" {
			log.SetOutput(f)
		} else {
			log.SetOutput(io.MultiWriter(f, os.Stderr)) // zusätzlich ins Journal
		}
	}
	st, err := store.Open(dataDir)
	if err != nil {
		return nil, err
	}
	lg, err := events.Open(dataDir, st.Settings().LogRetention)
	if err != nil {
		return nil, err
	}
	cm, err := certs.NewManager(dataDir)
	if err != nil {
		return nil, err
	}
	rs := radiusd.New(st, lg, cm)
	if err := rs.Start(); err != nil {
		// Weboberfläche trotzdem starten, damit man den Fehler sieht und Ports ändern kann.
		log.Printf("RADIUS: %v", err)
		lg.Add(events.Event{Kind: events.Warning, Message: err.Error()})
	}
	le := &letsencrypt.Service{Store: st, Certs: cm, Log: lg, DataDir: dataDir}
	go le.Loop()
	ws := &web.Server{Store: st, Log: lg, Certs: cm, Radius: rs, ACME: le, Version: version}
	hs := &http.Server{Addr: st.Settings().WebAddr, Handler: ws.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := hs.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Weboberfläche: %v", err)
			lg.Add(events.Event{Kind: events.Warning, Message: "Weboberfläche konnte nicht starten: " + err.Error()})
		}
	}()
	// Letzte Anmeldungen regelmäßig sichern.
	go func() {
		for range time.Tick(5 * time.Minute) {
			st.Flush()
		}
	}()
	log.Printf("EasyRadius %s gestartet, Daten in %s, Web auf %s", version, dataDir, hs.Addr)
	return &app{store: st, log: lg, radius: rs, http: hs}, nil
}

func (a *app) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a.http.Shutdown(ctx)
	a.radius.Stop()
	a.store.Flush()
	a.log.Close()
}

func main() {
	dataDir := flag.String("data", defaultDataDir(), "Datenverzeichnis")
	showVersion := flag.Bool("version", false, "Version anzeigen")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	if flag.Arg(0) == "open" {
		// Startmenü-Verknüpfung: Weboberfläche öffnen.
		addr := ":8088"
		if st, err := store.Open(*dataDir); err == nil {
			addr = st.Settings().WebAddr
		}
		port := addr[strings.LastIndex(addr, ":")+1:]
		if err := openBrowser("http://localhost:" + port + "/"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if isService() {
		os.MkdirAll(*dataDir, 0o700)
		if err := secureDir(*dataDir); err != nil {
			log.Printf("Rechte für %s setzen: %v", *dataDir, err)
		}
		if err := runService(*dataDir); err != nil {
			log.Fatal(err)
		}
		return
	}

	a, err := start(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Start fehlgeschlagen:", err)
		os.Exit(1)
	}
	if os.Getenv("INVOCATION_ID") == "" { // nicht unter systemd: Hinweis für die Konsole
		fmt.Printf("EasyRadius %s läuft. Weboberfläche: http://localhost%s  (Strg+C beendet)\n", version, a.http.Addr)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	a.stop()
}
