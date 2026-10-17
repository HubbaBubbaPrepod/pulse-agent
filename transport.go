package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"
)

// agentTransport optionally routes the configured Monitor origin to a literal
// address. HTTP Host, TLS SNI and certificate verification retain the URL identity.
func agentTransport(connectAddress string) (*http.Transport, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.MaxIdleConnsPerHost = 2
	if connectAddress == "" {
		return transport, nil
	}
	host, port, err := net.SplitHostPort(connectAddress)
	number, portErr := strconv.Atoi(port)
	if err != nil || net.ParseIP(host) == nil || portErr != nil || number < 1 || number > 65535 {
		return nil, errors.New("connect-address must be a literal IP:port, for example 127.0.0.1:2053")
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, connectAddress)
	}
	return transport, nil
}
