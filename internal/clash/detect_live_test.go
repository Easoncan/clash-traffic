package clash

import (
	"context"
	"os"
	"testing"
)

func TestDetectLive(t *testing.T) {
	if os.Getenv("CLASH_LIVE") == "" {
		t.Skip("set CLASH_LIVE=1 to try the running core")
	}
	for _, ep := range Candidates() {
		c := NewClient(ep)
		v, err := c.Version(t.Context())
		if err != nil {
			t.Logf("candidate %s (%s): %v", ep, ep.Source, err)
		} else {
			s, err := c.Connections(t.Context())
			if err != nil {
				t.Logf("candidate %s: version %s, /connections failed: %v", ep, v, err)
			} else {
				t.Logf("candidate %s: version %s, %d connections, up %d down %d", ep, v, len(s.Connections), s.UploadTotal, s.DownloadTotal)
			}
		}
		c.Close()
	}
	ep, v, err := Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("found %s %s", ep, v)
	c := NewClient(ep)
	defer c.Close()
	s, err := c.Connections(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d connections, up %d down %d", len(s.Connections), s.UploadTotal, s.DownloadTotal)
}
