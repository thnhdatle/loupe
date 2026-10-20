//go:build integration

// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_BuildsDatabase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, mf := filepath.Join(dir, "loupe.db"), filepath.Join(dir, "manifest.json")
	args := append([]string{}, "--spans", "5000", "--seed", "3", "--db", db, "--manifest", mf, "--capture", "on")
	var log strings.Builder
	if err := run(context.Background(), args, &log); err != nil {
		t.Fatalf("run: %v\n%s", err, log.String())
	}
	if !strings.Contains(log.String(), "done spans=5000") {
		t.Errorf("no summary in log: %s", log.String())
	}
	if _, err := os.Stat(mf); err != nil {
		t.Errorf("manifest: %v", err)
	}
	conn, err := sql.Open("sqlite", "file:"+db+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	var spans, parts int
	ctx := context.Background()
	if err = conn.QueryRowContext(ctx, "SELECT count(*) FROM spans").Scan(&spans); err != nil || spans != 5000 {
		t.Fatalf("spans = %d, err %v", spans, err)
	}
	err = conn.QueryRowContext(ctx, "SELECT count(*) FROM span_content").Scan(&parts)
	if err != nil || parts == 0 {
		t.Fatalf("content rows = %d, err %v", parts, err)
	}
	if err := run(context.Background(), args, io.Discard); err == nil {
		t.Error("existing database was not refused")
	}
	if err := run(context.Background(), append(args, "--force"), io.Discard); err != nil {
		t.Errorf("--force: %v", err)
	}
}
