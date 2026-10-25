//go:build integration

package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"strings"
	"testing"
	"time"
)

var integrationWASIGuest = flag.String("wasi-guest", "", "Explicit Docker WASI guest path")
var integrationWASIFixture = flag.String("wasi-fixture", "", "Explicit disposable Docker container ID")

func TestRealWASIDockerGuest(t *testing.T) {
	if *integrationWASIGuest == "" || *integrationWASIFixture == "" {
		t.Skip("Supply an explicit guest and disposable fixture")
	}
	if !dockerContainerID.MatchString(*integrationWASIFixture) {
		t.Fatal("Invalid fixture ID")
	}
	artifact, err := os.ReadFile(*integrationWASIGuest)
	if err != nil {
		t.Fatal(err)
	}
	client, err := newBrokerDockerClient()
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	run := func(action string, grants []string) extensionResult {
		t.Helper()
		lease := wasiLeaseFor(t, artifact)
		lease.Invocation.Trigger = "manual"
		lease.Invocation.Action = action
		lease.Invocation.Input = map[string]any{"containerId": *integrationWASIFixture}
		lease.Invocation.Grants = grants
		lease.Mutating = action != "logs"
		out, err := runExtensionLease(ctx, *integrationWASIGuest, lease)
		if err != nil {
			t.Fatal(action, err)
		}
		return out
	}
	logs := run("logs", []string{"docker.logs"})
	if logs.Status != "ok" || !strings.Contains(string(logs.Data), "pulse-wasi-fixture") {
		t.Fatal("Selected container logs unavailable", logs.Status, logs.Error)
	}
	denied := run("stop", []string{"actions.execute"})
	observed, err := inspectDockerContainer(ctx, client, *integrationWASIFixture)
	if denied.Status != "error" || err != nil || observed.Running == nil || !*observed.Running {
		t.Fatal("Denied action changed fixture state", err)
	}
	for _, action := range []string{"stop", "start", "restart"} {
		out := run(action, []string{"docker.control", "actions.execute"})
		var proof struct {
			ContainerID string `json:"containerId"`
			Verified    bool   `json:"stateVerified"`
		}
		if out.Status != "ok" || json.Unmarshal(out.Data, &proof) != nil || proof.ContainerID != *integrationWASIFixture || !proof.Verified {
			t.Fatal("Observed proof missing", action, out.Status, out.Error)
		}
		state, err := inspectDockerContainer(ctx, client, *integrationWASIFixture)
		if err != nil || state.Running == nil || *state.Running != (action != "stop") {
			t.Fatal("Actual container state differs", action, err)
		}
	}
}
