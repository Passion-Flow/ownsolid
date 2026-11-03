package mysqlbackup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const manifestName = "manifest.json"
const completeName = "complete.json"
const archiveName = "database.sql"
const snapshotDirectory = "files"

type StoreReadback struct {
	Kind      string       `json:"kind"`
	Directory string       `json:"directory"`
	Tree      TreeReadback `json:"tree"`
}
type BackupManifest struct {
	Format                       string               `json:"format"`
	BackupID                     string               `json:"backup_id"`
	CreatedAt                    time.Time            `json:"created_at"`
	Reason                       string               `json:"reason"`
	Source                       mysqloperator.Target `json:"source"`
	SourceBindingSHA256          string               `json:"source_binding_sha256"`
	TenantID                     string               `json:"tenant_id"`
	SchemaSteps                  int                  `json:"schema_steps"`
	NativeArtifactManifestSHA256 string               `json:"native_artifact_manifest_sha256"`
	DumpSHA256                   string               `json:"dump_sha256"`
	DumpExecution                NativeExecution      `json:"dump_execution"`
	SQLFile                      string               `json:"sql_file"`
	SQLSHA256                    string               `json:"sql_sha256"`
	SQLBytes                     int64                `json:"sql_bytes"`
	Data                         DatabaseReadback     `json:"data"`
	Stores                       []StoreReadback      `json:"stores"`
}
type BackupCompletion struct {
	Format         string `json:"format"`
	BackupID       string `json:"backup_id"`
	ManifestSHA256 string `json:"manifest_sha256"`
	AuditEventID   string `json:"audit_event_id"`
}

func canonicalUUID(value string, version int) bool {
	id, e := uuid.Parse(value)
	return e == nil && id != uuid.Nil && id.String() == value && (version == 0 || int(id.Version()) == version)
}
func (m BackupManifest) validate(policy config.MySQLBackupConfiguration) error {
	bad := errors.New("MYSQL_BACKUP_MANIFEST_INVALID")
	source := ConnectionTarget{m.Source.ServerUUID, m.Source.Database, m.Source.Principal, m.Source.Version}
	if m.Format != "projectone.mysql.logical-backup.v1" || !canonicalUUID(m.BackupID, 4) || m.CreatedAt.IsZero() || !modelpreparations.ValidOperatorReason(m.Reason) || m.Source.Format != "projectone.mysql.operator-target.v1" || !source.valid() || !canonicalUUID(m.Source.InstallationID, 0) || !canonicalUUID(m.TenantID, 0) || m.SchemaSteps < 1 || !digestPattern.MatchString(m.SourceBindingSHA256) || !digestPattern.MatchString(m.NativeArtifactManifestSHA256) || !digestPattern.MatchString(m.DumpSHA256) || m.SQLFile != archiveName || m.SQLBytes < 1 || m.SQLBytes > policy.MaximumSQLBytes || !digestPattern.MatchString(m.SQLSHA256) || len(m.Stores) != 4 {
		return bad
	}
	if !m.DumpExecution.Joined || m.DumpExecution.ExitCode != 0 || m.DumpExecution.PID < 1 || m.DumpExecution.StartedAt.IsZero() || m.DumpExecution.CompletedAt.Before(m.DumpExecution.StartedAt) || !filepath.IsAbs(m.DumpExecution.File) || filepath.Clean(m.DumpExecution.File) != m.DumpExecution.File || m.DumpExecution.File == string(filepath.Separator) || m.DumpExecution.GOOS != "darwin" && m.DumpExecution.GOOS != "linux" || m.DumpExecution.GOARCH != "amd64" && m.DumpExecution.GOARCH != "arm64" {
		return bad
	}
	args := m.DumpExecution.Argv
	if len(args) != 15 || !strings.HasPrefix(args[0], "--defaults-file=") || args[len(args)-1] != m.Source.Database {
		return bad
	}
	expected := []string{"--no-login-paths", "--single-transaction", "--quick", "--triggers", "--hex-blob", "--default-character-set=utf8mb4", "--no-tablespaces", "--set-gtid-purged=OFF", "--skip-add-locks", "--skip-disable-keys", "--skip-network-timeout"}
	for i, arg := range expected {
		if args[i+1] != arg {
			return bad
		}
	}
	if !strings.HasPrefix(args[12], "--max-allowed-packet=") || args[13] != "--skip-lock-tables" {
		return bad
	}
	packetText := strings.TrimPrefix(args[12], "--max-allowed-packet=")
	packet, e := strconv.Atoi(packetText)
	packetPolicy := mysqlstore.DefaultPolicy()
	packetPolicy.MaximumPacketBytes = packet
	if e != nil || strconv.Itoa(packet) != packetText || packetPolicy.Validate() != nil {
		return bad
	}
	optionPath := strings.TrimPrefix(args[0], "--defaults-file=")
	if !filepath.IsAbs(optionPath) || filepath.Clean(optionPath) != optionPath || filepath.Base(optionPath) != "client.cnf" || strings.ContainsAny(optionPath, "\x00\r\n") {
		return bad
	}

	if m.Data.Format != "projectone.mysql.database-readback.v1" || !digestPattern.MatchString(m.Data.SHA256) || len(m.Data.Tables) < 1 {
		return bad
	}
	previous := ""
	haveJournal, haveAudit := false, false
	for _, table := range m.Data.Tables {
		if !identifier.MatchString(table.Table) || table.Table <= previous || table.Rows < 0 || !digestPattern.MatchString(table.SHA256) || len(table.Columns) == 0 || len(table.PrimaryKey) == 0 {
			return bad
		}
		previous = table.Table
		if table.Table == "schema_migrations" && table.Rows != int64(m.SchemaSteps) {
			return bad
		}
		columns := map[string]bool{}
		for _, name := range table.Columns {
			if !identifier.MatchString(name) || columns[name] {
				return bad
			}
			columns[name] = true
		}
		keys := map[string]bool{}
		for _, name := range table.PrimaryKey {
			if !columns[name] || keys[name] {
				return bad
			}
			keys[name] = true
		}
		haveJournal = haveJournal || table.Table == "schema_migrations"
		haveAudit = haveAudit || table.Table == "audit_events"
	}
	raw, e := json.Marshal(m.Data.Tables)
	if e != nil || mysqloperator.SHA256(raw) != m.Data.SHA256 || !haveJournal || !haveAudit {
		return bad
	}
	var totalBytes int64
	totalFiles := 0
	for i, kind := range []string{"models", "datasets", "training", "preparations"} {
		proof := m.Stores[i]
		if proof.Kind != kind || proof.Directory != snapshotDirectory+"/"+kind || !digestPattern.MatchString(proof.Tree.SHA256) || proof.Tree.Bytes < 0 || proof.Tree.Bytes > policy.MaximumStoreBytes-totalBytes || proof.Tree.Files < 1 || proof.Tree.Files > policy.MaximumStoreFiles-totalFiles {
			return bad
		}
		totalBytes += proof.Tree.Bytes
		totalFiles += proof.Tree.Files
	}
	return nil
}

