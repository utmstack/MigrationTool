package ui

import (
	"bufio"
	"strings"
	"testing"
)

func TestConfirmReadsTypedAnswer(t *testing.T) {
	old := stdinReader
	t.Cleanup(func() { stdinReader = old })

	cases := []struct {
		in   string
		want bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{"si\n", true},
		{"sí\n", true},
		{" n\n", false},
		{"N\n", false},
		{"\n", false},
	}
	for _, tc := range cases {
		stdinReader = bufio.NewReader(strings.NewReader(tc.in))
		if got := Confirm("Delete data file? (keeping it allows for retry if needed)"); got != tc.want {
			t.Errorf("Confirm with %q = %v, want %v", tc.in, got, tc.want)
		}
	}
}
