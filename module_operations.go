package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"
)

type extensionInvocation struct {
	Protocol int             `json:"protocol"`
	ID       string          `json:"id"`
	ServerID string          `json:"serverId,omitempty"`
	Trigger  string          `json:"trigger"`
	Action   string          `json:"action,omitempty"`
	Config   map[string]any  `json:"config"`
	Input    map[string]any  `json:"input"`
	State    json.RawMessage `json:"state"`
	Grants   []string        `json:"grants"`
	Event    json.RawMessage `json:"event,omitempty"`
}
type extensionResult struct {
	Protocol int             `json:"protocol"`
	Status   string          `json:"status"`
	Data     json.RawMessage `json:"data"`
	State    json.RawMessage `json:"state,omitempty"`
	Error    string          `json:"error,omitempty"`
}
type extensionLease struct {
	ID                string              `json:"id"`
	ModuleID          string              `json:"moduleId"`
	ComponentID       string              `json:"componentId"`
	Version           string              `json:"version"`
	SHA256            string              `json:"sha256"`
	Mutating          bool                `json:"mutating"`
	Invocation        extensionInvocation `json:"invocation"`
	ExpiresAt         time.Time           `json:"expiresAt"`
	SecretFields      []string            `json:"secretFields"`
	SecretInputFields []string            `json:"secretInputFields"`
}
type extensionReceipt struct {
	ID           string          `json:"id"`
	ExpiresAt    time.Time       `json:"expiresAt"`
	Completed    bool            `json:"completed"`
	Acknowledged bool            `json:"acknowledged"`
	Result       extensionResult `json:"result"`
	Failed       bool            `json:"failed"`
}

