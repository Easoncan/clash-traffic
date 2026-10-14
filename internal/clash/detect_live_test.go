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
		t.Logf("candidate %s (%s)", ep, ep.Source)
	}
	ep, v, err := Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("found %s %s", ep, v)
	s, err := NewClient(ep).Connections(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d connections, up %d down %d", len(s.Connections), s.UploadTotal, s.DownloadTotal)
}
