package converters

import (
	"testing"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/importer"
)

const bcryptHash = "$2a$10$mE.qmcV0mFU5NcKh73TZx.z4ueI/.bDWbj0T1BYyqP481kGGarKLG"

func TestMapUserRow(t *testing.T) {
	tests := []struct {
		name string
		src  map[string]interface{}
		want userRow
	}{
		{
			name: "full row uses email and combined name",
			src: map[string]interface{}{
				"id":            "42",
				"login":         "jane",
				"first_name":    "Jane",
				"last_name":     "Doe",
				"email":         "jane@corp.com",
				"password_hash": bcryptHash,
				"activated":     true,
				"lang_key":      "en",
				"image_url":     "https://corp.com/jane.png",
			},
			want: userRow{
				ID:           "42",
				TenantID:     DefaultTenantID,
				Email:        "jane@corp.com",
				Name:         "Jane Doe",
				PasswordHash: bcryptHash,
				Status:       "active",
				LangKey:      "en",
				ImageURL:     "https://corp.com/jane.png",
			},
		},
		{
			name: "empty email falls back to login",
			src: map[string]interface{}{
				"id":            "7",
				"login":         "sam",
				"first_name":    "Sam",
				"last_name":     "Lee",
				"email":         "",
				"password_hash": bcryptHash,
				"activated":     true,
				"lang_key":      "es",
			},
			want: userRow{
				ID:           "7",
				TenantID:     DefaultTenantID,
				Email:        "sam",
				Name:         "Sam Lee",
				PasswordHash: bcryptHash,
				Status:       "active",
				LangKey:      "es",
			},
		},
		{
			name: "activated bool true maps to active",
			src: map[string]interface{}{
				"id":        "1",
				"login":     "admin",
				"activated": true,
			},
			want: userRow{
				ID:       "1",
				TenantID: DefaultTenantID,
				Email:    "admin",
				Name:     "admin",
				Status:   "active",
			},
		},
		{
			name: "activated string false maps to inactive",
			src: map[string]interface{}{
				"id":        "2",
				"login":     "bob",
				"activated": "false",
			},
			want: userRow{
				ID:       "2",
				TenantID: DefaultTenantID,
				Email:    "bob",
				Name:     "bob",
				Status:   "inactive",
			},
		},
		{
			name: "empty names fall back to login",
			src: map[string]interface{}{
				"id":         "9",
				"login":      "nina",
				"first_name": "  ",
				"last_name":  "",
				"email":      "nina@corp.com",
				"activated":  "true",
			},
			want: userRow{
				ID:       "9",
				TenantID: DefaultTenantID,
				Email:    "nina@corp.com",
				Name:     "nina",
				Status:   "active",
			},
		},
		{
			name: "password hash is never transformed",
			src: map[string]interface{}{
				"id":            "10",
				"login":         "pwd",
				"first_name":    "P",
				"last_name":     "W",
				"email":         "pwd@corp.com",
				"password_hash": bcryptHash,
				"activated":     false,
			},
			want: userRow{
				ID:           "10",
				TenantID:     DefaultTenantID,
				Email:        "pwd@corp.com",
				Name:         "P W",
				PasswordHash: bcryptHash,
				Status:       "inactive",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapUserRow(tt.src)
			if got != tt.want {
				t.Errorf("mapUserRow() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestMapRoleName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"admin role", "ROLE_ADMIN", RoleAdminID},
		{"user role maps to analyst", "ROLE_USER", RoleAnalystID},
		{"viewer role", "ROLE_VIEWER", RoleViewerID},
		{"unknown role falls back to viewer", "ROLE_X", RoleViewerID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mapRoleName(tt.in); got != tt.want {
				t.Errorf("mapRoleName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestUserConvertersRegistered(t *testing.T) {
	for _, table := range []string{"jhi_user", "jhi_user_authority", "jhi_authority"} {
		if _, ok := importer.Get(table); !ok {
			t.Errorf("converter for %s not registered", table)
		}
	}
}
