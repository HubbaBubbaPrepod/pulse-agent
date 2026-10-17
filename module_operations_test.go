package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestExtensionProcessHelper(t *testing.T) {
	if os.Getenv("PULSE_EXTENSION_TEST_HELPER") != "1" {
		return
	}
	var input extensionInvocation
	if json.NewDecoder(os.Stdin).Decode(&input) != nil {
		os.Exit(2)
	}
	if input.Config["hang"] == true {
		time.Sleep(10 * time.Second)
	}
	json.NewEncoder(os.Stdout).Encode(extensionResult{Protocol: input.Protocol, Status: "ok", Data: json.RawMessage(`{"actual":true}`)})
	os.Exit(0)
}
func TestExtensionInvocationKeepsV2AndCancellation(t *testing.T) {
	t.Setenv("PULSE_EXTENSION_TEST_HELPER", "1")
	input := extensionInvocation{Protocol: 2, ID: strings.Repeat("a", 64), Config: map[string]any{}, Input: map[string]any{}}
	result, err := runExtensionInvocation(context.Background(), os.Args[0], input, "-test.run=TestExtensionProcessHelper")
	if err != nil || result.Protocol != 2 || string(result.Data) != `{"actual":true}` {
		t.Fatal(result, err)
	}
	input.Config["hang"] = true
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := runExtensionInvocation(ctx, os.Args[0], input, "-test.run=TestExtensionProcessHelper"); err == nil {
		t.Fatal("hung invocation not cancelled")
	}
}
func TestExtensionReceiptPreventsDuplicateExecutionAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	id := strings.Repeat("a", 64)
	if claimed, err := claimExtensionReceipt(dir, id, time.Now().Add(time.Minute)); err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	if claimed, err := claimExtensionReceipt(dir, id, time.Now().Add(time.Minute)); err != nil || claimed {
		t.Fatal("duplicate operation was claimed", claimed, err)
	}
	if claimed, err := claimExtensionReceipt(dir, "../escape", time.Now().Add(time.Minute)); err == nil || claimed {
		t.Fatal("unsafe receipt path accepted")
	}
}
func TestExtensionArtifactIsBoundToOperationAndDigest(t *testing.T) {
	data := []byte("test-artifact")
	digest := sha256.Sum256(data)
	operation := strings.Repeat("b", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/node/extensions/example/collector/binary" || r.URL.Query().Get("operation") != operation || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("artifact request lost scope", r.URL.Path)
			w.WriteHeader(403)
			return
		}
		w.Write(data)
	}))
	defer srv.Close()
	lease := extensionLease{ID: operation, ModuleID: "example", ComponentID: "collector", Version: "2.0.0", SHA256: hex.EncodeToString(digest[:]), ExpiresAt: time.Now().Add(time.Minute), Invocation: extensionInvocation{Protocol: 2, ID: operation, ServerID: "node"}}
	path, err := installExtensionCollector(context.Background(), srv.Client(), srv.URL, "node", "test-token", t.TempDir(), lease)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := os.ReadFile(path)
	if string(actual) != string(data) {
		t.Fatal("artifact bytes changed")
	}
	lease.SHA256 = strings.ToUpper(lease.SHA256)
	if _, err := installExtensionCollector(context.Background(), srv.Client(), srv.URL, "node", "test-token", t.TempDir(), lease); err != nil {
		t.Fatal("valid uppercase digest rejected", err)
	}
	lease.SHA256 = strings.Repeat("0", 64)
	if _, err := installExtensionCollector(context.Background(), srv.Client(), srv.URL, "node", "test-token", t.TempDir(), lease); err == nil {
		t.Fatal("wrong digest accepted")
	}
}
func TestExtensionRedactionIncludesKeysAndBoundsReceipts(t *testing.T) {
	lease := extensionLease{ID: strings.Repeat("c", 64), Mutating: true, SecretFields: []string{"token"}, Invocation: extensionInvocation{Config: map[string]any{"token": "x"}}}
	result := redactExtensionResult(extensionResult{Protocol: 2, Status: "ok", Data: json.RawMessage(`{"x":"x"}`), State: json.RawMessage(`{"private":"x"}`)}, lease)
	var data map[string]any
	json.Unmarshal(result.Data, &data)
	if _, ok := data["x"]; ok {
		t.Fatal("secret key exposed")
	}
	if string(result.State) != `{"private":"x"}` {
		t.Fatal("private state was changed")
	}
	huge, _ := json.Marshal(map[string]string{"value": strings.Repeat("x", 100*1024)})
	result = redactExtensionResult(extensionResult{Protocol: 2, Status: "ok", Data: huge}, lease)
	receipt := extensionReceipt{ID: lease.ID, Completed: true, Result: result}
	raw := encodeExtensionReceipt(receipt, lease.Mutating)
	if len(raw) > 768*1024 {
		t.Fatal("encoded receipt exceeds read limit")
	}
	json.Unmarshal(raw, &receipt)
	if receipt.Result.Status != "unknown" {
		t.Fatal("oversized mutation result must preserve uncertain outcome", receipt.Result.Status)
	}
}
func TestOversizedExtensionReceiptIsReportedWithoutReplay(t *testing.T) {
	state := t.TempDir()
	id := strings.Repeat("d", 64)
	if ok, err := claimExtensionReceipt(state, id, time.Now().Add(time.Minute)); err != nil || !ok {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath(state, id), []byte(strings.Repeat("x", 800*1024)), 0600); err != nil {
		t.Fatal(err)
	}
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		var body struct{ Failed bool }
		if json.NewDecoder(r.Body).Decode(&body) != nil || !body.Failed {
			t.Error("corrupt receipt not reported as uncertain failure")
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	flushExtensionReceipts(context.Background(), srv.Client(), srv.URL, "node", "token", state)
	if posts != 1 {
		t.Fatal("oversized receipt silently skipped", posts)
	}
	if ok, _ := claimExtensionReceipt(state, id, time.Now().Add(time.Minute)); ok {
		t.Fatal("operation can be replayed")
	}
}
