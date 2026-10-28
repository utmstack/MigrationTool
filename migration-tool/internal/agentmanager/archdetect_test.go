package agentmanager

import "testing"

// Windows 11 23H2+ removed wmic; the detection command must not depend on it.
func TestGetArchDetectCommandWindows(t *testing.T) {
	cmd := GetArchDetectCommand(PlatformInfo{OS: "windows", IsWindows: true})
	if cmd == `wmic os get osarchitecture /value` {
		t.Fatalf("still uses wmic, which does not exist on Windows 11 23H2+: %q", cmd)
	}
	if cmd != `@echo %PROCESSOR_ARCHITECTURE%` {
		t.Fatalf("unexpected windows command: %q", cmd)
	}
}

func TestGetArchDetectCommandUnix(t *testing.T) {
	for _, os := range []string{"linux", "darwin"} {
		if cmd := GetArchDetectCommand(PlatformInfo{OS: os}); cmd != `uname -m` {
			t.Fatalf("%s: unexpected command %q", os, cmd)
		}
	}
}

func TestParseArchitectureFromOutput(t *testing.T) {
	cases := []struct {
		out  string
		want string
	}{
		{`AMD64`, "amd64"},                 // @echo %PROCESSOR_ARCHITECTURE% (Windows)
		{`ARM64`, "arm64"},                 // Windows on ARM
		{`64-BIT WINDOWS`, "amd64"},        // wmic legacy output
		{`OSArchitecture=64-bit`, "amd64"}, // wmic legacy output
		{`x86_64`, "amd64"},                // uname
		{`aarch64`, "arm64"},               // uname
		{``, "amd64"},                      // default
	}
	for _, c := range cases {
		if got := ParseArchitectureFromOutput(c.out); got != c.want {
			t.Errorf("ParseArchitectureFromOutput(%q) = %q, want %q", c.out, got, c.want)
		}
	}
}
