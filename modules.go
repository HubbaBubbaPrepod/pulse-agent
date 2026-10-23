package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

type desiredModule struct {
	ID          string         `json:"id"`
	Version     string         `json:"version"`
	SHA256      string         `json:"sha256"`
	Unsupported bool           `json:"unsupported"`
	Config      map[string]any `json:"config"`
}

var moduleID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var moduleVer = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9-]+)?$`)

func runCollector(ctx context.Context, path string, config map[string]any, args ...string) (json.RawMessage, error) {
	input, err := json.Marshal(map[string]any{"protocol": 1, "config": config})
	if err != nil {
		return nil, err
	}
	return runCollectorPayload(ctx, path, input, args...)
}

func runCollectorPayload(ctx context.Context, path string, input []byte, args ...string) (json.RawMessage, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	configureCommand(cmd)
	cmd.Stdin = bytes.NewReader(input)
	var out limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	if err := runModuleCommand(cmd); err != nil {
		return nil, errors.New("Collector failed or timed out")
	}
	if !json.Valid(out.Bytes()) || len(out.Bytes()) > 512*1024 {
		return nil, errors.New("Collector returned invalid or oversized JSON")
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(out.Bytes(), &obj) != nil || obj == nil {
		return nil, errors.New("Collector must return a JSON object")
	}
	return out.Bytes(), nil
}
func moduleRequest(ctx context.Context, client *http.Client, method, endpoint, token string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != 200 {
		response.Body.Close()
		return nil, fmt.Errorf("module endpoint returned HTTP %d", response.StatusCode)
	}
	return response, nil
}
func installCollector(ctx context.Context, client *http.Client, base, id, token, state string, m desiredModule) (string, error) {
	if !moduleID.MatchString(m.ID) || !moduleVer.MatchString(m.Version) || len(m.Version) > 48 || len(m.SHA256) != 64 {
		return "", errors.New("Invalid module identity")
	}
	expected, err := hex.DecodeString(m.SHA256)
	if err != nil {
		return "", err
	}
	root := filepath.Join(state, "modules", m.ID)
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	path := filepath.Join(root, m.Version+"-"+m.SHA256+suffix)
	if raw, err := os.ReadFile(path); err == nil {
		hash := sha256.Sum256(raw)
		if bytes.Equal(hash[:], expected) {
			return path, nil
		}
		return "", errors.New("Installed module checksum mismatch")
	}
	endpoint := base + "/api/agent/" + url.PathEscape(id) + "/modules/" + url.PathEscape(m.ID) + "/binary?os=" + runtime.GOOS + "&arch=" + runtime.GOARCH
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
		return "", errors.New("Module binary exceeds limit")
	}
	hash := sha256.Sum256(raw)
	if !bytes.Equal(hash[:], expected) {
		return "", errors.New("Downloaded module checksum mismatch")
	}
	if err := atomicFile(path, raw); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0700); err != nil {
		return "", err
	}
	return path, nil
}
func runModules(ctx context.Context, client *http.Client, base, id, token, state string) {
	for ctx.Err() == nil {
		endpoint := base + "/api/agent/" + url.PathEscape(id) + "/modules?os=" + runtime.GOOS + "&arch=" + runtime.GOARCH
		response, err := moduleRequest(ctx, client, "GET", endpoint, token, nil)
		modules := []desiredModule{}
		if err == nil {
			raw, readErr := io.ReadAll(io.LimitReader(response.Body, 128*1024+1))
			response.Body.Close()
			if readErr != nil || len(raw) > 128*1024 || json.Unmarshal(raw, &modules) != nil || len(modules) > 32 {
				err = errors.New("Invalid module configuration")
			}
		}
		if err != nil {
			if ctx.Err() == nil {
				slog.Debug("module sync unavailable", "error", err)
			}
			if !wait(ctx, 5*time.Second) {
				return
			}
			continue
		}
		for _, m := range modules {
			if !moduleID.MatchString(m.ID) {
				continue
			}
			status := "ok"
			message := ""
			data := json.RawMessage(`{}`)
			if m.Unsupported {
				status = "unsupported"
				message = "No collector for this platform"
			} else {
				collectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				path, err := installCollector(collectCtx, client, base, id, token, state, m)
				if err == nil {
					data, err = runCollector(collectCtx, path, m.Config)
				}
				cancel()
				if err != nil {
					status = "error"
					message = "Collector unavailable, failed or timed out"
					data = json.RawMessage(`{}`)
				}
			}
			raw, err := json.Marshal(map[string]any{"status": status, "error": message, "data": data})
			if err != nil {
				continue
			}
			reportURL := base + "/api/agent/" + url.PathEscape(id) + "/modules/" + url.PathEscape(m.ID) + "/data"
			response, err := moduleRequest(ctx, client, "POST", reportURL, token, raw)
			if err == nil {
				io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
				response.Body.Close()
			} else if ctx.Err() == nil && !strings.Contains(err.Error(), "HTTP 403") {
				slog.Debug("module result not accepted", "module", m.ID)
			}
		}
		if !wait(ctx, 5*time.Second) {
			return
		}
	}
}
