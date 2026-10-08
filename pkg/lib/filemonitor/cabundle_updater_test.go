package filemonitor

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewCertPoolStore_ValidBundle verifies a valid CA bundle loads successfully.
func TestNewCertPoolStore_ValidBundle(t *testing.T) {
	store, err := NewCertPoolStore(filepath.Join("testdata", "ca.crt"))
	require.NoError(t, err)
	require.NotNil(t, store)
	assert.NotNil(t, store.GetCertPool())
}

// TestNewCertPoolStore_RejectsBundleWithNoCerts verifies that a readable bundle
// that contains no parseable certificates is rejected, rather than silently
// producing an empty pool that can verify no client certificates.
func TestNewCertPoolStore_RejectsBundleWithNoCerts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ca.crt")
	require.NoError(t, os.WriteFile(path, []byte("not a certificate\n"), 0644))

	_, err := NewCertPoolStore(path)
	assert.Error(t, err, "a bundle with no parseable certificates should be rejected")
}

// TestStoreCABundle_PreservesPoolOnInvalidUpdate verifies that an invalid CA
// bundle update does not replace a previously usable pool.
func TestStoreCABundle_PreservesPoolOnInvalidUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ca.crt")

	validPEM, err := os.ReadFile(filepath.Join("testdata", "ca.crt"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, validPEM, 0644))

	store, err := NewCertPoolStore(path)
	require.NoError(t, err)
	original := store.GetCertPool()
	require.NotNil(t, original)

	// Overwrite with an invalid bundle and attempt to reload.
	require.NoError(t, os.WriteFile(path, []byte("garbage\n"), 0644))
	err = store.storeCABundle(path)

	assert.Error(t, err, "reloading an invalid bundle should return an error")
	assert.Same(t, original, store.GetCertPool(), "the existing pool must be preserved on an invalid update")
}

// TestCertPoolStore_ConcurrentReadWrite exercises concurrent GetCertPool reads
// against storeCABundle writes. It is meaningful under the race detector
// (go test -race): GetCertPool must synchronize with the pool replacement.
func TestCertPoolStore_ConcurrentReadWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ca.crt")

	validPEM, err := os.ReadFile(filepath.Join("testdata", "ca.crt"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, validPEM, 0644))

	store, err := NewCertPoolStore(path)
	require.NoError(t, err)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Readers spin on GetCertPool.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = store.GetCertPool()
				}
			}
		}()
	}

	// Writer repeatedly replaces the pool.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			if err := store.storeCABundle(path); err != nil {
				t.Errorf("storeCABundle failed: %v", err)
				return
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}
