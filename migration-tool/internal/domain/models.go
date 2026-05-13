package domain

import (
	"fmt"
	"time"
)

type InstanceConfig struct {
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	User          string `yaml:"user"`
	Password      string `yaml:"password"`
	SSLMode       string `yaml:"sslmode"`
	SSHUser       string `yaml:"ssh_user"`
	SSHPassword   string `yaml:"ssh_password"`
	SSHPrivateKey string `yaml:"ssh_private_key"`
	SSHPort       int    `yaml:"ssh_port"`
}

type DatabaseSource struct {
	Database string   `yaml:"database"`
	Tables   []string `yaml:"tables"`
}

type MigrationConfig struct {
	Source      InstanceConfig   `yaml:"source"`
	Destination InstanceConfig   `yaml:"destination"`
	Databases   []DatabaseSource `yaml:"-"`
	OutputFile  string           `yaml:"output_file"`
}

type TableData struct {
	TableName string           `yaml:"table_name"`
	Columns   []string         `yaml:"columns"`
	Rows      []map[string]any `yaml:"rows"`
	RowCount  int              `yaml:"row_count"`
}

type DatabaseBackup struct {
	Database string       `yaml:"database"`
	Tables   []*TableData `yaml:"tables"`
}

type BackupData struct {
	Version     string            `yaml:"version"`
	ExportedAt  time.Time         `yaml:"exported_at"`
	SourceHost  string            `yaml:"source_host"`
	InternalKey string            `yaml:"internal_key,omitempty"`
	Databases   []*DatabaseBackup `yaml:"databases"`

	Tables []*TableData `yaml:"tables,omitempty"`
}

func (ic *InstanceConfig) ConnectionString(database string) string {
	sslmode := ic.SSLMode
	if sslmode == "" {
		sslmode = "disable"
	}

	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s connect_timeout=10",
		ic.Host, ic.Port, ic.User, ic.Password, database, sslmode)
}

func (ic *InstanceConfig) SSHConfig() SSHClientConfig {
	return SSHClientConfig{
		Host:       ic.Host,
		Port:       ic.SSHPort,
		User:       ic.SSHUser,
		Password:   ic.SSHPassword,
		PrivateKey: ic.SSHPrivateKey,
	}
}

type SSHClientConfig struct {
	Host       string
	Port       int
	User       string
	Password   string
	PrivateKey string
}
