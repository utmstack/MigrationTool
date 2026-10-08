package converters

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/importer"
)

// userUUIDNamespace derives V12 user IDs from the V11 numeric surrogate id.
// V11 jhi_user.id is a bigint; V12 user.id is a uuid. Deriving from the numeric
// id (not the login) keeps the user and user_role converters independent of
// each other's execution order, because jhi_user_authority only carries the
// numeric user_id.
var userUUIDNamespace = uuid.MustParse("8f3c9a1e-7b2d-4e6a-9c1f-5d8e0a3b7c21")

func userUUIDForV11ID(v11ID string) string {
	return uuid.NewSHA1(userUUIDNamespace, []byte(v11ID)).String()
}

type userRow struct {
	ID           string
	TenantID     string
	Email        string
	Name         string
	PasswordHash string
	Status       string
	LangKey      string
	ImageURL     string
}

func fieldString(row map[string]interface{}, key string) string {
	v, ok := row[key]
	if !ok || v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func isActivated(row map[string]interface{}) bool {
	v, ok := row["activated"]
	if !ok || v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return strings.EqualFold(val, "true")
	default:
		return false
	}
}

func mapUserRow(src map[string]interface{}) userRow {
	v11ID := fieldString(src, "id")
	login := fieldString(src, "login")

	email := fieldString(src, "email")
	if email == "" {
		email = login
	}

	name := strings.TrimSpace(fieldString(src, "first_name") + " " + fieldString(src, "last_name"))
	if name == "" {
		name = login
	}

	status := "inactive"
	if isActivated(src) {
		status = "active"
	}

	return userRow{
		ID:           userUUIDForV11ID(v11ID),
		TenantID:     DefaultTenantID,
		Email:        email,
		Name:         name,
		PasswordHash: fieldString(src, "password_hash"),
		Status:       status,
		LangKey:      fieldString(src, "lang_key"),
		ImageURL:     fieldString(src, "image_url"),
	}
}

const insertUserSQL = `INSERT INTO user (id, tenant_id, email, name, password_hash, status, lang_key, image_url)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (id) DO UPDATE SET
	email = EXCLUDED.email,
	tenant_id = EXCLUDED.tenant_id,
	name = EXCLUDED.name,
	password_hash = EXCLUDED.password_hash,
	status = EXCLUDED.status,
	lang_key = EXCLUDED.lang_key,
	image_url = EXCLUDED.image_url`

type userConverter struct{}

func (userConverter) SrcTable() string { return "jhi_user" }

func (c userConverter) Import(ctx context.Context, rows []map[string]interface{}, destDB *sql.DB) (int, error) {
	count := 0
	for _, src := range rows {
		row := mapUserRow(src)
		if row.ID == "" {
			continue
		}
		if _, err := destDB.ExecContext(ctx, insertUserSQL,
			row.ID, row.TenantID, row.Email, row.Name, row.PasswordHash, row.Status, row.LangKey, row.ImageURL); err != nil {
			return 0, fmt.Errorf("jhi_user → user (v11_id=%s): %w", fieldString(src, "id"), err)
		}
		count++
	}
	return count, nil
}

func mapRoleName(authority string) string {
	switch authority {
	case "ROLE_ADMIN":
		return RoleAdminID
	case "ROLE_USER":
		return RoleAnalystID
	default:
		return RoleViewerID
	}
}

const insertUserRoleSQL = `INSERT INTO user_role (user_id, role_id, tenant_id)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, role_id) DO NOTHING`

type userAuthorityConverter struct{}

func (userAuthorityConverter) SrcTable() string { return "jhi_user_authority" }

func (c userAuthorityConverter) Import(ctx context.Context, rows []map[string]interface{}, destDB *sql.DB) (int, error) {
	count := 0
	for _, src := range rows {
		v11UserID := fieldString(src, "user_id")
		authority := fieldString(src, "authority_name")
		if v11UserID == "" || authority == "" {
			continue
		}
		userID := userUUIDForV11ID(v11UserID)
		roleID := mapRoleName(authority)
		log.Printf("converter: user v11_id=%s: %s → %s", v11UserID, authority, roleID)
		if _, err := destDB.ExecContext(ctx, insertUserRoleSQL, userID, roleID, DefaultTenantID); err != nil {
			return 0, fmt.Errorf("jhi_user_authority → user_role (user_id=%s): %w", v11UserID, err)
		}
		count++
	}
	return count, nil
}

type authorityConverter struct{}

func (authorityConverter) SrcTable() string { return "jhi_authority" }

func (c authorityConverter) Import(_ context.Context, rows []map[string]interface{}, _ *sql.DB) (int, error) {
	names := map[string]bool{}
	for _, src := range rows {
		if name := fieldString(src, "name"); name != "" {
			names[name] = true
		}
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	log.Printf("converter: V11 roles are already seeded in V12; distinct source authorities: %v", sorted)
	return 0, nil
}

func init() {
	importer.Register(userConverter{})
	importer.Register(userAuthorityConverter{})
	importer.Register(authorityConverter{})
}
