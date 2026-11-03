package main

import (
	"context"
	"errors"
	"flag"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqlbackup"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func mysqlNativeRestoreCommand(command string) bool {
	return command == mysqlbackup.RestoreCommand || command == mysqlbackup.ReconcileRestoreCommand
}
func mysqlNativeCommand(command string) bool {
	return mysqlNativeRestoreCommand(command) || command == mysqlbackup.BackupCommand || command == mysqlbackup.ReconcileBackupCommand
}
func validateMySQLNativeFlags(flags *flag.FlagSet, command, configuration, runtime, backup, restore, reason string, isolated bool) error {
	if !mysqlNativeCommand(command) && !databaseaccess.MySQLRestoredCommand(command) {
		supplied := isolated
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "confirm-isolated-mysql-restore" {
				supplied = true
			}
		})
		if supplied {
			return errors.New("MYSQL_RESTORE_CONFIRMATION_COMMAND_INVALID")
		}
		return nil
	}
	allowed := map[string]bool{"config": true}
	valid := configuration != ""
	switch command {
	case mysqlbackup.BackupCommand:
		allowed["backup-root"], allowed["reason"] = true, true
		valid = valid && backup != "" && modelpreparations.ValidOperatorReason(reason) && restore == "" && !isolated && runtime == ""
	case mysqlbackup.ReconcileBackupCommand:
		allowed["backup-root"] = true
		valid = valid && backup != "" && restore == "" && reason == "" && !isolated && runtime == ""
	case mysqlbackup.RestoreCommand:
		allowed["backup-root"], allowed["restore-context"], allowed["reason"], allowed["confirm-isolated-mysql-restore"] = true, true, true, true
		valid = valid && backup != "" && restore != "" && modelpreparations.ValidOperatorReason(reason) && isolated && runtime == ""
	case mysqlbackup.ReconcileRestoreCommand:
		allowed["restore-context"], allowed["confirm-isolated-mysql-restore"] = true, true
		valid = valid && backup == "" && restore != "" && reason == "" && isolated && runtime == ""
	default:
		allowed["runtime-config"], allowed["restore-context"] = true, true
		valid = valid && runtime != "" && restore != "" && backup == "" && reason == "" && !isolated
	}
	flags.Visit(func(f *flag.Flag) {
		if !allowed[f.Name] {
			valid = false
		}
	})
	if !valid {
		return errors.New("MYSQL_NATIVE_OPERATOR_ARGUMENT_INVALID")
	}
	return nil
}
func (d *selectedDatabase) mysqlFresh(ctx context.Context, c config.Config) (*mysqloperator.Fresh, error) {
	if d == nil || d.mysql == nil || d.myLease == nil || d.postgres != nil || d.pgLease != nil {
		return nil, errors.New("MYSQL_OPERATOR_CONTEXT_REQUIRED")
	}
	return mysqloperator.NewFresh(ctx, d.mysql, d.myLease, c, d.catalog, d.options)
}
func (d *selectedDatabase) nativeMySQLCommand(ctx context.Context, c config.Config, command, backup, restore, reason string, isolated bool) (string, error) {
	if d == nil || !mysqlNativeCommand(command) || c.DatabaseEngine() != config.MySQLDatabaseEngine || c.Database.MySQLBackup == nil || c.Database.MySQLBackup.Validate() != nil {
		return "", errors.New("MYSQL_BACKUP_CONFIGURATION_REQUIRED")
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(c.Database.MySQLBackup.OperationSeconds)*time.Second)
	defer cancel()
	accounts := d.identity(c)
	if mysqlNativeRestoreCommand(command) {
		fresh, e := d.mysqlFresh(bounded, c)
		if e != nil {
			return "", e
		}
		if command == mysqlbackup.RestoreCommand {
			_, e = mysqlbackup.Restore(bounded, fresh, c, accounts, backup, restore, reason, isolated)
		} else {
			_, e = mysqlbackup.ReconcileRestore(bounded, fresh, c, accounts, restore, isolated)
		}
		if e != nil {
			return "", e
		}
		return "MYSQL_ISOLATED_RESTORE_VERIFIED", nil
	}
	operator, e := d.mysqlOperator(bounded, c)
	if e != nil {
		return "", e
	}
	if command == mysqlbackup.BackupCommand {
		_, e = mysqlbackup.Backup(bounded, operator, c, accounts, backup, reason, d.catalog.Steps())
	} else {
		_, e = mysqlbackup.ReconcileBackup(bounded, operator, c, backup)
	}
	if e != nil {
		return "", e
	}
	return "MYSQL_LOGICAL_BACKUP_VERIFIED", nil
}
func (d *selectedDatabase) restoredMySQLAccess(ctx context.Context, c config.Config, input databaseaccess.MySQLRuntimeInput, path, command string) (string, error) {
	if c.Database.MySQLBackup == nil || c.Database.MySQLBackup.Validate() != nil {
		return "", errors.New("MYSQL_BACKUP_CONFIGURATION_REQUIRED")
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(c.Database.MySQLBackup.OperationSeconds)*time.Second)
	defer cancel()
	fresh, e := d.mysqlFresh(bounded, c)
	if e != nil {
		return "", e
	}
	operator, e := d.mysqlOperator(bounded, c)
	if e != nil {
		return "", e
	}
	return databaseaccess.RestoredMySQLAccess(bounded, operator, fresh, c, d.identity(c), input, path, command)
}
