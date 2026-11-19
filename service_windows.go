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
	a, err := start(h.dataDir)
	if err != nil {
		if el, e := eventlog.Open(serviceName); e == nil {
			el.Error(1, "Start fehlgeschlagen: "+err.Error())
			el.Close()
		}
		return true, 1
	}
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for c := range req {
		switch c.Cmd {
		case svc.Interrogate:
			status <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			status <- svc.Status{State: svc.StopPending}
			a.stop()
			return false, 0
		}
	}
	return false, 0
}

func runService(dataDir string) error {
	return svc.Run(serviceName, &handler{dataDir: dataDir})
}
