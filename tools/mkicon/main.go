// mkicon writes assets/slicetrigger.ico and docs/icon.png.
package main

import (
	"image/png"
	"log"
	"os"

	"slicetrigger/internal/sticon"
)

func main() {
	ico, err := sticon.ICO([]int{16, 20, 24, 32, 40, 48, 64, 128, 256})
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("assets/slicetrigger.ico", ico, 0o644); err != nil {
		log.Fatal(err)
	}
	f, err := os.Create("docs/icon.png")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, sticon.Draw(256, nil)); err != nil {
		log.Fatal(err)
	}
}