func writeDocument(root *os.Root, name string, value any, maximum int64) error {
	raw, e := json.Marshal(value)
	if e != nil || int64(len(raw))+1 > maximum {
		return errors.New("MYSQL_BACKUP_MANIFEST_LIMIT_REACHED")
	}
	return writeNew(root, name, append(raw, '\n'))
}

// loadManifest validates byte inventory and the original measured producer. It
// makes no publisher-signature or target qualification claim from that inventory.
func loadManifest(ctx context.Context, root string, policy config.MySQLBackupConfiguration, completed bool) (BackupManifest, string, error) {
	var m BackupManifest
	raw, e := readPrivateDocument(filepath.Join(root, manifestName), policy.MaximumManifestBytes, &m)
	if e != nil {
		return BackupManifest{}, "", e
	}
	if e = m.validate(policy); e != nil {
		return BackupManifest{}, "", e
	}
	sha := mysqloperator.SHA256(raw)
	if completed {
		var completion BackupCompletion
		if _, e = readPrivateDocument(filepath.Join(root, completeName), policy.MaximumManifestBytes, &completion); e != nil || completion.Format != "projectone.mysql.backup-completion.v1" || completion.BackupID != m.BackupID || completion.AuditEventID != m.BackupID || completion.ManifestSHA256 != sha {
			return BackupManifest{}, "", errors.New("MYSQL_BACKUP_COMPLETION_REQUIRED")
		}
	}
	if e = privateArchive(ctx, filepath.Join(root, m.SQLFile), policy.MaximumSQLBytes, m.SQLSHA256, func(*os.File) error { return nil }); e != nil {
		return BackupManifest{}, "", e
	}
	var totalBytes int64
	totalFiles := 0
	for _, proof := range m.Stores {
		actual, e := TreeDigest(ctx, filepath.Join(root, filepath.FromSlash(proof.Directory)), policy.MaximumStoreBytes-totalBytes, policy.MaximumStoreFiles-totalFiles, policy.MaximumManifestBytes)
		if e != nil || actual != proof.Tree {
			return BackupManifest{}, "", errors.New("MYSQL_BACKUP_STORE_READBACK_CHANGED")
		}
		totalBytes += actual.Bytes
		totalFiles += actual.Files
	}
	return m, sha, nil
}
