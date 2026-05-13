// Package ui provides consistent CLI output formatting for UTMStack Migration Tool.
package ui

import (
	"os"

	"golang.org/x/term"
)

// ANSI color codes
const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	blue   = "\033[34m"
	cyan   = "\033[36m"
	white  = "\033[37m"
)

// colorsEnabled indicates whether ANSI colors should be used
var colorsEnabled = true

func init() {
	// Disable colors if not a terminal or NO_COLOR is set
	if !term.IsTerminal(int(os.Stdout.Fd())) || os.Getenv("NO_COLOR") != "" {
		colorsEnabled = false
	}
}

// DisableColors turns off ANSI color output
func DisableColors() {
	colorsEnabled = false
}

// EnableColors turns on ANSI color output
func EnableColors() {
	colorsEnabled = true
}

// color wraps text with ANSI color codes if colors are enabled
func color(c, text string) string {
	if !colorsEnabled {
		return text
	}
	return c + text + reset
}

// Bold returns bold text
func Bold(text string) string {
	return color(bold, text)
}

// Dim returns dimmed text
func Dim(text string) string {
	return color(dim, text)
}

// Red returns red text
func Red(text string) string {
	return color(red, text)
}

// Green returns green text
func Green(text string) string {
	return color(green, text)
}

// Yellow returns yellow text
func Yellow(text string) string {
	return color(yellow, text)
}

// Blue returns blue text
func Blue(text string) string {
	return color(blue, text)
}

// Cyan returns cyan text
func Cyan(text string) string {
	return color(cyan, text)
}

// White returns white text
func White(text string) string {
	return color(white, text)
}

// BoldGreen returns bold green text
func BoldGreen(text string) string {
	return color(bold+green, text)
}

// BoldRed returns bold red text
func BoldRed(text string) string {
	return color(bold+red, text)
}

// BoldYellow returns bold yellow text
func BoldYellow(text string) string {
	return color(bold+yellow, text)
}

// BoldCyan returns bold cyan text
func BoldCyan(text string) string {
	return color(bold+cyan, text)
}
