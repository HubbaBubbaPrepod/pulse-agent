package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/HubbaBubbaPrepod/pulse-agent/internal/modulewasm"
)

type extensionBroker struct {
	lease                                     extensionLease
	client                                    *http.Client
	ownedClient                               bool
	mu                                        sync.Mutex
	snapshotRead, logsRead, mutationAttempted bool
	mutationStatus                            string
	proof                                     map[string]any
}

func newExtensionBroker(lease extensionLease, client *http.Client) *extensionBroker {
	return &extensionBroker{lease: lease, client: client}
}
func (b *extensionBroker) Close() {
	if b.ownedClient && b.client != nil {
		b.client.CloseIdleConnections()
	}
}

func (b *extensionBroker) Call(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	request, err := modulewasm.DecodeRequest(raw)
	if err != nil || ctx.Err() != nil {
		return modulewasm.Reply("error", nil, "Invalid or cancelled host request"), nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if ctx.Err() != nil {
		return modulewasm.Reply("error", nil, "Host request cancelled"), nil
	}
	action := strings.TrimPrefix(request.Method, "docker.")
	if action == request.Method {
		return modulewasm.Reply("error", nil, "Host method not supported on this node"), nil
	}
	denied := func() (json.RawMessage, error) {
		return modulewasm.Reply("error", nil, "Host operation is not granted or does not match this lease"), nil
	}
	grants := b.lease.Invocation.Grants
	id, _ := b.lease.Invocation.Input["containerId"].(string)
	switch action {
	case "snapshot":
		if len(request.Params) != 0 || !slices.Contains(grants, "docker.read") || b.snapshotRead {
			return denied()
		}
	case "logs", "start", "restart", "stop":
		for key, value := range request.Params {
			var target string
			if key != "containerId" || json.Unmarshal(value, &target) != nil || target != id {
				return denied()
			}
		}
		if !dockerContainerID.MatchString(id) || b.lease.Invocation.Trigger != "manual" || b.lease.Invocation.Action != action {
			return denied()
		}
		if action == "logs" {
			if !slices.Contains(grants, "docker.logs") || b.logsRead {
				return denied()
			}
		} else if !b.lease.Mutating || !slices.Contains(grants, "docker.control") || !slices.Contains(grants, "actions.execute") || b.mutationAttempted {
			return denied()
		}
	default:
		return denied()
	}
	if b.client == nil {
		b.client, err = newBrokerDockerClient()
		if err != nil {
			return modulewasm.Reply("error", nil, "Local Docker transport unavailable"), nil
		}
		b.ownedClient = true
	}
	if action == "snapshot" {
		b.snapshotRead = true
		data, err := collectDockerSnapshot(ctx, b.client)
		if err != nil {
			return modulewasm.Reply("error", nil, "Docker snapshot unavailable"), nil
		}
		return modulewasm.Reply("ok", data, ""), nil
	}
	if action == "logs" {
		b.logsRead = true
	} else {
		b.mutationAttempted = true
		b.mutationStatus = "unknown"
	}
	data, err := handleDockerBrokerOperation(ctx, b.client, action, id, grants)
	if err != nil {
		var uncertain *uncertainDockerError
		status := "error"
		if errors.As(err, &uncertain) {
			status = "unknown"
		}
		if action != "logs" {
			b.mutationStatus = status
		}
		return modulewasm.Reply(status, nil, "Docker operation failed; check its observed state"), nil
	}
	if action != "logs" {
		b.mutationStatus = "ok"
		b.proof = data
	}
	return modulewasm.Reply("ok", data, ""), nil
}

// A guest cannot turn an absent/unconfirmed Docker side effect into success by
// printing an "ok" envelope. The trusted adapter supplies the observed proof.
func (b *extensionBroker) Finalize(result extensionResult, runErr error) (extensionResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.lease.Mutating {
		return result, runErr
	}
	if !b.mutationAttempted {
		return extensionResult{Protocol: 2, Status: "error", Data: json.RawMessage(`{}`), Error: "No authorized host action was executed"}, nil
	}
	if b.mutationStatus != "ok" || runErr != nil || result.Status != "ok" {
		status := b.mutationStatus
		if status != "error" {
			status = "unknown"
		}
		raw, _ := json.Marshal(b.proof)
		if b.proof == nil {
			raw = []byte(`{}`)
		}
		return extensionResult{Protocol: 2, Status: status, Data: raw, Error: "Host action outcome requires verification; it was not replayed"}, nil
	}
	var data map[string]any
	if json.Unmarshal(result.Data, &data) != nil || data == nil {
		data = map[string]any{}
	}
	for key, value := range b.proof {
		data[key] = value
	}
	result.Data, runErr = json.Marshal(data)
	return result, runErr
}
