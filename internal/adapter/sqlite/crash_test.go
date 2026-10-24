// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const (
	crashHelperEnv  = "LOUPE_SQLITE_CRASH_DB"
	crashBatches    = 5
	crashBatchSpans = 20
	crashLastSpans  = 3000
)

func crashBatch(n, spans int) *trace.Batch {
	b := &trace.Batch{}
	for i := range spans {
		sp := mkSpan(1000+n, i+1, 0, trace.KindTool)
		if i > 0 {
			sp.ParentSpanID = sid(i)
		}
		b.Spans = append(b.Spans, sp)
	}
	return b
}

func runCrashHelper() int {
	ctx := context.Background()
	s, err := sqlite.Open(ctx, os.Getenv(crashHelperEnv))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	for i := range crashBatches {
		if _, err := s.WriteBatch(ctx, crashBatch(i, crashBatchSpans)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 4
		}
		fmt.Printf("committed %d\n", i+1)
	}
	started := make(chan struct{})
	go func() {
		close(started)
		_, _ = s.WriteBatch(ctx, crashBatch(crashBatches, crashLastSpans))
	}()
	<-started
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	select {}
}

func TestStore_CrashAndReopen(t *testing.T) {
	t.Parallel()
	path := dbPath(t)
	for round := range 3 {
		cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^$")
		cmd.Env = append(os.Environ(), crashHelperEnv+"="+path)
		out, err := cmd.StdoutPipe()
		require.NoError(t, err)
		require.NoError(t, cmd.Start())
		committed := 0
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if n, ok := strings.CutPrefix(sc.Text(), "committed "); ok {
				committed, err = strconv.Atoi(n)
				require.NoError(t, err)
			}
		}
		err = cmd.Wait()
		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr, "round %d", round)
		require.Equal(t, crashBatches, committed, "round %d", round)

		s := openAt(t, path)
		db := rawDB(t, path)
		var check string
		require.NoError(t, db.QueryRowContext(context.Background(), "PRAGMA integrity_check").Scan(&check))
		assert.Equal(t, "ok", check)
		spans := queryInt(t, db, "SELECT count(*) FROM spans")
		base := int64(crashBatches * crashBatchSpans)
		assert.Contains(t, []int64{base, base + crashLastSpans}, spans, "the last batch is all or nothing")
		assert.Equal(t, spans, queryInt(t, db, "SELECT COALESCE(SUM(span_count), 0) FROM traces"))
		assert.Equal(t, spans, queryInt(t, db, "SELECT COALESCE(SUM(span_count), 0) FROM rollup_hour"))
		require.NoError(t, s.Close())
	}
}
