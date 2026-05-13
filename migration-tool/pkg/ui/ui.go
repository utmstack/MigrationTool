package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// Box width for headers and frames
const boxWidth = 67

// Icons for different message types
const (
	iconSuccess = "✓"
	iconError   = "✗"
	iconWarning = "⚠"
	iconInfo    = "→"
	iconStep    = "●"
)

// Header prints a boxed header with centered title
func Header(title string) {
	top := "╔" + strings.Repeat("═", boxWidth-2) + "╗"
	bottom := "╚" + strings.Repeat("═", boxWidth-2) + "╝"

	// Center the title - use RuneCountInString for Unicode support
	titleLen := utf8.RuneCountInString(title)
	padding := boxWidth - 4 - titleLen
	leftPad := padding / 2
	rightPad := padding - leftPad

	middle := "║ " + strings.Repeat(" ", leftPad) + title + strings.Repeat(" ", rightPad) + " ║"

	fmt.Println()
	fmt.Println(BoldCyan(top))
	fmt.Println(BoldCyan(middle))
	fmt.Println(BoldCyan(bottom))
	fmt.Println()
}

// SubHeader prints a smaller section header
func SubHeader(title string) {
	line := strings.Repeat("─", boxWidth-2)
	fmt.Println()
	fmt.Println(Cyan("┌" + line + "┐"))

	titleLen := utf8.RuneCountInString(title)
	padding := boxWidth - 4 - titleLen
	leftPad := padding / 2
	rightPad := padding - leftPad
	middle := "│ " + strings.Repeat(" ", leftPad) + title + strings.Repeat(" ", rightPad) + " │"
	fmt.Println(Cyan(middle))

	fmt.Println(Cyan("└" + line + "┘"))
}

// Step prints a step indicator with current/total
func Step(current, total int, message string) {
	fmt.Printf("\n%s %s\n", Bold(fmt.Sprintf("[%d/%d]", current, total)), message)
}

// StepCompact prints a step indicator without extra spacing (for dense operations)
func StepCompact(current, total int, message string) {
	fmt.Printf("%s %s\n", Bold(fmt.Sprintf("[%d/%d]", current, total)), message)
}

// Success prints a success message with green checkmark
func Success(message string) {
	fmt.Printf("  %s %s\n", BoldGreen(iconSuccess), message)
}

// Error prints an error message with red X
func Error(message string) {
	fmt.Printf("  %s %s\n", BoldRed(iconError), message)
}

// Warning prints a warning message with yellow triangle
func Warning(message string) {
	fmt.Printf("  %s %s\n", BoldYellow(iconWarning), message)
}

// Info prints an info message with cyan arrow
func Info(message string) {
	fmt.Printf("  %s %s\n", BoldCyan(iconInfo), message)
}

// Detail prints an indented detail line with aligned values
func Detail(key, value string) {
	// Fixed width for key column (including colon)
	keyWithColon := key + ":"
	fmt.Printf("  %-16s%s\n", Dim(keyWithColon), value)
}

// Blank prints a blank line
func Blank() {
	fmt.Println()
}

// Message prints a plain message
func Message(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
}

// Confirm prompts the user for confirmation
func Confirm(message string) bool {
	fmt.Printf("%s %s ", BoldYellow(iconWarning), message)
	fmt.Print(Bold("[y/N]: "))

	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	return response == "y" || response == "yes"
}

// ConfirmDangerous prompts for a dangerous operation requiring "Yes" (capital Y)
func ConfirmDangerous(message string) bool {
	fmt.Println()
	fmt.Println(BoldRed("╔" + strings.Repeat("═", boxWidth-2) + "╗"))

	// Warning line
	warningText := "WARNING: DESTRUCTIVE OPERATION"
	textLen := utf8.RuneCountInString(warningText)
	padding := boxWidth - 4 - textLen
	leftPad := padding / 2
	rightPad := padding - leftPad
	fmt.Println(BoldRed("║ " + strings.Repeat(" ", leftPad) + warningText + strings.Repeat(" ", rightPad) + " ║"))

	fmt.Println(BoldRed("╚" + strings.Repeat("═", boxWidth-2) + "╝"))
	fmt.Println()

	fmt.Println(message)
	fmt.Println()
	fmt.Print(Bold("Type 'Yes' to confirm (or anything else to cancel): "))

	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(response)

	return response == "Yes"
}

// Summary prints a key-value summary block
func Summary(title string, items map[string]string) {
	fmt.Println()
	fmt.Println(Bold(title + ":"))
	for key, value := range items {
		Detail(key, value)
	}
}

// SummaryOrdered prints a key-value summary block with ordered keys
func SummaryOrdered(title string, keys []string, items map[string]string) {
	fmt.Println()
	fmt.Println(Bold(title + ":"))
	for _, key := range keys {
		if value, ok := items[key]; ok {
			Detail(key, value)
		}
	}
}

// NextSteps prints a "Next Steps" section
func NextSteps(steps []string) {
	fmt.Println()
	fmt.Println(Bold("Next Steps:"))
	for i, step := range steps {
		fmt.Printf("  %s %s\n", Dim(fmt.Sprintf("%d.", i+1)), step)
	}
	fmt.Println()
}

// FinalSuccess prints a final success box
func FinalSuccess(message string) {
	top := "╔" + strings.Repeat("═", boxWidth-2) + "╗"
	bottom := "╚" + strings.Repeat("═", boxWidth-2) + "╝"

	// Center the message with checkmark
	fullMsg := iconSuccess + " " + message
	msgLen := utf8.RuneCountInString(fullMsg)
	padding := boxWidth - 4 - msgLen
	leftPad := padding / 2
	rightPad := padding - leftPad

	middle := "║ " + strings.Repeat(" ", leftPad) + fullMsg + strings.Repeat(" ", rightPad) + " ║"

	fmt.Println()
	fmt.Println(BoldGreen(top))
	fmt.Println(BoldGreen(middle))
	fmt.Println(BoldGreen(bottom))
	fmt.Println()
}

// FinalError prints a final error box
func FinalError(message string) {
	top := "╔" + strings.Repeat("═", boxWidth-2) + "╗"
	bottom := "╚" + strings.Repeat("═", boxWidth-2) + "╝"

	// Center the message with X
	fullMsg := iconError + " " + message
	msgLen := utf8.RuneCountInString(fullMsg)
	padding := boxWidth - 4 - msgLen
	leftPad := padding / 2
	rightPad := padding - leftPad

	middle := "║ " + strings.Repeat(" ", leftPad) + fullMsg + strings.Repeat(" ", rightPad) + " ║"

	fmt.Println()
	fmt.Println(BoldRed(top))
	fmt.Println(BoldRed(middle))
	fmt.Println(BoldRed(bottom))
	fmt.Println()
}

// StartOperation prints a starting operation message
func StartOperation(message string) {
	fmt.Printf("  %s %s...\n", Dim(iconStep), message)
}
