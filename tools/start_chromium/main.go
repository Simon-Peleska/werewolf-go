// start_chromium opens one Chromium window with N isolated tabs for manual
// multi-player testing. Each tab gets its own CDP browser context (via
// Browser.Incognito), so it has separate cookies/storage and behaves as a
// separate signed-in player — without the window clutter of N processes.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

func main() {
	url := flag.String("url", "http://localhost:8080", "Target URL")
	instances := flag.Int("instances", 5, "Number of isolated tabs to open")
	bin := flag.String("bin", "chromium", "Chromium binary name")
	workspace := flag.Int("workspace", 5, "Hyprland workspace to open the window on")
	flag.Parse()

	switchHyprlandWorkspace(*workspace)

	tmpDir, err := os.MkdirTemp("", "chromium-tabs-")
	if err != nil {
		log.Fatalf("creating temp profile dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	controlURL, err := launcher.New().
		Bin(*bin).
		UserDataDir(tmpDir).
		Headless(false).
		Set("no-first-run", "").
		Set("no-default-browser-check", "").
		Set("disable-sync", "").
		Launch()
	if err != nil {
		log.Fatalf("launching %s: %v", *bin, err)
	}

	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		log.Fatalf("connecting to browser: %v", err)
	}
	defer browser.MustClose()

	for i := 0; i < *instances; i++ {
		ctx, err := browser.Incognito()
		if err != nil {
			log.Fatalf("creating isolated tab %d: %v", i+1, err)
		}
		if _, err := ctx.Page(proto.TargetCreateTarget{URL: *url}); err != nil {
			log.Fatalf("opening tab %d: %v", i+1, err)
		}
	}

	closeBlankStartupTab(browser)

	fmt.Printf("Opened %d isolated tabs at %s — press Ctrl+C to close.\n", *instances, *url)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
}

// closeBlankStartupTab closes the default about:blank tab Chromium opens on
// launch, since it's not one of our isolated player tabs.
func closeBlankStartupTab(browser *rod.Browser) {
	pages, err := browser.Pages()
	if err != nil {
		return
	}
	for _, p := range pages {
		if info, err := p.Info(); err == nil && info.URL == "about:blank" {
			p.Close()
		}
	}
}

// switchHyprlandWorkspace mirrors the old bash script's best-effort workspace
// switch so all windows still land in the same place on Hyprland.
func switchHyprlandWorkspace(workspace int) {
	if _, err := exec.LookPath("hyprctl"); err != nil {
		fmt.Println("Warning: hyprctl not found — window will open on the current workspace (--workspace ignored)")
		return
	}
	cmd := exec.Command("hyprctl", "dispatch", fmt.Sprintf("hl.dsp.focus({ workspace = %d })", workspace))
	_ = cmd.Run()
}
