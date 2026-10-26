package migration

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"

	"github.com/utmstack/MigrationTool/config"
)

// Port mirrors the V11 log-collector-config.json port schema.
type Port struct {
	Enabled    bool   `json:"enabled"`
	Value      string `json:"value"`
	TLSEnabled bool   `json:"tls_enabled"`
}

// V11Integration mirrors one entry of the V11 integrations map.
type V11Integration struct {
	TCPPort Port `json:"tcp_port"`
	UDPPort Port `json:"udp_port"`
}

// V11CollectorConfig mirrors the V11 log-collector-config.json schema.
type V11CollectorConfig struct {
	Integrations map[string]V11Integration `json:"integrations"`
}

// ListenerPort is one enabled V11 listener to replicate on the forwarder.
// Protocol is the V12 conf proto value: tcp, tls or udp.
type ListenerPort struct {
	DataType string
	Proto    string
	Port     string
	TLS      bool
}

// EnabledListeners extracts the enabled protocol listeners from a parsed
// V11 collector config. TCP wins over UDP when both are enabled on the same
// data type: the V12 config row is per data type and carries a single proto.
func EnabledListeners(cnf *V11CollectorConfig) []ListenerPort {
	if cnf == nil {
		return nil
	}
	out := []ListenerPort{}
	for name, integ := range cnf.Integrations {
		proto := ""
		port := ""
		tls := false
		if integ.TCPPort.Enabled {
			proto = "tcp"
			port = integ.TCPPort.Value
			tls = integ.TCPPort.TLSEnabled
		} else if integ.UDPPort.Enabled {
			proto = "udp"
			port = integ.UDPPort.Value
		}
		if proto == "" || port == "" {
			continue
		}
		if tls {
			proto = "tls"
		}
		out = append(out, ListenerPort{DataType: name, Proto: proto, Port: port, TLS: tls})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DataType < out[j].DataType })
	return out
}

// ReadEnabledListeners loads the extracted V11 collector config from the
// migration path and returns its enabled listeners.
func ReadEnabledListeners() ([]ListenerPort, error) {
	path := config.MigrationPath() + "log-collector-config.json"
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cnf V11CollectorConfig
	if err := json.Unmarshal(data, &cnf); err != nil {
		return nil, fmt.Errorf("parse v11 collector config: %w", err)
	}
	return EnabledListeners(&cnf), nil
}

// ForwarderNeeded applies the T4 gate: the forwarder only runs on Linux and
// is only installed when at least one V11 protocol listener was enabled.
func ForwarderNeeded(listeners []ListenerPort) bool {
	return runtime.GOOS == "linux" && len(listeners) > 0
}

// listenerConfKeys builds the V12 SetCollectorConfig key/value pairs the
// forwarder applies (same contract as the panel UI).
func listenerConfKeys(l ListenerPort) map[string]string {
	tls := "false"
	if l.TLS {
		tls = "true"
	}
	return map[string]string{
		"enabled": "true",
		"proto":   l.Proto,
		"port":    l.Port,
		"tls":     tls,
	}
}

// writeSkippedReport records listeners that could not be migrated because the
// forwarder only runs on Linux. The tool surfaces this in its final summary.
func writeSkippedReport(listeners []ListenerPort) error {
	if len(listeners) == 0 {
		return nil
	}
	lines := make([]string, 0, len(listeners))
	for _, l := range listeners {
		lines = append(lines, fmt.Sprintf("%s %s %s", l.DataType, l.Proto, l.Port))
	}
	sort.Strings(lines)
	path := config.MigrationPath() + "forwarder-skipped.txt"
	return os.WriteFile(path, []byte("listeners not migrated (forwarder is Linux-only):\n"+joinNewline(lines)+"\n"), 0644)
}

func joinNewline(parts []string) string {
	s := ""
	for i, p := range parts {
		if i > 0 {
			s += "\n"
		}
		s += p
	}
	return s
}

// ForwarderBinaryName is the forwarder binary name served by the panel.
func ForwarderBinaryName() string {
	name := "utmstack_forwarder"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// forwarderDownloadURL points at the forwarder binary on the V12 panel 443.
// The agent-manager serves it under collector/forwarder/, verified live.
func forwarderDownloadURL(server string) string {
	return fmt.Sprintf("https://%s/private/dependencies/collector/forwarder/%s", server, ForwarderBinaryName())
}
