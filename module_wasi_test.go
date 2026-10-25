package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var extensionWASIFixture = sync.OnceValues(func() ([]byte, error) {
	directory, err := os.MkdirTemp("", "pulse-extension-wasi-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "guest.wasm")
	command := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-trimpath", "-o", path, "./testdata/extension-wasi")
	command.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("fixture build: %w: %s", err, output)
	}
	return os.ReadFile(path)
})

func wasiLeaseFor(t *testing.T, data []byte) extensionLease {
	t.Helper()
	hash := sha256.Sum256(data)
	id := strings.Repeat("a", 64)
	return extensionLease{ID: id, ModuleID: "isolated", ComponentID: "worker", Version: "2.1.0", SHA256: hex.EncodeToString(hash[:]), Runtime: "wasip1", ExpiresAt: time.Now().Add(time.Minute), Invocation: extensionInvocation{Protocol: 2, ID: id, ServerID: "one", Config: map[string]any{}, Input: map[string]any{}, State: json.RawMessage(`{}`)}}
}
func TestWASILeaseWithholdsHostSecretsFromGuest(t *testing.T) {
	data, err := extensionWASIFixture()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "guest.wasm")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	lease := wasiLeaseFor(t, data)
	lease.SecretFields = []string{"secret"}
	lease.SecretInputFields = []string{"password"}
	lease.Invocation.Config = map[string]any{"secret": "private-secret", "telegramToken": "host-token", "telegramChatId": "private-chat", "serverId": "private-scope", "label": "visible"}
	lease.Invocation.Input = map[string]any{"password": "private-input", "value": "public"}
	result, err := runExtensionLease(context.Background(), path, lease)
	if err != nil || result.Status != "ok" {
		t.Fatal(result, err)
	}
	for _, secret := range []string{"private-secret", "host-token", "private-chat", "private-scope", "private-input"} {
		if strings.Contains(string(result.Data), secret) {
			t.Fatal("guest received host-only settings")
		}
	}
	if !strings.Contains(string(result.Data), "visible") || !strings.Contains(string(result.Data), "public") {
		t.Fatal("public inputs were lost")
	}
}
func TestWASILeaseDoesNotExecuteNativeFallback(t *testing.T) {
	t.Setenv("PULSE_EXTENSION_TEST_HELPER", "1")
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	lease := wasiLeaseFor(t, data)
	if _, err = runExtensionLease(context.Background(), os.Args[0], lease, "-test.run=TestExtensionProcessHelper"); err == nil {
		t.Fatal("native helper ran under a WASI lease")
	}
	lease.Runtime = "future-runtime"
	if _, err = runExtensionLease(context.Background(), os.Args[0], lease, "-test.run=TestExtensionProcessHelper"); err == nil {
		t.Fatal("unknown runtime fell back to native execution")
	}
}

func TestWASIExecutionIsCancelledWhenOperationRevoked(t *testing.T) {
	artifact, err := extensionWASIFixture()
	if err != nil {
		t.Fatal(err)
	}
	lease := wasiLeaseFor(t, artifact)
	lease.Invocation.Config["hang"] = true
	var checks atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/binary") {
			w.Write(artifact)
			return
		}
		allowed := checks.Add(1) <= 2
		json.NewEncoder(w).Encode(map[string]bool{"allowed": allowed})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	state := t.TempDir()
	start := time.Now()
	executeExtensionLease(ctx, server.Client(), server.URL, "one", "test-token", state, lease)
	if checks.Load() < 3 || time.Since(start) > 6*time.Second {
		t.Fatal("revoked guest was not promptly cancelled")
	}
	raw, err := os.ReadFile(receiptPath(state, lease.ID))
	if err != nil {
		t.Fatal(err)
	}
	var receipt extensionReceipt
	if json.Unmarshal(raw, &receipt) != nil || !receipt.Completed || !receipt.Failed {
		t.Fatal("revocation did not persist failed receipt")
	}
	if claimed, err := claimExtensionReceipt(state, lease.ID, lease.ExpiresAt); err != nil || claimed {
		t.Fatal("revoked invocation can be replayed", err)
	}
}
func TestExtensionResultRejectsTrailingJSON(t *testing.T) {
	if _, err := decodeExtensionResult([]byte(`{"protocol":2,"status":"ok","data":{}} {}`)); err == nil {
		t.Fatal("multiple responses accepted")
	}
}
