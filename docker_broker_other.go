//go:build !windows && !linux

package main

import (
	"errors"
	"net/http"
)

func newBrokerDockerClient() (*http.Client, error) {
	return nil, errors.New("Docker broker requires Linux or Windows")
}
