// Command mkicon writes the icon files derived from internal/icon.
package main

import (
	"log"
	"os"
	"path/filepath"

	"spacehop/internal/icon"
)

func main() {
	out := "assets"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	files := map[string][]byte{
		"icon.ico":     icon.ICO(16, 20, 24, 32, 40, 48, 64, 256),
		"icon.png":     icon.PNG(512, false),
		"icon-128.png": icon.PNG(128, false),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(out, name), data, 0o644); err != nil {
			log.Fatal(err)
		}
	}
}
