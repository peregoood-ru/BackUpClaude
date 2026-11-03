//go:build windows

package tray

import (
	"context"
	"errors"
	"time"

	"fyne.io/systray"
	"golang.org/x/sys/windows"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// Supported reports whether this OS can show the icon.
func Supported() bool { return true }

// Run shows the icon until Quit is chosen or ctx ends. Only one icon runs at a
// time: a second one stops with an error that says so.
func Run(ctx context.Context, opts Options) error {
	name, err := windows.UTF16PtrFromString(`Local\BackUpClaudeTray` + opts.Instance)
	if err != nil {
		return i18n.E("tray.failed").Because(err)
	}
	mutex, err := windows.CreateMutex(nil, false, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return i18n.E("tray.failed").Because(err)
	}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		_ = windows.CloseHandle(mutex)
		return i18n.E("tray.already_running").Hint("tray.already_running.hint")
	}
	defer windows.CloseHandle(mutex)

	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	onReady := func() {
		systray.SetIcon(Icon(LevelUnknown))
		systray.SetTitle(opts.Labels.Title)
		systray.SetTooltip(opts.Labels.Checking)
		status := systray.AddMenuItem(opts.Labels.Checking, "")
		status.Disable()
		systray.AddSeparator()
		open := systray.AddMenuItem(opts.Labels.Open, "")
		refresh := systray.AddMenuItem(opts.Labels.Refresh, "")
		systray.AddSeparator()
		quit := systray.AddMenuItem(opts.Labels.Quit, "")
		systray.SetOnTapped(func() { _ = opts.Open() })

		poke := make(chan struct{}, 1)
		go func() {
			for {
				select {
				case <-open.ClickedCh:
					_ = opts.Open()
				case <-refresh.ClickedCh:
					select {
					case poke <- struct{}{}:
					default:
					}
				case <-quit.ClickedCh:
					systray.Quit()
					return
				case <-runCtx.Done():
					systray.Quit()
					return
				}
			}
		}()
		go func() {
			update := func() {
				state := opts.Source(runCtx)
				if runCtx.Err() != nil {
					return
				}
				systray.SetIcon(Icon(state.Level))
				systray.SetTooltip(truncate(state.Text, 120))
				status.SetTitle(state.Text)
			}
			update()
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					update()
				case <-poke:
					update()
				case <-runCtx.Done():
					return
				}
			}
		}()
	}
	systray.Run(onReady, func() { cancel() })
	return nil
}

// truncate shortens a tooltip: Windows cuts it at 127 characters.
func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}
