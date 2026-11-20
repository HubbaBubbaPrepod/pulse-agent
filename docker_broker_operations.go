package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
)

var dockerContainerID = regexp.MustCompile(`^[a-f0-9]{64}$`)

type uncertainDockerError struct{ message string }

func (e *uncertainDockerError) Error() string { return e.message }

func decodeSelectedLogs(raw []byte) ([]string, error) {
	var text string
	if len(raw) >= 4 && (raw[0] == 1 || raw[0] == 2) && raw[1] == 0 && raw[2] == 0 && raw[3] == 0 {
		var output strings.Builder
		for len(raw) > 0 {
			if len(raw) < 8 || (raw[0] != 1 && raw[0] != 2) || raw[1] != 0 || raw[2] != 0 || raw[3] != 0 {
				return nil, errors.New("Invalid multiplexed log frame")
			}
			size := uint64(binary.BigEndian.Uint32(raw[4:8]))
			if size > uint64(len(raw)-8) {
				return nil, errors.New("Incomplete multiplexed log frame")
			}
			output.Write(raw[8 : 8+int(size)])
			raw = raw[8+int(size):]
		}
		text = output.String()
	} else {
		text = string(raw)
	}
	text = strings.TrimRight(text, "\r\n")
	if text == "" {
		return []string{}, nil
	}
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
	}
	return lines, nil
}

func handleDockerBrokerOperation(ctx context.Context, client *http.Client, action, id string, grants []string) (map[string]any, error) {
	return handlePreparedDockerBrokerOperation(ctx, client, action, id, grants, nil)
}
func handlePreparedDockerBrokerOperation(ctx context.Context, client *http.Client, action, id string, grants []string, prepare func(string) error) (map[string]any, error) {
	if !dockerContainerID.MatchString(id) {
		return nil, errors.New("A full 64-character container ID is required")
	}
	result := map[string]any{"containerId": id, "action": action, "at": time.Now().UTC().Format(time.RFC3339Nano)}
	if action == "logs" {
		raw, err := dockerGet(ctx, client, "/containers/"+id+"/logs?stdout=1&stderr=1&tail=200&timestamps=1")
		if err != nil {
			return nil, fmt.Errorf("Container logs unavailable: %w", err)
		}
		lines, err := decodeSelectedLogs(raw)
		if err != nil {
			return nil, err
		}
		result["logs"] = lines
		return result, nil
	}
	if !slices.Contains([]string{"start", "restart", "stop"}, action) {
		return nil, errors.New("Unsupported container action")
	}
	if !slices.Contains(grants, "actions.execute") {
		return nil, errors.New("Container management is not granted")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	previousStart := ""
	if action == "restart" {
		before, err := inspectDockerContainer(ctx, client, id)
		if _, parseErr := time.Parse(time.RFC3339Nano, before.StartedAt); err != nil || parseErr != nil {
			return nil, errors.New("Could not inspect container before restart; no action was sent")
		}
		previousStart = before.StartedAt
	}
	if prepare != nil {
		if err := prepare(previousStart); err != nil {
			return nil, errors.New("Could not persist Docker verification intent; no action was sent")
		}
	}
	endpoint := "http://docker/containers/" + id + "/" + action
	if action == "stop" || action == "restart" {
		endpoint += "?t=5"
	}
	request, err := http.NewRequestWithContext(ctx, "POST", endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		proof, observeErr := observeDockerBrokerOperation(ctx, client, action, id, previousStart)
		if observeErr != nil {
			return nil, observeErr
		}
		proof["outcome"] = "state-observed"
		return proof, nil
	}
	defer response.Body.Close()
	io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode == 204 {
		result["outcome"] = "completed"
	} else if response.StatusCode == 304 && (action == "start" || action == "stop") {
		result["outcome"] = "already-in-state"
	} else if response.StatusCode >= 500 {
		return nil, &uncertainDockerError{fmt.Sprintf("Docker control returned HTTP %d; action was not replayed", response.StatusCode)}
	} else {
		return nil, fmt.Errorf("Docker control returned HTTP %d", response.StatusCode)
	}

	proof, err := observeDockerBrokerOperation(ctx, client, action, id, previousStart)
	if err != nil {
		return nil, err
	}
	for key, value := range proof {
		result[key] = value
	}
	return result, nil
}

// Observations never resend the control request, including after a lost reply.
func observeDockerBrokerOperation(ctx context.Context, client *http.Client, action, id, previousStart string) (map[string]any, error) {
	if !dockerContainerID.MatchString(id) {
		return nil, errors.New("Invalid verification target")
	}
	var stableSince time.Time
	stableStartedAt := ""
	for {
		if ctx.Err() != nil {
			return nil, &uncertainDockerError{"Docker outcome could not be verified before the deadline; action was not replayed"}
		}
		state, err := inspectDockerContainer(ctx, client, id)
		if err == nil && state.matches(action, previousStart) {
			stable := action == "stop" || state.Health != nil
			if !stable {
				if stableSince.IsZero() || state.StartedAt != stableStartedAt {
					stableSince = time.Now()
					stableStartedAt = state.StartedAt
				}
				stable = time.Since(stableSince) >= 3*time.Second
			}
			if stable {
				result := map[string]any{"containerId": id, "action": action, "at": time.Now().UTC().Format(time.RFC3339Nano), "stateVerified": true, "running": *state.Running, "startedAt": state.StartedAt}
				if state.Health != nil {
					result["health"] = state.Health.Status
					result["readiness"] = "docker-healthcheck"
				} else if action != "stop" {
					result["readiness"] = "container-running-stable"
				} else {
					result["readiness"] = "container-stopped"
				}
				return result, nil
			}
		} else {
			stableSince = time.Time{}
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, &uncertainDockerError{"Docker outcome could not be verified before the deadline; action was not replayed"}
		case <-timer.C:
		}
	}
}

type dockerOperationState struct {
	Running                  *bool
	Paused, Restarting, Dead bool
	StartedAt                string
	Health                   *struct{ Status string }
}

func inspectDockerContainer(ctx context.Context, client *http.Client, id string) (dockerOperationState, error) {
	var out struct {
		ID    string `json:"Id"`
		State *dockerOperationState
	}
	raw, err := dockerGet(ctx, client, "/containers/"+id+"/json")
	if err != nil {
		return dockerOperationState{}, err
	}
	if json.Unmarshal(raw, &out) != nil || out.ID != id || out.State == nil || out.State.Running == nil {
		return dockerOperationState{}, errors.New("Invalid container inspection response")
	}
	return *out.State, nil
}

func (s dockerOperationState) matches(action, previousStart string) bool {
	if s.Running == nil || s.Restarting || s.Dead || s.Paused {
		return false
	}
	if action == "stop" {
		return !*s.Running
	}
	if !*s.Running || (s.Health != nil && s.Health.Status != "healthy") {
		return false
	}
	if action == "restart" {
		before, err := time.Parse(time.RFC3339Nano, previousStart)
		after, afterErr := time.Parse(time.RFC3339Nano, s.StartedAt)
		return err == nil && afterErr == nil && after.After(before)
	}
	return true
}