var extensionOperationID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validExtensionLease(lease extensionLease, server string) error {
	digest, err := hex.DecodeString(lease.SHA256)
	if !extensionOperationID.MatchString(lease.ID) || !moduleID.MatchString(lease.ModuleID) || !moduleID.MatchString(lease.ComponentID) || !moduleVer.MatchString(lease.Version) || len(lease.Version) > 48 || err != nil || len(digest) != 32 || lease.Invocation.Protocol != 2 || lease.Invocation.ID != lease.ID || lease.Invocation.ServerID != server || time.Now().After(lease.ExpiresAt) || time.Until(lease.ExpiresAt) > 2*time.Minute {
		return errors.New("Invalid or expired component lease")
	}
	if lease.Mutating && !slices.Contains(lease.Invocation.Grants, "actions.execute") {
		return errors.New("Action execution is not granted")
	}
	return nil
}
func runExtensionInvocation(ctx context.Context, path string, input extensionInvocation, args ...string) (extensionResult, error) {
	var result extensionResult
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	output, err := runCollectorPayload(ctx, path, raw, args...)
	if err != nil {
		return result, err
	}
	d := json.NewDecoder(bytes.NewReader(output))
	d.DisallowUnknownFields()
	if d.Decode(&result) != nil || result.Protocol != 2 || (result.Status != "ok" && result.Status != "error" && result.Status != "unknown") || len(result.Error) > 256 {
		return result, errors.New("Invalid component response")
	}
	if len(result.Data) == 0 {
		result.Data = json.RawMessage(`{}`)
	}
	for _, raw := range []json.RawMessage{result.Data, result.State} {
		if len(raw) == 0 {
			continue
		}
		var value map[string]json.RawMessage
		if json.Unmarshal(raw, &value) != nil || value == nil {
			return result, errors.New("Component response must contain JSON objects")
		}
	}
	return result, nil
}
func receiptPath(state, id string) string { return filepath.Join(state, "module-receipts", id+".json") }
func claimExtensionReceipt(state, id string, expires time.Time) (bool, error) {
	if !extensionOperationID.MatchString(id) {
		return false, errors.New("Invalid operation ID")
	}
	path := receiptPath(state, id)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return false, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	writeErr := json.NewEncoder(file).Encode(extensionReceipt{ID: id, ExpiresAt: expires})
	syncErr := file.Sync()
	closeErr := file.Close()
	return writeErr == nil && syncErr == nil && closeErr == nil, errors.Join(writeErr, syncErr, closeErr)
}
func installExtensionCollector(ctx context.Context, client *http.Client, base, server, token, state string, lease extensionLease) (string, error) {
	if err := validExtensionLease(lease, server); err != nil {
		return "", err
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	root := filepath.Join(state, "extensions", lease.ModuleID, lease.ComponentID)
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(root, lease.Version+"-"+lease.SHA256+suffix)
	if raw, err := os.ReadFile(path); err == nil {
		digest := sha256.Sum256(raw)
		if !strings.EqualFold(hex.EncodeToString(digest[:]), lease.SHA256) {
			return "", errors.New("Cached component digest mismatch")
		}
		return path, os.Chmod(path, 0700)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	endpoint := base + "/api/agent/" + url.PathEscape(server) + "/extensions/" + url.PathEscape(lease.ModuleID) + "/" + url.PathEscape(lease.ComponentID) + "/binary?operation=" + lease.ID
	response, err := moduleRequest(ctx, client, "GET", endpoint, token, nil)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16*1024*1024+1))
	if err != nil {
		return "", err
	}
	if len(raw) == 0 || len(raw) > 16*1024*1024 {
		return "", errors.New("Component artifact exceeds limit")
	}
	digest := sha256.Sum256(raw)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), lease.SHA256) {
		return "", errors.New("Component artifact digest mismatch")
	}
	if err := atomicFile(path, raw); err != nil {
		return "", err
	}
	return path, os.Chmod(path, 0700)
}
func redactExtensionResult(result extensionResult, lease extensionLease) extensionResult {
	secrets := []string{}
	for _, key := range lease.SecretFields {
		if value, ok := lease.Invocation.Config[key].(string); ok && value != "" {
			secrets = append(secrets, value)
		}
	}
	for _, key := range lease.SecretInputFields {
		if value, ok := lease.Invocation.Input[key].(string); ok && value != "" {
			secrets = append(secrets, value)
		}
	}
	sort.SliceStable(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	pairs := []string{}
	seen := map[string]bool{}
	for _, secret := range secrets {
		if !seen[secret] {
			pairs = append(pairs, secret, "[REDACTED]")
			seen[secret] = true
		}
	}
	replace := strings.NewReplacer(pairs...).Replace
	result.Error = replace(result.Error)
	if len(result.Error) > 256 {
		result.Error = "Component reported an error"
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(result.Data))
	d.UseNumber()
	if d.Decode(&value) != nil {
		result.Data = json.RawMessage(`{}`)
		return result
	}
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			return replace(x)
		case []any:
			for i, item := range x {
				x[i] = walk(item)
			}
		case map[string]any:
			out := map[string]any{}
			for key, item := range x {
				out[replace(key)] = walk(item)
			}
			return out
		}
		return v
	}
	result.Data, _ = json.Marshal(walk(value))
	return result
}

