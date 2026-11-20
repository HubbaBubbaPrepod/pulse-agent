package main

import (
	"context"
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

func TestPhaseNotificationConfirmsCurrentPermission(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var checks atomic.Int32
			panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					w.WriteHeader(status)
					return
				}
				checks.Add(1)
				fmt.Fprint(w, `{"allowed":false}`)
			}))
			defer panel.Close()
			if notifyExtensionVerifying(context.Background(), panel.Client(), panel.URL, "node", "token", strings.Repeat("a", 64)) {
				t.Fatal("revoked permission accepted")
			}
			if checks.Load() != 1 {
				t.Fatal("phase acknowledgement bypassed a fresh permission check", checks.Load())
			}
		})
	}
}

func TestRecoveryFlushPreservesVerificationWhenPanelUnavailable(t *testing.T) {
	state := t.TempDir()
	id := strings.Repeat("d", 64)
	receipt := extensionReceipt{ID: id, ExpiresAt: time.Now().Add(time.Minute), Phase: "verifying", Verification: &extensionVerification{Binding: extensionVerificationBinding{ID: id, ModuleID: "docker", ComponentID: "collector", Version: "3.0.0", ServerID: "node", Action: "stop", Target: map[string]any{"containerId": strings.Repeat("c", 64)}, SHA256: strings.Repeat("a", 64), Runtime: "wasip1", ConfigurationDigest: strings.Repeat("b", 64)}, PreparedAt: time.Now().UTC()}}
	if err := os.MkdirAll(filepath.Dir(receiptPath(state, id)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := atomicFile(receiptPath(state, id), encodeExtensionReceipt(receipt, true)); err != nil {
		t.Fatal(err)
	}
	var finalResults atomic.Int32
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var v map[string]any
			json.NewDecoder(r.Body).Decode(&v)
			if _, ok := v["result"]; ok {
				finalResults.Add(1)
			}
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer panel.Close()
	flushExtensionReceipts(context.Background(), panel.Client(), panel.URL, "node", "token", state)
	raw, err := os.ReadFile(receiptPath(state, id))
	if err != nil {
		t.Fatal(err)
	}
	var stored extensionReceipt
	if err = json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Completed || stored.Acknowledged || stored.Verification == nil || stored.Phase != "verifying" || finalResults.Load() != 0 {
		t.Fatalf("temporary outage discarded durable verification: %+v", stored)
	}
	if ok, err := claimExtensionReceipt(state, id, time.Now().Add(time.Minute)); err != nil || ok {
		t.Fatal("unfinished action can be replayed", err)
	}
}

func TestRecoveryRejectsRevocationExpiryAndMissingRestartBaseline(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*extensionReceipt)
		allowed bool
	}{
		{name: "revoked", allowed: false},
		{name: "expired", allowed: true, change: func(r *extensionReceipt) { r.ExpiresAt = time.Now().Add(-11 * time.Minute) }},
		{name: "missing-restart-baseline", allowed: true, change: func(r *extensionReceipt) { r.Verification.Binding.Action = "restart" }},
		{name: "legacy-native", allowed: true, change: func(r *extensionReceipt) { r.Verification.Binding.Runtime = "native" }},
		{name: "another-node", allowed: true, change: func(r *extensionReceipt) { r.Verification.Binding.ServerID = "another" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := strings.Repeat("e", 64)
			receipt := extensionReceipt{ID: id, ExpiresAt: time.Now().Add(time.Minute), Phase: "verifying", Verification: &extensionVerification{Binding: extensionVerificationBinding{ID: id, ModuleID: "docker", ComponentID: "collector", Version: "3.0.0", ServerID: "node", Action: "stop", Target: map[string]any{"containerId": strings.Repeat("c", 64)}, SHA256: strings.Repeat("a", 64), Runtime: "wasip1", ConfigurationDigest: strings.Repeat("b", 64)}, PreparedAt: time.Now().UTC()}}
			if tc.change != nil {
				tc.change(&receipt)
			}
			var calls atomic.Int32
			docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
			defer docker.Close()
			panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"reconcileAllowed": tc.allowed, "binding": receipt.Verification.Binding})
			}))
			defer panel.Close()
			got := reconcileExtensionReceipt(context.Background(), panel.Client(), docker.Client(), panel.URL, "node", "token", receipt)
			if calls.Load() != 0 || !got.Completed || got.Result.Status != "unknown" {
				t.Fatalf("invalid recovery reached Docker or claimed success: %+v", got)
			}
		})
	}
}
