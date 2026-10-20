// SPDX-License-Identifier: Apache-2.0

package otlp_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/thnhdatle/loupe/internal/adapter/otlp"
	"github.com/thnhdatle/loupe/internal/app/ingest"
	"github.com/thnhdatle/loupe/internal/testutil"
	"github.com/thnhdatle/loupe/internal/testutil/fakeingestor"
)

func TestNewServer_Validation(t *testing.T) {
	t.Parallel()
	lim, err := ingest.NewInflightLimiter(1)
	require.NoError(t, err)
	tests := []struct {
		name   string
		mutate func(*otlp.Config)
		ing    bool
		lim    bool
	}{
		{"zero max request bytes", func(c *otlp.Config) { c.MaxRequestBytes = 0 }, true, true},
		{"zero max spans", func(c *otlp.Config) { c.MaxSpansPerRequest = 0 }, true, true},
		{"zero shutdown timeout", func(c *otlp.Config) { c.ShutdownTimeout = 0 }, true, true},
		{"empty token", func(c *otlp.Config) { c.Tokens = []string{"a", ""} }, true, true},
		{"nil ingestor", nil, false, true},
		{"nil limiter", nil, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := otlp.DefaultConfig()
			if tc.mutate != nil {
				tc.mutate(&cfg)
			}
			var ing *fakeingestor.Ingestor
			if tc.ing {
				ing = fakeingestor.New()
			}
			l := lim
			if !tc.lim {
				l = nil
			}
			var err error
			if ing == nil {
				_, err = otlp.NewServer(cfg, nil, l)
			} else {
				_, err = otlp.NewServer(cfg, ing, l)
			}
			require.ErrorIs(t, err, otlp.ErrInvalidConfig)
		})
	}
}

func TestNewServer_ReportsAllConfigErrors(t *testing.T) {
	t.Parallel()
	lim, err := ingest.NewInflightLimiter(1)
	require.NoError(t, err)
	_, err = otlp.NewServer(otlp.Config{}, fakeingestor.New(), lim)
	require.ErrorIs(t, err, otlp.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "max request bytes")
	assert.Contains(t, err.Error(), "max spans")
	assert.Contains(t, err.Error(), "shutdown timeout")
}

func TestServer_RunHTTP_ServesAndStopsOnCancel(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ln := listen(t)
	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() { errc <- f.srv.RunHTTP(ctx, ln) }()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+ln.Addr().String()+"/v1/traces",
		bytes.NewReader(marshal(t, nSpans(1))))
	require.NoError(t, err)
	req.Header.Set("Content-Type", ctProto)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	testutil.Eventually(t, func() bool { return len(f.ing.Spans()) == 1 }, 5*time.Second)

	http.DefaultClient.CloseIdleConnections()
	cancel()
	select {
	case err := <-errc:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("RunHTTP did not return after cancel")
	}
}

func TestServer_RunHTTP_ListenerErrorIsReturned(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ln := listen(t)
	require.NoError(t, ln.Close())
	require.Error(t, f.srv.RunHTTP(t.Context(), ln))
}

func TestServer_RunGRPC_ServesAndStopsOnCancel(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ln := listen(t)
	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() { errc <- f.srv.RunGRPC(ctx, ln) }()

	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	_, err = coltrace.NewTraceServiceClient(conn).Export(ctx, nSpans(1))
	require.NoError(t, err)
	assert.Len(t, f.ing.Spans(), 1)
	require.NoError(t, conn.Close())

	cancel()
	select {
	case err := <-errc:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("RunGRPC did not return after cancel")
	}
}

func TestServer_RunGRPC_ListenerErrorIsReturned(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	ln := listen(t)
	require.NoError(t, ln.Close())
	require.Error(t, f.srv.RunGRPC(t.Context(), ln))
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	return ln
}
