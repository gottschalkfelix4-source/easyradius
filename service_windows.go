package main

import (
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
)

func isService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

type handler struct{ dataDir string }

func (h *handler) Execute(args []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	// Quelle für die Windows-Ereignisanzeige anlegen (gibt es sie schon, ist das kein Fehler).
	eventlog.InstallAsEventCreate(serviceName, eventlog.Error|eventlog.Warning|eventlog.Info)
	el, _ := eventlog.Open(serviceName)
	if el != nil {
		defer el.Close()
	}
	a, err := start(h.dataDir)
	if err != nil {
		if el != nil {
			el.Error(1, "Start fehlgeschlagen: "+err.Error())
		}
		return true, 1
	}
	if el != nil {
		el.Info(1, "EasyRadius "+version+" gestartet")
	}
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for c := range req {
		switch c.Cmd {
		case svc.Interrogate:
			status <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			status <- svc.Status{State: svc.StopPending, WaitHint: 10000}
			a.stop()
			if el != nil {
				el.Info(2, "EasyRadius beendet")
			}
			return false, 0
		}
	}
	return false, 0
}

func runService(dataDir string) error {
	return svc.Run(serviceName, &handler{dataDir: dataDir})
}
