package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func isAdmin() bool {
	if runtime.GOOS != "windows" {
		return true
	}
	// 尝试打开物理驱动器 — 仅管理员可访问
	f, err := os.Open(`\\.\PHYSICALDRIVE0`)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func main() {
	if !isAdmin() {
		exe, _ := os.Executable()
		// 用 PowerShell Start-Process -Verb RunAs 触发 UAC 提权
		cmd := exec.Command("powershell", "-Command",
			fmt.Sprintf(`Start-Process -FilePath "%s" -Verb RunAs`, exe))
		hideCmdWindow(cmd)
		cmd.Run()
		os.Exit(0)
	}

	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "",
		Width:     1100,
		Height:    720,
		MinWidth:  960,
		MinHeight: 600,
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 30, G: 30, B: 46, A: 255},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
	})

	if err != nil {
		log.Fatal(err)
	}
}
