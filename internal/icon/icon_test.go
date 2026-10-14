package icon

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestIcons(t *testing.T) {
	for name, b := range map[string][]byte{"tray.png": Tray(64), "app.png": App(256)} {
		if _, err := png.Decode(bytes.NewReader(b)); err != nil {
			t.Fatal(name, err)
		}
		if dir := os.Getenv("ICON_OUT"); dir != "" {
			_ = os.WriteFile(filepath.Join(dir, name), b, 0o644)
		}
	}
}
