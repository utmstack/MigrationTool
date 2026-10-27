package os2ch

import (
	"testing"
)

func TestClassifyIndex(t *testing.T) {
	tests := []struct {
		name   string
		index  string
		want   Dataset
		wantOK bool
	}{
		{"log linux daily", "v11-log-linux-2026-10-11", DatasetLogs, true},
		{"log other datatype", "v11-log-o365-2026-10-11", DatasetLogs, true},
		{"alert daily", "v11-alert-2026-10-11", DatasetAlerts, true},
		{"statistics out of scope", "v11-statistics-2026.10", DatasetUnknown, false},
		{"backend logs excluded", "v11-backend-logs", DatasetUnknown, false},
		{"compliance eval excluded", "v11-log-compliance-evaluation", DatasetUnknown, false},
		{"api access excluded", "v11-api-access-logs-2026.10", DatasetUnknown, false},
		{"system index excluded", ".opendistro_security", DatasetUnknown, false},
		{"empty", "", DatasetUnknown, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ClassifyIndex(tt.index)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("ClassifyIndex(%q) = (%v, %v), want (%v, %v)", tt.index, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
