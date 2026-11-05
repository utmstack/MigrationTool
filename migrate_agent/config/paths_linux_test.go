//go:build linux

package config

import "testing"

func TestForwarderPath(t *testing.T) {
	got := ForwarderPath()
	want := "/opt/utmstack-forwarder/"
	if got != want {
		t.Errorf("ForwarderPath() = %q, want %q", got, want)
	}
}
