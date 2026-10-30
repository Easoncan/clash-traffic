package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"clashtraffic/internal/clash"
	"clashtraffic/internal/collector"
)

// StorageUsage is the on-disk space used by local traffic data and logs.
// Settings, credentials and the app itself are kept when clearing it.
type StorageUsage struct {
	Bytes     int64 `json:"bytes"`
	Traffic   int64 `json:"traffic"`
	AccessLog int64 `json:"accessLog"`
}

var storedDataPaths = []string{"data", "sessions", "collector-state.json", "sync-state.json", "mesh-state.json"}

func (c *Core) StorageUsage() (StorageUsage, error) {
	c.dataMu.RLock()
	defer c.dataMu.RUnlock()
	var usage StorageUsage
	for _, name := range storedDataPaths {
		var size int64
		err := filepath.WalkDir(filepath.Join(c.dir, name), func(path string, d fs.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return nil // a running flush may replace a file during the scan
			}
			if err != nil {
				return err
			}
			if d.Type().IsRegular() {
				info, err := d.Info()
				if errors.Is(err, os.ErrNotExist) {
					return nil
				}
				if err != nil {
					return err
				}
				size += info.Size()
			}
			return nil
		})
		if err != nil {
			return usage, err
		}
		usage.Bytes += size
		if name == "data" {
			usage.Traffic = size
		} else if name == "sessions" {
			usage.AccessLog = size
		}
	}
	return usage, nil
}

// ClearStorage runs only after the UI's confirmation. Stop all background
// writers, wait for queries and manual syncs, and restart counting from a
// fresh baseline without disconnecting any applications from Clash.
func (c *Core) ClearStorage(ctx context.Context) error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	running := c.cancel != nil
	if running {
		c.cancel()
		c.wg.Wait()
		c.cancel = nil
		defer c.startLoops()
	}
	c.dataMu.Lock()
	live, err := c.clearStorageLocked(ctx)
	c.dataMu.Unlock()
	if live != nil {
		c.publish(*live)
		_ = DataEvent.Broadcast(DataChange{At: live.T, Reason: "clear"})
		_ = MeshEvent.Broadcast(c.mesh.Status())
	}
	return err
}

func (c *Core) clearStorageLocked(ctx context.Context) (*collector.Live, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	client := c.client
	c.mu.Unlock()
	var baseline *clash.Snapshot
	if client != nil {
		tctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		baseline, _ = client.Connections(tctx) // read only; no core config changes
		cancel()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	trash, err := os.MkdirTemp(c.dir, ".clear-")
	if err != nil {
		return nil, err
	}
	var moved []string
	rollback := func(why error) error {
		var errs []error
		errs = append(errs, why)
		for _, name := range moved {
			if err := os.RemoveAll(filepath.Join(c.dir, name)); err != nil {
				errs = append(errs, err)
				continue
			}
			if err := os.Rename(filepath.Join(trash, name), filepath.Join(c.dir, name)); err != nil {
				errs = append(errs, err)
			}
		}
		// A failed restoration must keep the only remaining copy.
		if len(errs) == 1 {
			errs = append(errs, os.RemoveAll(trash))
		}
		return errors.Join(errs...)
	}
	for _, name := range storedDataPaths {
		err := os.Rename(filepath.Join(c.dir, name), filepath.Join(trash, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, rollback(err)
		}
		moved = append(moved, name)
	}
	if err := os.MkdirAll(filepath.Join(c.dir, "sessions"), 0o755); err != nil {
		return nil, rollback(err)
	}
	if err := c.store.Reset(); err != nil {
		return nil, rollback(err)
	}
	c.sessions.Reset()
	c.mesh.Reset()
	now := time.Now()
	live := c.coll.Clear(now, baseline)
	c.mu.Lock()
	c.rates = nil
	c.mu.Unlock()
	err = c.coll.Save(c.statePath, c.store.Gen())
	err = errors.Join(err, os.RemoveAll(trash))
	return &live, err
}
