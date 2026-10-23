// Command genicon writes the KnockBlock app icon, drawn by internal/icon,
// for the Windows build: scripts/winres.sh embeds it in knockblock.exe and
// the MSI uses it for the Start menu and "Installed apps". A development
// tool; not shipped.
//
//	genicon -o knockblock.ico [-png preview.png]
package main

import (
	"flag"
	"fmt"
	"image/png"
	"os"

	"github.com/Cecchellone/KnockBlock/client/internal/icon"
)

func main() {
	out := flag.String("o", "knockblock.ico", "output .ico file")
	preview := flag.String("png", "", "also write a 256×256 PNG preview here")
	flag.Parse()
	if err := run(*out, *preview); err != nil {
		fmt.Fprintln(os.Stderr, "genicon:", err)
		os.Exit(1)
	}
}

func run(out, preview string) error {
	ico, err := icon.AppICO()
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, ico, 0o644); err != nil {
		return err
	}
	if preview == "" {
		return nil
	}
	f, err := os.Create(preview)
	if err != nil {
		return err
	}
	if err := png.Encode(f, icon.Render(256, icon.LightBusy)); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
