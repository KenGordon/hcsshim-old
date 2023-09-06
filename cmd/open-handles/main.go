package main

import (
	"os"
	"os/signal"

	log "github.com/sirupsen/logrus"
	"golang.org/x/sys/windows"
)

func main() {
	paths := os.Args[1:]
	if len(paths) == 0 {
		log.Fatal("no paths specified")
	}

	notifyCh := make(chan os.Signal, 1)
	signal.Notify(notifyCh, windows.SIGINT, windows.SIGTERM, windows.SIGKILL, os.Interrupt)

	var handles []windows.Handle
	for _, path := range paths {
		u16Ptr, err := windows.UTF16PtrFromString(path)
		if err != nil {
			log.Fatal(err)
		}

		h, err := windows.CreateFile(
			u16Ptr,
			0,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil,
			windows.OPEN_EXISTING,
			windows.FILE_FLAG_BACKUP_SEMANTICS,
			0,
		)
		if err != nil {
			log.Fatal(err)
		}
		handles = append(handles, h)
	}

	sig := <-notifyCh

	log.Debugf("received signal: %d\n", sig)
	for _, h := range handles {
		if err := windows.CloseHandle(h); err != nil {
			log.Debugf("failed to close handle: %s", err)
		}
	}
}
