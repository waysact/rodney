package main

import (
	"fmt"
	"io"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/devices"
)

// attachPage prepares a page that this command has just attached to. It is
// the one place for per-attach setup, and every command goes through it.
//
// connectBrowser switches off go-rod's default device, because its viewport
// emulation resized the page on every attach. The user agent part of that
// device stays: rodney has always sent it, and it has no effect on layout.
// It also applies the injected scripts, so that they survive reloads between commands.
func attachPage(page *rod.Page, s *State, warn io.Writer) error {
	if err := page.SetUserAgent(devices.LaptopWithMDPIScreen.UserAgentEmulation()); err != nil {
		return fmt.Errorf("failed to set user agent: %w", err)
	}
	return applyInjects(page, s, warn)
}
