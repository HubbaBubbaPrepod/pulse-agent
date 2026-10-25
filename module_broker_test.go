package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func brokerLease() extensionLease {
	return extensionLease{Mutating: true, Invocation: extensionInvocation{Protocol: 2, ID: strings.Repeat("1", 64), ServerID: "one", Trigger: "manual", Action: "restart", Input: map[string]any{"containerId": strings.Repeat("a", 64)}, Grants: []string{"docker.read", "docker.logs", "docker.control", "actions.execute"}}}
}
func brokerCall(t *testing.T, b *extensionBroker, ctx context.Context, request string) map[string]any {
	t.Helper()
	raw, err := b.Call(ctx, json.RawMessage(request))
	if err != nil {
		t.Fatal(err)
	}
	var reply map[string]any
	if json.Unmarshal(raw, &reply) != nil {
		t.Fatal("invalid broker JSON")
	}
	return reply
}
func TestBrokerRejectsMissingGrantAndUntrustedSelectors(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, `{}`) }))
	defer server.Close()
	client := server.Client()
	client.Transport = &brokerTestTransport{url: server.URL, base: client.Transport}
	for _, request := range []string{
		`{"method":"docker.restart","params":{"containerId":"` + strings.Repeat("b", 64) + `"}}`,
		`{"method":"docker.stop","params":{}}`,
		`{"method":"docker.restart","params":{"serverId":"other"}}`,
		`{"method":"docker.restart","params":{"url":"http://outside.invalid"}}`,
		`{"method":"docker.restart","method":"docker.stop","params":{}}`,
	} {
		b := newExtensionBroker(brokerLease(), client)
		if brokerCall(t, b, context.Background(), request)["ok"] != false {
			t.Fatal("untrusted broker selector accepted")
		}
	}
	lease := brokerLease()
	lease.Invocation.Grants = []string{"actions.execute"}
	if brokerCall(t, newExtensionBroker(lease, client), context.Background(), `{"method":"docker.restart","params":{}}`)["ok"] != false {
		t.Fatal("missing Docker grant accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("denied request reached Docker")
	}
}

type brokerTestTransport struct {
	url  string
	base http.RoundTripper
}

func (t *brokerTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = strings.TrimPrefix(t.url, "http://")
	return t.base.RoundTrip(clone)
}
func TestBrokerDockerMutationIsBoundAndExecutedOnce(t *testing.T) {
	var posts atomic.Int32
	id := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/containers/"+id+"/restart":
			posts.Add(1)
			w.WriteHeader(204)
		case r.Method == "GET" && r.URL.Path == "/containers/"+id+"/json":
			started := "2026-10-25T10:00:00Z"
			if posts.Load() > 0 {
				started = "2026-10-25T10:01:00Z"
			}
			json.NewEncoder(w).Encode(map[string]any{"Id": id, "State": map[string]any{"Running": true, "StartedAt": started, "Health": map[string]any{"Status": "healthy"}}})
		default:
			t.Error("unexpected Docker endpoint")
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	client := server.Client()
	client.Transport = &brokerTestTransport{url: server.URL, base: client.Transport}
	b := newExtensionBroker(brokerLease(), client)
	first := brokerCall(t, b, context.Background(), `{"method":"docker.restart","params":{}}`)
	if first["ok"] != true {
		t.Fatal("verified restart failed", first)
	}
	if brokerCall(t, b, context.Background(), `{"method":"docker.restart","params":{}}`)["ok"] != false || posts.Load() != 1 {
		t.Fatal("mutation was repeated")
	}
	result, err := b.Finalize(extensionResult{Protocol: 2, Status: "ok", Data: json.RawMessage(`{"containerId":"forged","stateVerified":false}`)}, nil)
	var data map[string]any
	json.Unmarshal(result.Data, &data)
	if err != nil || result.Status != "ok" || data["containerId"] != id || data["stateVerified"] != true {
		t.Fatal("guest replaced the host postcondition proof")
	}
}
func TestBrokerCannotForgeSuccessfulActionWithoutHostCall(t *testing.T) {
	result, err := newExtensionBroker(brokerLease(), nil).Finalize(extensionResult{Protocol: 2, Status: "ok", Data: json.RawMessage(`{}`)}, nil)
	if err != nil || result.Status == "ok" {
		t.Fatal("guest claimed an action that never ran")
	}
}
func TestBrokerCannotHideUnknownDockerOutcome(t *testing.T) {
	id := strings.Repeat("a", 64)
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				connection.Close()
			}
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"Id": id, "State": map[string]any{"Running": true, "StartedAt": "2026-10-25T10:00:00Z"}})
	}))
	defer server.Close()
	client := server.Client()
	client.Transport = &brokerTestTransport{url: server.URL, base: client.Transport}
	b := newExtensionBroker(brokerLease(), client)
	reply := brokerCall(t, b, context.Background(), `{"method":"docker.restart","params":{}}`)
	result, err := b.Finalize(extensionResult{Protocol: 2, Status: "ok", Data: json.RawMessage(`{}`)}, nil)
	if reply["status"] != "unknown" || result.Status != "unknown" || err != nil || posts.Load() != 1 {
		t.Fatal("unknown external effect was hidden or retried")
	}
}
func TestBrokerCancellationPreventsOrCancelsHostIO(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	client := server.Client()
	client.Transport = &brokerTestTransport{url: server.URL, base: client.Transport}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := newExtensionBroker(brokerLease(), client)
	if brokerCall(t, b, ctx, `{"method":"docker.restart","params":{}}`)["ok"] != false || calls.Load() != 0 {
		t.Fatal("cancelled invocation dispatched host I/O")
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); b.Call(ctx, json.RawMessage(`{"method":"docker.restart","params":{}}`)) }()
	select {
	case <-entered:
		cancel()
	case <-time.After(3 * time.Second):
		t.Fatal("host call did not begin")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("host I/O ignored cancellation")
	}
}

func TestBrokerFinalizationKeepsBoundedTrustedProof(t *testing.T) {
	b := &extensionBroker{lease: extensionLease{Mutating: true}, mutationAttempted: true, mutationStatus: "ok", proof: map[string]any{"stateVerified": true}}
	data, _ := json.Marshal(map[string]string{"padding": strings.Repeat("x", 512*1024-20)})
	out, err := b.Finalize(extensionResult{Protocol: 2, Status: "ok", Data: data}, nil)
	if err != nil || out.Status != "unknown" || len(out.Data)+len(out.State) > 512*1024 || !strings.Contains(string(out.Data), "stateVerified") {
		t.Fatal("merged result lost bounded trusted proof", out.Status, err)
	}
}
