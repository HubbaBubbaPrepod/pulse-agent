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
	"path/filepath"
	"strings"
	"sync/atomic"
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

// Persisted verification must survive loss of the Agent process; recovery may only inspect.
func TestPersistedDockerVerificationRecoversWithoutDispatch(t *testing.T) {
	id := strings.Repeat("e", 64)
	container := strings.Repeat("c", 64)
	now := time.Now().UTC()
	state := t.TempDir()
	raw := fmt.Sprintf(`{"id":%q,"expiresAt":%q,"phase":"verifying","verification":{"binding":{"id":%q,"moduleId":"docker","componentId":"collector","version":"3.0.0","serverId":"node","action":"restart","target":{"containerId":%q},"sha256":%q,"runtime":"wasip1","configurationDigest":%q},"previousStartedAt":"2026-10-25T10:00:00Z","preparedAt":%q}}`, id, now.Add(time.Minute).Format(time.RFC3339Nano), id, container, strings.Repeat("a", 64), strings.Repeat("b", 64), now.Format(time.RFC3339Nano))
	var receipt extensionReceipt
	if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(receiptPath(state, id)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receiptPath(state, id), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	var posts atomic.Int32
	docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/containers/"+container+"/json" {
			posts.Add(1)
			t.Error("recovery dispatched an effect")
			w.WriteHeader(500)
			return
		}
		fmt.Fprintf(w, `{"Id":%q,"State":{"Running":true,"StartedAt":"2026-10-25T10:01:00Z","Health":{"Status":"healthy"}}}`, container)
	}))
	defer docker.Close()
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/node/module-operations/"+id+"/status" {
			t.Error(r.URL.Path)
			w.WriteHeader(404)
			return
		}
		var stored map[string]json.RawMessage
		json.Unmarshal([]byte(raw), &stored)
		var verification map[string]json.RawMessage
		json.Unmarshal(stored["verification"], &verification)
		fmt.Fprintf(w, `{"allowed":false,"reconcileAllowed":true,"binding":%s}`, verification["binding"])
	}))
	defer panel.Close()
	client := docker.Client()
	client.Transport = &brokerTestTransport{url: docker.URL, base: client.Transport}
	// Decode after reading disk, as a fresh Agent would do.
	persisted, _ := os.ReadFile(receiptPath(state, id))
	json.Unmarshal(persisted, &receipt)
	recovered := reconcileExtensionReceipt(context.Background(), panel.Client(), client, panel.URL, "node", "token", receipt)
	if !recovered.Completed || recovered.Failed || recovered.Result.Status != "ok" || posts.Load() != 0 {
		t.Fatalf("verification did not recover safely: %+v", recovered)
	}
	var proof map[string]any
	json.Unmarshal(recovered.Result.Data, &proof)
	if proof["containerId"] != container || proof["stateVerified"] != true {
		t.Fatal("missing trusted recovery proof", proof)
	}
}
func TestRecoveryRefusesChangedBindingBeforeDockerIO(t *testing.T) {
	id := strings.Repeat("e", 64)
	raw := fmt.Sprintf(`{"id":%q,"expiresAt":%q,"verification":{"binding":{"id":%q,"moduleId":"docker","componentId":"collector","version":"3.0.0","serverId":"node","action":"restart","target":{"containerId":%q},"sha256":%q,"runtime":"wasip1","configurationDigest":%q},"previousStartedAt":"2026-10-25T10:00:00Z","preparedAt":%q}}`, id, time.Now().Add(time.Minute).Format(time.RFC3339Nano), id, strings.Repeat("c", 64), strings.Repeat("a", 64), strings.Repeat("b", 64), time.Now().UTC().Format(time.RFC3339Nano))
	var receipt extensionReceipt
	json.Unmarshal([]byte(raw), &receipt)
	var calls atomic.Int32
	docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer docker.Close()
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"reconcileAllowed":true,"binding":{"action":"start"}}`)
	}))
	defer panel.Close()
	got := reconcileExtensionReceipt(context.Background(), panel.Client(), docker.Client(), panel.URL, "node", "token", receipt)
	if calls.Load() != 0 || got.Result.Status == "ok" {
		t.Fatal("changed binding reached Docker or claimed success")
	}
}
