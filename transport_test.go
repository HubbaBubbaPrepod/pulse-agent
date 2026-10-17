package main

import (
	"context"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDirectConnectionPreservesHTTPSIdentity(t *testing.T) {
	var host, sni string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host = r.Host
		sni = r.TLS.ServerName
		w.WriteHeader(200)
	}))
	defer server.Close()
	transport, err := agentTransport(strings.TrimPrefix(server.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport.TLSClientConfig.RootCAs = roots
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	if err := deliver(context.Background(), client, "https://example.com:7443/report", "test-token", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if host != "example.com:7443" || sni != "example.com" {
		t.Fatalf("identity changed: host=%q sni=%q", host, sni)
	}
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("TLS verification disabled")
	}
	if err := deliver(context.Background(), client, "https://wrong.test:7443/report", "test-token", []byte(`{}`)); err == nil {
		t.Fatal("wrong certificate identity accepted")
	}
}

func TestDirectConnectionRejectsInvalidAddresses(t *testing.T) {
	for _, address := range []string{"localhost:2053", "127.0.0.1", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:https", "https://127.0.0.1:2053"} {
		t.Run(address, func(t *testing.T) {
			if _, err := agentTransport(address); err == nil {
				t.Fatal("invalid address accepted")
			}
		})
	}
	for _, address := range []string{"", "127.0.0.1:2053", "[::1]:2053"} {
		t.Run("valid-"+address, func(t *testing.T) {
			transport, err := agentTransport(address)
			if err != nil {
				t.Fatal(err)
			}
			transport.CloseIdleConnections()
		})
	}
}
