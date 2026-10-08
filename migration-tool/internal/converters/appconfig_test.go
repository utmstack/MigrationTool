package converters

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/importer"
)

func configRowEqual(a, b configRow) bool {
	if a.UpdatedAt == nil || b.UpdatedAt == nil {
		if (a.UpdatedAt == nil) != (b.UpdatedAt == nil) {
			return false
		}
	} else if !a.UpdatedAt.Equal(*b.UpdatedAt) {
		return false
	}
	a.UpdatedAt, b.UpdatedAt = nil, nil
	return a == b
}

func TestMapConfigRow(t *testing.T) {
	fullTime := time.Date(2024, 5, 6, 7, 8, 9, 123456000, time.UTC)
	tests := []struct {
		name string
		src  map[string]interface{}
		want configRow
	}{
		{
			name: "full row maps all columns",
			src: map[string]interface{}{
				"conf_param_short":       "smtp.host",
				"conf_param_large":       "SMTP Host",
				"conf_param_description": "Outbound mail server",
				"conf_param_value":       "aGVsbG8gd29ybGQ=",
				"conf_param_datatype":    "string",
				"modification_time":      "2024-05-06 07:08:09.123456",
				"modification_user":      "admin",
			},
			want: configRow{
				ID:          configUUIDForKey("smtp.host"),
				TenantID:    DefaultTenantID,
				Key:         "smtp.host",
				Label:       "SMTP Host",
				Description: "Outbound mail server",
				Value:       "aGVsbG8gd29ybGQ=",
				IsSecret:    false,
				UpdatedAt:   &fullTime,
				UpdatedBy:   "admin",
			},
		},
		{
			name: "masked password value is stored empty",
			src: map[string]interface{}{
				"conf_param_short":    "smtp.password",
				"conf_param_large":    "SMTP Password",
				"conf_param_value":    "*****",
				"conf_param_datatype": "password",
			},
			want: configRow{
				ID:       configUUIDForKey("smtp.password"),
				TenantID: DefaultTenantID,
				Key:      "smtp.password",
				Label:    "SMTP Password",
				Value:    "",
				IsSecret: true,
			},
		},
		{
			name: "non-password datatype leaves value untouched and is_secret false",
			src: map[string]interface{}{
				"conf_param_short":    "branding.logo",
				"conf_param_large":    "Logo",
				"conf_param_value":    "*****",
				"conf_param_datatype": "file",
			},
			want: configRow{
				ID:       configUUIDForKey("branding.logo"),
				TenantID: DefaultTenantID,
				Key:      "branding.logo",
				Label:    "Logo",
				Value:    "*****",
				IsSecret: false,
			},
		},
		{
			name: "datatype comparison is case-insensitive",
			src: map[string]interface{}{
				"conf_param_short":    "mail.password",
				"conf_param_large":    "Mail Password",
				"conf_param_value":    "c2VjcmV0",
				"conf_param_datatype": "Password",
			},
			want: configRow{
				ID:       configUUIDForKey("mail.password"),
				TenantID: DefaultTenantID,
				Key:      "mail.password",
				Label:    "Mail Password",
				Value:    "c2VjcmV0",
				IsSecret: true,
			},
		},
		{
			name: "time.Time modification_time maps to UpdatedAt",
			src: map[string]interface{}{
				"conf_param_short":    "language",
				"conf_param_large":    "Language",
				"conf_param_value":    "en",
				"conf_param_datatype": "select",
				"modification_time":   fullTime,
				"modification_user":   "svc-migrate",
			},
			want: configRow{
				ID:        configUUIDForKey("language"),
				TenantID:  DefaultTenantID,
				Key:       "language",
				Label:     "Language",
				Value:     "en",
				IsSecret:  false,
				UpdatedAt: &fullTime,
				UpdatedBy: "svc-migrate",
			},
		},
		{
			name: "empty and unparseable modification_time leave UpdatedAt NULL",
			src: map[string]interface{}{
				"conf_param_short":    "mail.host",
				"conf_param_large":    "Mail Host",
				"conf_param_value":    "mail.corp.com",
				"conf_param_datatype": "string",
				"modification_time":   "not-a-timestamp",
			},
			want: configRow{
				ID:       configUUIDForKey("mail.host"),
				TenantID: DefaultTenantID,
				Key:      "mail.host",
				Label:    "Mail Host",
				Value:    "mail.corp.com",
				IsSecret: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := mapConfigRow(tt.src)
			if !ok {
				t.Fatal("mapConfigRow() = skipped, want mapped row")
			}
			if !configRowEqual(got, tt.want) {
				t.Errorf("mapConfigRow() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestMapConfigRowSkipsEmptyKey(t *testing.T) {
	src := map[string]interface{}{
		"conf_param_large": "Orphan",
		"conf_param_value": "x",
	}
	if _, ok := mapConfigRow(src); ok {
		t.Fatal("mapConfigRow() = mapped, want skipped for empty conf_param_short")
	}
}

func TestConfigUUIDForKeyIsDeterministicAndDistinct(t *testing.T) {
	a := configUUIDForKey("mail.host")
	b := configUUIDForKey("mail.host")
	if a != b {
		t.Fatalf("configUUIDForKey not deterministic: %q != %q", a, b)
	}
	if _, err := uuid.Parse(a); err != nil {
		t.Fatalf("configUUIDForKey not a valid uuid: %v", err)
	}
	if configUUIDForKey("mail.host") == configUUIDForKey("mail.port") {
		t.Fatal("distinct keys must map to distinct uuids")
	}
}

func TestAppConfigConverterRegistered(t *testing.T) {
	if _, ok := importer.Get("utm_configuration_parameter"); !ok {
		t.Error("converter for utm_configuration_parameter not registered")
	}
}
