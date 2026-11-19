// EasyRadius – ein einfacher RADIUS-Server mit Weboberfläche (Windows und Linux).
package main

import (
	"context"
	"crypto/tls"
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
	web    *web.Server
	http   *http.Server
	https  *http.Server
	cancel context.CancelFunc
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
	if runtime.GOOS != "windows" || sameDir(dataDir, defaultDataDir()) {
		if err := secureDir(dataDir); err != nil {
			log.Printf("Rechte für %s setzen: %v", dataDir, err)
		}
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
	rs.DataDir = dataDir
	if err := rs.Start(); err != nil {
		// Weboberfläche trotzdem starten, damit man den Fehler sieht und Ports ändern kann.
		log.Printf("RADIUS: %v", err)
		lg.Add(events.Event{Kind: events.Warning, Message: err.Error()})
	}
	ctx, cancel := context.WithCancel(context.Background())
	le := &letsencrypt.Service{Store: st, Certs: cm, Log: lg, DataDir: dataDir}
	go le.Loop(ctx)
	ws := &web.Server{Store: st, Log: lg, Certs: cm, Radius: rs, ACME: le, Version: version}
	handler := ws.Handler()
	newServer := func(addr string) *http.Server {
		return &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout: 2 * time.Minute, WriteTimeout: 2 * time.Minute, IdleTimeout: 2 * time.Minute,
			MaxHeaderBytes: 64 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	}
	a := &app{store: st, log: lg, radius: rs, web: ws, cancel: cancel}
	cfg := st.Settings()
	a.http = newServer(cfg.WebAddr)
	go func() {
		if err := a.http.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Weboberfläche: %v", err)
			lg.Add(events.Event{Kind: events.Warning, Message: "Weboberfläche konnte nicht starten: " + err.Error()})
		}
	}()
	// HTTPS mit demselben Zertifikat wie RADIUS (wechselt automatisch mit, z. B. nach Let's Encrypt).
	if cfg.HTTPSAddr != "" {
		a.https = newServer(cfg.HTTPSAddr)
		a.https.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12,
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return cm.Certificate(), nil }}
		go func() {
			if err := a.https.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				log.Printf("Weboberfläche (HTTPS): %v", err)
				lg.Add(events.Event{Kind: events.Warning, Message: "Weboberfläche per HTTPS konnte nicht starten: " + err.Error()})
			}
		}()
	}
	// Letzte Anmeldungen regelmäßig sichern.
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				st.Flush()
			}
		}
	}()
	log.Printf("EasyRadius %s gestartet, Daten in %s, Web auf %s (HTTPS %s)", version, dataDir, cfg.WebAddr, cfg.HTTPSAddr)
	return a, nil
}

func (a *app) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.cancel()
	a.web.Close() // Live-Protokolle beenden, sonst wartet Shutdown auf sie
	web.Shutdown(ctx, a.http, a.https)
	a.radius.Stop()
	a.store.Flush()
	a.log.Close()
}

func sameDir(a, b string) bool {
	pa, err1 := filepath.Abs(a)
	pb, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && strings.EqualFold(filepath.Clean(pa), filepath.Clean(pb))
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
		// Nur lesen – der Befehl läuft ohne Administratorrechte.
		addr := ":8088"
		if cfg, err := store.ReadSettings(*dataDir); err == nil && cfg.WebAddr != "" {
			addr = cfg.WebAddr
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
		port := a.http.Addr[strings.LastIndex(a.http.Addr, ":")+1:]
		fmt.Printf("EasyRadius %s läuft. Weboberfläche: http://localhost:%s  (Strg+C beendet)\n", version, port)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	a.stop()
}