// Bound the final encoding, including JSON escaping and redaction expansion.
func encodeExtensionReceipt(receipt extensionReceipt, mutating bool) []byte {
	raw, err := json.Marshal(receipt)
	if err == nil && len(raw) <= 768*1024 && len(receipt.Result.Data)+len(receipt.Result.State) <= 512*1024 {
		return raw
	}
	status := "error"
	if mutating {
		status = "unknown"
	}
	receipt.Result = extensionResult{Protocol: 2, Status: status, Data: json.RawMessage(`{}`), Error: "Sanitized component output exceeds limit"}
	raw, _ = json.Marshal(receipt)
	return raw
}
func extensionOperationAllowed(ctx context.Context, client *http.Client, base, server, token, id string) bool {
	response, err := moduleRequest(ctx, client, "GET", base+"/api/agent/"+url.PathEscape(server)+"/module-operations/"+id+"/status", token, nil)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	var status struct {
		Allowed bool `json:"allowed"`
	}
	return json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&status) == nil && status.Allowed
}
func executeExtensionLease(ctx context.Context, client *http.Client, base, server, token, state string, lease extensionLease) {
	if validExtensionLease(lease, server) != nil {
		return
	}
	claimed, err := claimExtensionReceipt(state, lease.ID, lease.ExpiresAt)
	if err != nil || !claimed {
		return
	}
	child, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result := extensionResult{}
	failed := true
	if extensionOperationAllowed(child, client, base, server, token, lease.ID) {
		path, err := installExtensionCollector(child, client, base, server, token, state, lease)
		if err == nil && extensionOperationAllowed(child, client, base, server, token, lease.ID) {
			watcherDone := make(chan struct{})
			go func() {
				defer close(watcherDone)
				timer := time.NewTicker(time.Second)
				defer timer.Stop()
				for {
					select {
					case <-child.Done():
						return
					case <-timer.C:
						if !extensionOperationAllowed(child, client, base, server, token, lease.ID) {
							cancel()
							return
						}
					}
				}
			}()
			result, err = runExtensionInvocation(child, path, lease.Invocation)
			failed = err != nil
			cancel()
			<-watcherDone
		}
	}
	result = redactExtensionResult(result, lease)
	receipt := extensionReceipt{ID: lease.ID, ExpiresAt: lease.ExpiresAt, Completed: true, Result: result, Failed: failed}
	raw := encodeExtensionReceipt(receipt, lease.Mutating)
	_ = atomicFile(receiptPath(state, lease.ID), raw)
}
func flushExtensionReceipts(ctx context.Context, client *http.Client, base, server, token, state string) {
	entries, err := os.ReadDir(filepath.Join(state, "module-receipts"))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if entry.IsDir() || !extensionOperationID.MatchString(id) {
			continue
		}
		path := receiptPath(state, id)
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, 768*1024+1))
		file.Close()
		if readErr != nil {
			continue
		}
		var receipt extensionReceipt
		if len(raw) > 768*1024 || json.Unmarshal(raw, &receipt) != nil || receipt.ID != id {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			receipt = extensionReceipt{ID: id, Completed: true, Failed: true, ExpiresAt: info.ModTime().Add(time.Minute)}
		}
		if receipt.Acknowledged {
			if time.Now().After(receipt.ExpiresAt.Add(2 * time.Minute)) {
				_ = os.Remove(path)
			}
			continue
		}
		// An unfinished persisted claim means the previous process may have executed it.
		if !receipt.Completed {
			receipt.Completed = true
			receipt.Failed = true
		}
		body, _ := json.Marshal(map[string]any{"result": receipt.Result, "failed": receipt.Failed})
		req, err := http.NewRequestWithContext(ctx, "POST", base+"/api/agent/"+url.PathEscape(server)+"/module-operations/"+id+"/result", bytes.NewReader(body))
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			continue
		}
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		response.Body.Close()
		if response.StatusCode == 200 || response.StatusCode == 404 || response.StatusCode == 409 {
			receipt.Acknowledged = true
			raw, _ := json.Marshal(receipt)
			_ = atomicFile(path, raw)
		}
	}
}
func runModuleOperations(ctx context.Context, client *http.Client, base, server, token, state string) {
	for ctx.Err() == nil {
		flushExtensionReceipts(ctx, client, base, server, token, state)
		response, err := moduleRequest(ctx, client, "GET", base+"/api/agent/"+url.PathEscape(server)+"/module-operations?os="+runtime.GOOS+"&arch="+runtime.GOARCH, token, nil)
		if err == nil {
			raw, readErr := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
			response.Body.Close()
			var leases []extensionLease
			if readErr == nil && len(raw) <= 1024*1024 && json.Unmarshal(raw, &leases) == nil && len(leases) <= 1 {
				for _, lease := range leases {
					executeExtensionLease(ctx, client, base, server, token, state, lease)
				}
			}
		}
		if !wait(ctx, time.Second) {
			return
		}
	}
}
