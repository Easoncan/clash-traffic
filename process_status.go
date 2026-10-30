package main

import (
	"context"
	"time"

	"clashtraffic/internal/clash"
	"clashtraffic/internal/i18n"
	"clashtraffic/internal/procname"
)

// CheckProcessIdentification reads the currently connected core only. It
// observes existing connections. It does not save settings, change
// find-process-mode or create test traffic.
func (c *Core) CheckProcessIdentification(ctx context.Context) ClashStatus {
	c.processMu.Lock()
	defer c.processMu.Unlock()
	c.mu.Lock()
	client, connected := c.client, c.status.Connected
	c.mu.Unlock()
	if client != nil && connected {
		c.inspectProcessMode(ctx, client, false)
	}
	return c.Status()
}

// The poll loop maintains the saved choice. Confirm a successful PATCH by
// reading it back: an accepted request alone does not prove it took effect.
func (c *Core) checkProcessMode(ctx context.Context, client *clash.Client) {
	c.processMu.Lock()
	defer c.processMu.Unlock()
	c.inspectProcessMode(ctx, client, c.settings().ForceProcess)
}

func (c *Core) inspectProcessMode(ctx context.Context, client *clash.Client, maintain bool) {
	before := c.Status()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	mode, err := client.FindProcessMode(ctx)
	problem := ""
	if err != nil {
		mode = ""
		problem = i18n.T("Could not read process identification: %v", err)
	} else if mode != "always" && mode != "strict" && mode != "off" {
		problem = i18n.T("The core did not return a supported process identification mode")
	} else if maintain && mode != "always" {
		if err := client.SetFindProcessMode(ctx, "always"); err != nil {
			problem = i18n.T("Could not enable process identification: %v", err)
		} else if mode, err = client.FindProcessMode(ctx); err != nil {
			mode = ""
			problem = i18n.T("Could not read process identification: %v", err)
		} else if mode != "always" {
			problem = i18n.T("The core did not keep process identification enabled (mode: %s)", mode)
		}
	}
	connections, identified, supplemented := 0, 0, 0
	sampleError := ""
	if problem == "" {
		snap, err := client.Connections(ctx)
		if err != nil {
			sampleError = i18n.T("Could not observe current connection processes: %v", err)
		} else {
			for _, conn := range snap.Connections {
				if conn.Metadata.Type == "Inner" {
					continue
				}
				connections++
				if conn.Metadata.Process != "" || conn.Metadata.ProcessPath != "" {
					identified++
				}
			}
			if client.Endpoint().IsLocal() {
				// Keep actual core results distinct from this app's OS fallback.
				var resolver procname.Resolver
				resolver.Resolve(ctx, snap.Connections)
				for _, conn := range snap.Connections {
					if conn.Metadata.Type != "Inner" && (conn.Metadata.Process != "" || conn.Metadata.ProcessPath != "") {
						supplemented++
					}
				}
				supplemented -= identified
			}
		}
	}
	c.mu.Lock()
	// A late response from an old core must not certify a new connection.
	if c.client != client || !c.status.Connected || c.status.Since != before.Since {
		c.mu.Unlock()
		return
	}
	c.status.FindProcessMode = mode
	c.status.ProcessCheckedAt = time.Now().UnixMilli()
	c.status.ProcessCheckError = problem
	c.status.ProcessConnections, c.status.ProcessIdentified, c.status.ProcessSupplemented = connections, identified, supplemented
	c.status.ProcessSampleError = sampleError
	status := c.status
	c.mu.Unlock()
	_ = StatusEvent.Broadcast(status)
}
