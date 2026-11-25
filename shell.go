package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"runtime"

	"github.com/egoist/mygo"
)

// hotkeys are tried in order at startup; the first one macOS accepts
// becomes the global shortcut for showing the window. Cmd+Shift+Space is
// the default; the rest are fallbacks for when it is already taken
// (macOS reserves it for switching input sources in some setups).
var hotkeys = []string{
	"CmdOrCtrl+Shift+Space",
	"CmdOrCtrl+Option+Space",
	"CmdOrCtrl+Shift+F",
	"CmdOrCtrl+Option+F",
}

// quitting lets the window's OnClose pass the close through to a real quit.
var quitting bool

// setupShell turns the app into a menu-bar resident with a global hotkey:
//   - a template icon in the macOS menu bar (left click toggles the window,
//     right click opens a menu with Show and Quit);
//   - a global shortcut that shows and focuses the window from anywhere;
//   - closing the window hides it and its Dock icon instead of quitting, so
//     fsearch's status and the search state survive; the Dock icon returns
//     when the window shows again, and only Quit (menu or Cmd+Q) exits.
func setupShell(win *mygo.Window) {
	tray, err := mygo.NewTray(mygo.TrayOptions{
		Icon:           trayIcon(),
		IconIsTemplate: true,
		ToolTip:        "FSearch — click to show",
	})
	if err != nil {
		log.Printf("menu bar icon: %v", err)
	} else {
		tray.OnClick(func() { toggleWindow(win) })
		menu := mygo.NewMenu([]*mygo.MenuItem{
			{Label: "Show FSearch", Click: func(*mygo.MenuItem, *mygo.Window) { showWindow(win) }},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		})
		// On macOS a TrayOptions.Menu would swallow the primary click, so
		// install it only for the right-click popup, then remove it.
		if runtime.GOOS == "darwin" {
			tray.OnRightClick(func() {
				tray.SetMenu(menu)
				defer tray.SetMenu(nil)
				tray.PopUpMenu()
			})
		} else {
			tray.SetMenu(menu)
		}
	}

	win.OnClose(func(e *mygo.CloseEvent) {
		if quitting {
			return
		}
		e.PreventDefault()
		hideWindow(win)
	})

	for _, acc := range hotkeys {
		key := acc
		if err := mygo.GlobalShortcut.Register(key, func() { showWindow(win) }); err != nil {
			log.Printf("global shortcut %s unavailable: %v", key, err)
			continue
		}
		log.Printf("global shortcut: %s", key)
		break
	}
}

func showWindow(win *mygo.Window) {
	// accessory mode (a hidden Dock icon) can still show windows, but going
	// back to the regular policy first keeps the window active and in the
	// app switcher while it is on screen.
	if runtime.GOOS == "darwin" {
		mygo.App.Dock.Show()
	}
	win.Show()
	win.Focus()
}

// hideWindow keeps the daemon and search state alive but takes the window and
// its Dock icon off screen; the menu-bar icon and global shortcut bring it back.
func hideWindow(win *mygo.Window) {
	win.Hide()
	if runtime.GOOS == "darwin" {
		mygo.App.Dock.Hide()
	}
}

func toggleWindow(win *mygo.Window) {
	if win.IsVisible() {
		hideWindow(win)
	} else {
		showWindow(win)
	}
}

// trayIcon draws a magnifying glass, black on transparent so macOS tints
// it for the current menu bar appearance (template icon).
func trayIcon() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	ink := color.NRGBA{R: 79, G: 124, B: 255, A: 255}
	if runtime.GOOS == "darwin" {
		ink = color.NRGBA{A: 255}
	}
	fill := func(r image.Rectangle) {
		draw.Draw(img, r, image.NewUniform(ink), image.Point{}, draw.Src)
	}
	// Ring: four bars approximate a circle at 32px, plus a handle.
	fill(image.Rect(7, 5, 19, 7))   // top
	fill(image.Rect(7, 17, 19, 19)) // bottom
	fill(image.Rect(5, 7, 7, 17))   // left
	fill(image.Rect(17, 7, 19, 17)) // right
	fill(image.Rect(18, 17, 21, 19))
	fill(image.Rect(20, 19, 23, 21))
	fill(image.Rect(22, 21, 25, 24))
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}
