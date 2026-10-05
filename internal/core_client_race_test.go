package internal

import (
	"context"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestStartDialCoreConcurrentWithRequests guards the data race between the
// dialCore goroutine (which assigns the core mesh client) and code paths that
// read it (publish, discovery, storage import) and Stop (which closes it).
func TestStartDialCoreConcurrentWithRequests(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lis.Close() }()
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	t.Setenv("MUXCORE_GRPC_ADDR", lis.Addr().String())
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	t.Setenv("MUXCORE_DEV_TLS_SKIP", "true")

	m := NewModule(Config{
		DBPath:   filepath.Join(t.TempDir(), "race.db"),
		GRPCAddr: "127.0.0.1:0",
	})
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			deadline := time.Now().Add(300 * time.Millisecond)
			for time.Now().Before(deadline) {
				_, _ = m.findCapabilityAddr(ctx, "media.roots")
				_, _ = m.listRegisteredRoots(ctx)
				if i == 0 { // importStoragePath takes m.mu, which would order this goroutine's reads; keep one dedicated
					_, _ = m.importStoragePath(ctx, "storage://some/key")
					continue
				}
				m.publish(ctx, "test.event", nil)
				time.Sleep(100 * time.Microsecond)
			}
		}(i)
	}
	wg.Wait()

	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
