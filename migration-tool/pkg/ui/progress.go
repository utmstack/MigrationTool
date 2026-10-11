package ui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// Progress represents a progress bar for tracking operations
type Progress struct {
	total       int
	current     int
	label       string
	startTime   time.Time
	lastUpdate  time.Time
	mu          sync.Mutex
	width       int
	showPercent bool
	showCount   bool
	showETA     bool
}

// isStdoutTTY reports whether the bar can animate in place. When stdout is a
// pipe or file the \r frames would concatenate into a garbled line, so we
// suppress the live bar.
func isStdoutTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// activeBar tracks the live progress bar so Logf can clear it before printing
// a log line and redraw it after, keeping the two from overlapping on a TTY.
var (
	activeBarMu sync.Mutex
	activeBar   *Progress
)

func setActiveBar(p *Progress) {
	activeBarMu.Lock()
	activeBar = p
	activeBarMu.Unlock()
}

// Logf prints an informational line that must not collide with a running
// progress bar. On a TTY it clears the bar first and redraws it afterwards;
// on a pipe the bar is not shown at all. All converter progress logs go
// through here so they stay on one stream with the bar.
func Logf(format string, args ...any) {
	if isStdoutTTY() {
		activeBarMu.Lock()
		bar := activeBar
		activeBarMu.Unlock()
		if bar != nil {
			bar.ClearLine()
		}
	}
	fmt.Printf("  "+format+"\n", args...)
	if isStdoutTTY() {
		activeBarMu.Lock()
		bar := activeBar
		activeBarMu.Unlock()
		if bar != nil {
			bar.Redraw()
		}
	}
}

// NewProgress creates a new progress bar
func NewProgress(total int, label string) *Progress {
	p := &Progress{
		total:       total,
		current:     0,
		label:       label,
		startTime:   time.Now(),
		lastUpdate:  time.Now(),
		width:       30,
		showPercent: true,
		showCount:   true,
		showETA:     true,
	}
	setActiveBar(p)
	return p
}

// SetWidth sets the progress bar width
func (p *Progress) SetWidth(width int) *Progress {
	p.width = width
	return p
}

// Increment increases the progress by 1
func (p *Progress) Increment() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current++
	p.render()
}

// IncrementWithLabel increases progress and updates the label
func (p *Progress) IncrementWithLabel(label string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current++
	p.label = label
	p.render()
}

// Set sets the current progress value
func (p *Progress) Set(current int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current = current
	p.render()
}

// SetLabel updates the current label
func (p *Progress) SetLabel(label string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.label = label
	p.render()
}

// render draws the progress bar. It is a no-op when stdout is not a TTY so
// piped or logged runs stay clean instead of accumulating \r frames on one
// line.
func (p *Progress) render() {
	if p.total == 0 {
		return
	}
	if !isStdoutTTY() {
		return
	}

	// Calculate percentage
	percent := float64(p.current) / float64(p.total)
	if percent > 1 {
		percent = 1
	}

	// Build the progress bar
	filled := int(percent * float64(p.width))
	if filled > p.width {
		filled = p.width
	}

	bar := strings.Repeat("█", filled) + strings.Repeat("░", p.width-filled)

	// Build the status line
	var parts []string

	// Percentage
	if p.showPercent {
		parts = append(parts, fmt.Sprintf("%3d%%", int(percent*100)))
	}

	// Count
	if p.showCount {
		parts = append(parts, fmt.Sprintf("(%d/%d)", p.current, p.total))
	}

	// ETA calculation
	if p.showETA && p.current > 0 {
		elapsed := time.Since(p.startTime)
		estimatedTotal := time.Duration(float64(elapsed) / percent)
		remaining := estimatedTotal - elapsed

		if remaining > 0 && remaining < 24*time.Hour {
			if remaining >= time.Minute {
				parts = append(parts, fmt.Sprintf("ETA: %dm%ds", int(remaining.Minutes()), int(remaining.Seconds())%60))
			} else {
				parts = append(parts, fmt.Sprintf("ETA: %ds", int(remaining.Seconds())))
			}
		}
	}

	// Label (truncate if too long)
	label := p.label
	maxLabelLen := 25
	if len(label) > maxLabelLen {
		label = label[:maxLabelLen-3] + "..."
	}

	// Print the line with carriage return to overwrite
	status := strings.Join(parts, " ")
	line := fmt.Sprintf("\r  %s %s %s", bar, status, Dim(label))

	// Pad with spaces to clear any previous longer line
	padding := 80 - len(line)
	if padding > 0 {
		line += strings.Repeat(" ", padding)
	}

	fmt.Print(line)
	p.lastUpdate = time.Now()
}

// Redraw re-renders the current bar state, used after a log line has
// cleared it.
func (p *Progress) Redraw() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.render()
}

// ClearLine clears the bar's line without re-rendering it.
func (p *Progress) ClearLine() {
	fmt.Print("\r" + strings.Repeat(" ", 100) + "\r")
}

// Finish completes the progress bar with a final newline
func (p *Progress) Finish() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !isStdoutTTY() {
		return
	}
	// Clear the line and move to next
	fmt.Print("\r" + strings.Repeat(" ", 100) + "\r")
}

// FinishWithMessage completes the progress bar with a success message
func (p *Progress) FinishWithMessage(message string) {
	p.mu.Lock()
	startTime := p.startTime
	p.mu.Unlock()

	p.Finish()
	elapsed := time.Since(startTime)
	Success(fmt.Sprintf("%s in %s", message, formatDuration(elapsed)))
}

// formatDuration formats a duration nicely
func formatDuration(d time.Duration) string {
	if d >= time.Minute {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// SimpleProgress prints a simple progress update (for when you don't need a bar)
func SimpleProgress(current, total int, message string) {
	fmt.Printf("\r  Processing %d/%d: %s", current, total, message)
	if len(message) < 30 {
		fmt.Print(strings.Repeat(" ", 30-len(message)))
	}
}

// ClearLine clears the current line
func ClearLine() {
	fmt.Print("\r" + strings.Repeat(" ", 100) + "\r")
}
