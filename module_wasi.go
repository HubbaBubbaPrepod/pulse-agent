package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/HubbaBubbaPrepod/pulse-agent/internal/modulewasm"
)

func validWASIArtifact(raw []byte) bool {
	return len(raw) >= 8 && len(raw) <= modulewasm.MaxArtifactBytes && bytes.Equal(raw[:8], []byte{0, 97, 115, 109, 1, 0, 0, 0})
}

func runExtensionLease(ctx context.Context, path string, lease extensionLease, args ...string) (extensionResult, error) {
	if err := validExtensionLease(lease, lease.Invocation.ServerID); err != nil {
		return extensionResult{}, err
	}
	child, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	if lease.Runtime == "" || lease.Runtime == "native" {
		return runExtensionInvocation(child, path, lease.Invocation, args...)
	}
	if lease.Runtime != "wasip1" {
		return extensionResult{}, errors.New("Unsupported component runtime")
	}
	file, err := os.Open(path)
	if err != nil {
		return extensionResult{}, err
	}
	defer file.Close()
	artifact, err := io.ReadAll(io.LimitReader(file, modulewasm.MaxArtifactBytes+1))
	if err != nil || !validWASIArtifact(artifact) {
		return extensionResult{}, errors.New("Invalid WASM artifact")
	}
	digest := sha256.Sum256(artifact)
	if !strings.EqualFold(lease.SHA256, hex.EncodeToString(digest[:])) {
		return extensionResult{}, errors.New("WASM artifact digest changed")
	}
	broker := newExtensionBroker(lease, nil)
	defer broker.Close()
	public := lease.Invocation
	public.Config = modulewasm.PublicFields(public.Config, lease.SecretFields)
	public.Input = modulewasm.PublicFields(public.Input, lease.SecretInputFields)
	raw, err := json.Marshal(public)
	if err != nil {
		return extensionResult{}, err
	}
	output, runErr := modulewasm.Run(child, artifact, raw, broker.Call)
	var result extensionResult
	if runErr == nil {
		result, runErr = decodeExtensionResult(output)
	}
	return broker.Finalize(result, runErr)
}
