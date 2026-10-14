package main

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"clashtraffic/internal/i18n"
)

// errLocked is returned by tryLock while another process holds the lock.
var errLocked = errors.New("locked")

// dataLock keeps the lock of the data directory for as long as the process
// lives: the system lets it go when the process exits.
var dataLock *os.File

// lockDataDir takes the data directory for this process. MyGo lets a new
// instance start as soon as the old one's event loop stops, while the old
// one still saves its counters on its way out: the new one waits for that,
// up to wait, before it reads them.
func lockDataDir(dir string, wait time.Duration) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err := tryLock(f)
		if err == nil {
			return f, nil
		}
		if errors.Is(err, errLocked) && time.Now().After(deadline) {
			f.Close()
			return nil, i18n.Errorf("still in use after %s", wait)
		}
		if !errors.Is(err, errLocked) {
			f.Close()
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}
