// SPDX-License-Identifier: Apache-2.0

package sqlite

import "errors"

var (
	// ErrLocked reports that another process holds the database lock file.
	ErrLocked = errors.New("sqlite: database is owned by another process")
	// ErrPragma reports a pragma whose value differs from the required one after open.
	ErrPragma = errors.New("sqlite: pragma verification failed")
	// ErrDowngrade reports a database whose schema is newer than this binary's migrations.
	ErrDowngrade = errors.New("sqlite: schema version is newer than this binary (downgrades are not supported)")
	// ErrInsufficientDisk reports too little free disk for the pre-migration backup.
	ErrInsufficientDisk = errors.New("sqlite: not enough free disk for the pre-migration backup")
	// ErrBackupExists reports that a backup target path already exists.
	ErrBackupExists = errors.New("sqlite: backup target already exists")
	// ErrInvalidOption reports an invalid argument given to Open.
	ErrInvalidOption = errors.New("sqlite: invalid option")
	// ErrPendingMigrations reports a database whose schema needs migrations a read-only open cannot apply.
	ErrPendingMigrations = errors.New("sqlite: migrations are pending; run `loupe db migrate`")
	// ErrUnknownClass reports a retention class the store does not know.
	ErrUnknownClass = errors.New("sqlite: unknown retention class")
)
