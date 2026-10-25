package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDockerBrokerControlWaitsForObservedStateWithoutRepeatingMutation(t *testing.T) {
	id := strings.Repeat("e", 64)
	for _, action := range []string{"start", "restart", "stop"} {
		t.Run(action, func(t *testing.T) {
			var posts, reads atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts.Add(1)
					w.WriteHeader(204)
					return
				}
				if r.URL.Path != "/containers/"+id+"/json" {
					t.Error(r.URL.Path)
					w.WriteHeader(404)
					return
				}
				n := reads.Add(1)
				running, health, started := true, "healthy", "2026-10-23T10:00:01Z"
				if action == "stop" {
					running = n == 1
				}
				if action == "start" && n == 1 {
					health = "starting"
				}
				if action == "restart" && n <= 2 {
					started = "2026-10-23T10:00:00Z"
				}
				fmt.Fprintf(w, `{"Id":%q,"State":{"Running":%t,"Paused":false,"Restarting":false,"Dead":false,"StartedAt":%q,"Health":{"Status":%q}}}`, id, running, started, health)
			}))
			defer srv.Close()
			client := srv.Client()
			client.Transport = &brokerTestTransport{url: srv.URL, base: client.Transport}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, err := handleDockerBrokerOperation(ctx, client, action, id, []string{"actions.execute"})
			if err != nil {
				t.Fatal(err)
			}
			if posts.Load() != 1 || reads.Load() < 2 || result["stateVerified"] != true {
				t.Fatalf("unverified completion: posts=%d reads=%d result=%v", posts.Load(), reads.Load(), result)
			}
		})
	}
}

func TestDockerBrokerControlUnknownWhenPostconditionCannotBeEstablished(t *testing.T) {
	for _, response := range []string{
		`{"State":{"Running":true,"Health":{"Status":"unhealthy"}}}`,
		`{"State":{"Running":false}}`,
		`{"State":{"Running":true,"Paused":true}}`,
		`{}`,
	} {
		t.Run(response, func(t *testing.T) {
			var posts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					posts.Add(1)
					w.WriteHeader(204)
					return
				}
				if response == `{}` {
					w.Write([]byte(response))
					return
				}
				w.Write([]byte(strings.Replace(response, `{"State":`, `{"Id":"`+strings.Repeat("a", 64)+`","State":`, 1)))
			}))
			defer srv.Close()
			client := srv.Client()
			client.Transport = &brokerTestTransport{url: srv.URL, base: client.Transport}
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			_, err := handleDockerBrokerOperation(ctx, client, "start", strings.Repeat("a", 64), []string{"actions.execute"})
			var uncertain *uncertainDockerError
			if !errors.As(err, &uncertain) || posts.Load() != 1 {
				t.Fatalf("must preserve uncertain outcome without retry: %v, posts=%d", err, posts.Load())
			}
		})
	}
}
