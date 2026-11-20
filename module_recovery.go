package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"time"
)

type extensionCheckpointKey struct{}
type extensionVerificationBinding struct {
	ID                  string         `json:"id"`
	ModuleID            string         `json:"moduleId"`
	ComponentID         string         `json:"componentId"`
	Version             string         `json:"version"`
	ServerID            string         `json:"serverId"`
	Action              string         `json:"action"`
	Target              map[string]any `json:"target"`
	SHA256              string         `json:"sha256"`
	Runtime             string         `json:"runtime"`
	ConfigurationDigest string         `json:"configurationDigest"`
}
type extensionVerification struct {
	Binding           extensionVerificationBinding `json:"binding"`
	PreviousStartedAt string                       `json:"previousStartedAt,omitempty"`
	PreparedAt        time.Time                    `json:"preparedAt"`
}

func verificationForLease(lease extensionLease, previousStart string) (*extensionVerification, error) {
	id, _ := lease.Invocation.Input["containerId"].(string)
	if lease.Runtime != "wasip1" || !lease.Mutating || lease.Invocation.Trigger != "manual" || !dockerContainerID.MatchString(id) || !slices.Contains([]string{"start", "restart", "stop"}, lease.Invocation.Action) {
		return nil, errors.New("Unsupported durable verification intent")
	}
	if lease.Invocation.Action == "restart" {
		if _, err := time.Parse(time.RFC3339Nano, previousStart); err != nil {
			return nil, errors.New("Missing restart baseline")
		}
	}
	return &extensionVerification{Binding: extensionVerificationBinding{ID: lease.ID, ModuleID: lease.ModuleID, ComponentID: lease.ComponentID, Version: lease.Version, ServerID: lease.Invocation.ServerID, Action: lease.Invocation.Action, Target: map[string]any{"containerId": id}, SHA256: lease.SHA256, Runtime: lease.Runtime, ConfigurationDigest: lease.ConfigurationDigest}, PreviousStartedAt: previousStart, PreparedAt: time.Now().UTC()}, nil
}

func notifyExtensionVerifying(ctx context.Context, client *http.Client, base, server, token, id string) bool {
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/api/agent/"+url.PathEscape(server)+"/module-operations/"+id+"/result", bytes.NewBufferString(`{"phase":"verifying"}`))
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err == nil {
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		response.Body.Close()
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusConflict {
			return false
		}
	}
	// A phase acknowledgement is not permission to execute. Recheck even when
	// an older Monitor rejects the additive phase or a response was lost.
	return extensionOperationAllowed(ctx, client, base, server, token, id)
}

// Recovery can observe the original immutable target only. It never runs a guest
// or submits another Docker control request, even if the prior outcome is unknown.
func reconcileExtensionReceipt(ctx context.Context, panelClient, dockerClient *http.Client, base, server, token string, receipt extensionReceipt) extensionReceipt {
	unknown := func() extensionReceipt {
		receipt.Completed = true
		receipt.Phase = ""
		receipt.Failed = false
		receipt.Result = extensionResult{Protocol: 2, Status: "unknown", Data: json.RawMessage(`{}`), Error: "Docker outcome could not be verified; action was not replayed"}
		return receipt
	}
	v := receipt.Verification
	if v == nil {
		return unknown()
	}
	b := v.Binding
	id, _ := b.Target["containerId"].(string)
	if b.ID != receipt.ID || b.ServerID != server || b.Runtime != "wasip1" || !extensionOperationID.MatchString(b.ID) || !dockerContainerID.MatchString(id) || !extensionOperationID.MatchString(b.SHA256) || !extensionOperationID.MatchString(b.ConfigurationDigest) || !moduleID.MatchString(b.ModuleID) || !moduleID.MatchString(b.ComponentID) || !moduleVer.MatchString(b.Version) || !slices.Contains([]string{"start", "restart", "stop"}, b.Action) || len(b.Target) != 1 || v.PreparedAt.IsZero() || v.PreparedAt.After(time.Now().Add(time.Minute)) || time.Now().After(receipt.ExpiresAt.Add(10*time.Minute)) || time.Now().After(v.PreparedAt.Add(10*time.Minute)) {
		return unknown()
	}
	if b.Action == "restart" {
		if _, err := time.Parse(time.RFC3339Nano, v.PreviousStartedAt); err != nil {
			return unknown()
		}
	}
	child, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	response, err := moduleRequest(child, panelClient, "GET", base+"/api/agent/"+url.PathEscape(server)+"/module-operations/"+receipt.ID+"/status", token, nil)
	if err != nil {
		return receipt
	}
	var status struct {
		ReconcileAllowed bool                         `json:"reconcileAllowed"`
		Binding          extensionVerificationBinding `json:"binding"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&status)
	response.Body.Close()
	if decodeErr != nil {
		return receipt
	}
	if !status.ReconcileAllowed || !reflect.DeepEqual(status.Binding, b) {
		return unknown()
	}
	if dockerClient == nil {
		dockerClient, err = newBrokerDockerClient()
		if err != nil {
			return receipt
		}
		defer dockerClient.CloseIdleConnections()
	}
	proof, err := observeDockerBrokerOperation(child, dockerClient, b.Action, id, v.PreviousStartedAt)
	if err != nil {
		return receipt
	}
	proof["outcome"] = "state-observed"
	proof["reconciled"] = true
	raw, err := json.Marshal(proof)
	if err != nil {
		return unknown()
	}
	receipt.Completed = true
	receipt.Phase = ""
	receipt.Failed = false
	receipt.Result = extensionResult{Protocol: 2, Status: "ok", Data: raw}
	return receipt
}
