package main

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/Microsoft/go-winio"
)

func newBrokerDockerClient() (*http.Client, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return winio.DialPipeContext(ctx, `\\.\pipe\docker_engine`)
	}, ResponseHeaderTimeout: 5 * time.Second}
	return &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
