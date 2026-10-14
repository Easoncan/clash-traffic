// Genicon draws resources/icon.png, the application icon:
//
//	go run ./tools/genicon
package main

import (
	"log"
	"os"

	"clashtraffic/internal/icon"
)

func main() {
	if err := os.WriteFile("resources/icon.png", icon.App(1024), 0o644); err != nil {
		log.Fatal(err)
	}
}
