// SPDX-License-Identifier: Apache-2.0

package sqlite

import "errors"

var (
	ErrLocked            = errors.New("sqlite: database is owned by another process")
	ErrPragma            = errors.New("sqlite: pragma verification failed")
	ErrDowngrade         = errors.New("sqlite: schema version is newer than this binary (downgrades are not supported)")
	ErrInsufficientDisk  = errors.New("sqlite: not enough free disk for the pre-migration backup")
	ErrBackupExists      = errors.New("sqlite: backup target already exists")
	ErrInvalidOption     = errors.New("sqlite: invalid option")
	ErrPendingMigrations = errors.New("sqlite: migrations are pending; run `loupe db migrate`")
	ErrUnknownClass      = errors.New("sqlite: unknown retention class")
)
