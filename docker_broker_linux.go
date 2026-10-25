package main

import (
	"context"
	"net"
	"net/http"
	"time"
)

func newBrokerDockerClient() (*http.Client, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", "/var/run/docker.sock")
	}, ResponseHeaderTimeout: 15 * time.Second}
	return &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
