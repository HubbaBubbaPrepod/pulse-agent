package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type dockerStats struct {
	CPU struct {
		CPUUsage struct {
			Total  uint64   `json:"total_usage"`
			PerCPU []uint64 `json:"percpu_usage"`
		} `json:"cpu_usage"`
		System uint64 `json:"system_cpu_usage"`
		Online int    `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPU struct {
		CPUUsage struct {
			Total uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		System uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	Memory struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		Incoming uint64 `json:"rx_bytes"`
		Outgoing uint64 `json:"tx_bytes"`
	} `json:"networks"`
}

func dockerStatsValues(s dockerStats) (*float64, float64, float64) {
	var cpu *float64
	if s.PreCPU.System > 0 && s.CPU.System > s.PreCPU.System && s.CPU.CPUUsage.Total >= s.PreCPU.CPUUsage.Total {
		cores := s.CPU.Online
		if cores == 0 {
			cores = len(s.CPU.CPUUsage.PerCPU)
		}
		if cores > 0 {
			v := float64(s.CPU.CPUUsage.Total-s.PreCPU.CPUUsage.Total) / float64(s.CPU.System-s.PreCPU.System) * float64(cores) * 100
			cpu = &v
		}
	}
	used := s.Memory.Usage
	cache, ok := s.Memory.Stats["inactive_file"]
	if !ok {
		cache = s.Memory.Stats["cache"]
	}
	if cache < used {
		used -= cache
	}
	return cpu, float64(used) / (1024 * 1024), float64(s.Memory.Limit) / (1024 * 1024)
}
func dockerGet(ctx context.Context, client *http.Client, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://docker"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Docker API returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if len(raw) > 1024*1024 {
		return nil, errors.New("Docker response exceeds limit")
	}
	return raw, err
}
func collectDockerSnapshot(ctx context.Context, client *http.Client) (map[string]any, error) {
	raw, err := dockerGet(ctx, client, "/version")
	if err != nil {
		return nil, err
	}
	var version struct{ Version string }
	if json.Unmarshal(raw, &version) != nil {
		return nil, errors.New("Invalid Docker version")
	}
	raw, err = dockerGet(ctx, client, "/containers/json?all=1")
	if err != nil {
		return nil, err
	}
	var containers []struct {
		ID     string `json:"Id"`
		Names  []string
		Image  string
		State  string
		Status string
	}
	if json.Unmarshal(raw, &containers) != nil {
		return nil, errors.New("Invalid Docker container list")
	}
	for _, container := range containers {
		if !dockerContainerID.MatchString(container.ID) {
			return nil, errors.New("Invalid Docker container identifier")
		}
	}
	truncated := len(containers) > 100
	if truncated {
		containers = containers[:100]
	}
	rows := make([]map[string]any, len(containers))
	semaphore := make(chan struct{}, 8)
	previous := make([]dockerStats, len(containers))
	var wg sync.WaitGroup
	for pass := 0; pass < 2; pass++ {
		for i, c := range containers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case semaphore <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-semaphore }()
				name := c.ID[:min(12, len(c.ID))]
				if len(c.Names) > 0 {
					name = truncate(strings.TrimPrefix(c.Names[0], "/"), 512)
				}
				row := map[string]any{"id": c.ID, "name": name, "image": truncate(c.Image, 1024), "state": truncate(c.State, 64), "status": truncate(c.Status, 256), "cpu": nil, "memory": nil, "memoryLimit": nil, "networkIn": nil, "networkOut": nil}
				if c.State == "running" {
					if raw, err := dockerGet(ctx, client, "/containers/"+c.ID+"/stats?stream=false&one-shot=true"); err == nil {
						var stats dockerStats
						if json.Unmarshal(raw, &stats) == nil {
							// One-shot responses have no previous CPU cycle. Compare two
							// fresh samples instead of dividing lifetime counters.
							stats.PreCPU.CPUUsage.Total = previous[i].CPU.CPUUsage.Total
							stats.PreCPU.System = previous[i].CPU.System
							previous[i] = stats
							cpu, memory, limit := dockerStatsValues(stats)
							row["cpu"] = cpu
							row["memory"] = memory
							row["memoryLimit"] = limit
							var in, out uint64
							for _, network := range stats.Networks {
								in += network.Incoming
								out += network.Outgoing
							}
							row["networkIn"] = float64(in) / (1024 * 1024)
							row["networkOut"] = float64(out) / (1024 * 1024)
						}
					}
				}
				rows[i] = row
			}()
		}
		wg.Wait()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if pass == 0 {
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	running := 0
	cpu := 0.
	memory := 0.
	completeCPU, completeMemory := true, true
	for _, r := range rows {
		if r["state"] != "running" {
			continue
		}
		running++
		if v, ok := r["cpu"].(*float64); ok && v != nil {
			cpu += *v
		} else {
			completeCPU = false
		}
		if v, ok := r["memory"].(float64); ok {
			memory += v
		} else {
			completeMemory = false
		}
	}
	logs := []string{}
	var totalCPU, totalMemory any
	if completeCPU {
		totalCPU = cpu
	}
	if completeMemory {
		totalMemory = memory
	}
	return map[string]any{"engine": map[string]any{"version": version.Version, "containers": len(rows), "running": running, "truncated": truncated}, "containers": rows, "usage": map[string]any{"cpu": totalCPU, "memory": totalMemory}, "logs": logs}, nil
}
