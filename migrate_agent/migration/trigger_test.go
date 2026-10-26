package migration

import "testing"

func TestShouldMigrate(t *testing.T) {
	cases := []struct {
		name       string
		served     string
		local      string
		localOk    bool
		wantAction bool
	}{
		{"v11 local, v12 served", "12.0.1", "11.1.4", true, true},
		{"same major", "11.1.4", "11.1.4", true, false},
		{"served older", "11.0.0", "12.0.0", true, false},
		{"local missing", "12.0.0", "", false, false},
		{"empty served", "", "11.1.4", true, false},
		{"both v12", "12.0.1", "12.0.0", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldMigrate(tc.served, tc.local, tc.localOk); got != tc.wantAction {
				t.Fatalf("shouldMigrate(%q,%q,%v)=%v want %v", tc.served, tc.local, tc.localOk, got, tc.wantAction)
			}
		})
	}
}
