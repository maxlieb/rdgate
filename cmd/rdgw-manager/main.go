//go:build wails

// Command rdgw-manager is the desktop app.
//
// The build tag keeps the rest of the module compiling and testable without
// the Wails toolkit installed. Once you have run
//
//	go install github.com/wailsapp/wails/v2/cmd/wails@latest
//	go mod tidy
//
// build it with:
//
//	wails build -tags wails
//
// or, for a dev loop with hot reload:
//
//	wails dev -tags wails
package main

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/maxlieb/rdgw-manager/frontend"
	"github.com/maxlieb/rdgw-manager/internal/ui"
)

func main() {
	if relaunchElevated() {
		return // the elevated copy takes over
	}
	root := os.Getenv("ProgramData")
	if root == "" {
		root = "."
	}
	app := ui.New(filepath.Join(root, "RDGateway", "config.json"))

	err := wails.Run(&options.App{
		OnStartup: func(ctx context.Context) {
			// internal/ui has no Wails import, so the bridge is injected
			// here. This is what lets the setup screen draw a live
			// checklist instead of a spinner.
			app.Emit = func(event string, data any) {
				runtime.EventsEmit(ctx, event, data)
			}
		},
		Title: "RDGate",
		// Sized for the signal path at its tallest, with two problems open.
		Width:     760,
		Height:    820,
		MinWidth:  520,
		MinHeight: 560,
		AssetServer: &assetserver.Options{
			Assets: frontend.Assets,
		},
		// The frontend reads prefers-color-scheme, so the window chrome
		// should follow the system too rather than pinning one theme.
		Windows: &windows.Options{
			WebviewIsTransparent:              false,
			WindowIsTranslucent:               false,
			DisableWindowIcon:                 false,
			DisableFramelessWindowDecorations: false,
		},
		Bind: []any{app},
	})
	if err != nil {
		log.Fatal(err)
	}
}
