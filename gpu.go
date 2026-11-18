package main

import (
	"context"
	"encoding/csv"
	"math"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type gpuDevice struct {
	gpu
	busID  string
	luid   string
	vendor string
}

type gpuReading struct {
	devices        []gpuDevice
	memory         map[int]*float64
	load           map[int]*float64
	partial        bool
	processDevices map[string]*gpuProcessCounters
}

type gpuProcessCounters struct {
	memory map[int]*float64
	load   map[int]*float64
}

type gpuCollector struct {
	drmPrevious map[string]drmClient
	platform    gpuPlatformState
}

func emptyGPU(id, name string) gpu {
	return gpu{ID: id, Name: name, History: []float64{}, Supported: map[string]bool{
		"utilization": false, "memoryUsed": false, "memoryTotal": false,
		"temperature": false, "power": false, "fan": false,
	}}
}

func finiteGPUValue(text string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	return v, err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

func normalizedPCIBus(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	parts := strings.Split(text, ":")
	if len(parts) == 3 && len(parts[0]) > 4 {
		parts[0] = parts[0][len(parts[0])-4:]
	}
	return strings.Join(parts, ":")
}

func parseNVIDIAGPUs(raw []byte) []gpuDevice {
	rows := []gpuDevice{}
	records, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil {
		return rows
	}
	for _, record := range records {
		if len(record) != 9 || strings.TrimSpace(record[0]) == "" {
			continue
		}
		g := gpuDevice{gpu: emptyGPU(strings.TrimSpace(record[0]), strings.TrimSpace(record[1])), busID: normalizedPCIBus(record[8]), vendor: "10de"}
		values := []*float64{&g.Utilization, &g.MemoryUsed, &g.MemoryTotal, &g.Temperature, &g.Power, &g.Fan}
		for i, key := range []string{"utilization", "memoryUsed", "memoryTotal", "temperature", "power", "fan"} {
			v, ok := finiteGPUValue(record[i+2])
			if !ok {
				continue
			}
			if key == "memoryUsed" || key == "memoryTotal" {
				v /= 1024
			}
			if key == "utilization" || key == "fan" {
				v = clamp(v)
			}
			*values[i] = v
			g.Supported[key] = true
		}
		rows = append(rows, g)
	}
	return rows
}

func collectNVIDIA(ctx context.Context) gpuReading {
	reading := gpuReading{memory: map[int]*float64{}, load: map[int]*float64{}}
	raw, err := commandOutput(ctx, "nvidia-smi", "--query-gpu=uuid,name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw,fan.speed,pci.bus_id", "--format=csv,noheader,nounits")
	if err != nil {
		return reading
	}
	reading.devices = parseNVIDIAGPUs(raw)
	if len(reading.devices) == 0 {
		return reading
	}
	raw, err = commandOutput(ctx, "nvidia-smi", "--query-compute-apps=pid,used_gpu_memory", "--format=csv,noheader,nounits")
	if err == nil {
		records, _ := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
		for _, row := range records {
			if len(row) != 2 {
				continue
			}
			pid, pidErr := strconv.Atoi(strings.TrimSpace(row[0]))
			v, ok := finiteGPUValue(row[1])
			if pidErr == nil && pid > 0 && ok {
				v += valueOrZero(reading.memory[pid])
				reading.memory[pid] = &v
			}
		}
	}
	if runtime.GOOS == "linux" {
		raw, err = commandOutput(ctx, "nvidia-smi", "pmon", "-c", "1", "-s", "u")
		if err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				cols := strings.Fields(line)
				if len(cols) < 4 || strings.HasPrefix(strings.TrimSpace(line), "#") {
					continue
				}
				pid, pidErr := strconv.Atoi(cols[1])
				v, ok := finiteGPUValue(cols[3])
				if pidErr == nil && pid > 0 && ok {
					v = max(v, valueOrZero(reading.load[pid]))
					reading.load[pid] = &v
				}
			}
		}
	}
	return reading
}

func mergeGPUDevices(nvidia, native []gpuDevice) []gpuDevice {
	// PCI identity is exact on Linux. Windows uses DXGI LUIDs and enriches
	// only an unambiguous name match; enumeration order is not an identity.
	if len(native) == 0 {
		return nvidia
	}
	if len(native[0].luid) > 0 || strings.HasPrefix(native[0].ID, "windows:") {
		for i := range native {
			if native[i].vendor != "10de" {
				continue
			}
			matches := []int{}
			for j := range nvidia {
				if nvidia[j].Name == native[i].Name {
					matches = append(matches, j)
				}
			}
			nativeCount := 0
			for _, d := range native {
				if d.Name == native[i].Name {
					nativeCount++
				}
			}
			if len(matches) == 1 && nativeCount == 1 {
				enrichGPU(&native[i].gpu, nvidia[matches[0]].gpu)
			}
		}
		for _, d := range nvidia {
			found := false
			for _, device := range native {
				if device.vendor == "10de" && device.Name == d.Name {
					found = true
					break
				}
			}
			if !found {
				native = append(native, d)
			}
		}
		return native
	}
	result := append([]gpuDevice{}, nvidia...)
	for _, device := range native {
		found := false
		for i := range result {
			if device.busID != "" && device.busID == result[i].busID {
				enrichGPU(&result[i].gpu, device.gpu)
				found = true
				break
			}
		}
		if !found {
			result = append(result, device)
		}
	}
	return result
}

func enrichGPU(target *gpu, extra gpu) {
	values := []*float64{&target.Utilization, &target.MemoryUsed, &target.MemoryTotal, &target.Temperature, &target.Power, &target.Fan}
	others := []float64{extra.Utilization, extra.MemoryUsed, extra.MemoryTotal, extra.Temperature, extra.Power, extra.Fan}
	for i, key := range []string{"utilization", "memoryUsed", "memoryTotal", "temperature", "power", "fan"} {
		if !target.Supported[key] && extra.Supported[key] {
			*values[i] = others[i]
			target.Supported[key] = true
		}
	}
}

func (c *gpuCollector) collect(ctx context.Context) ([]gpu, map[int]*float64, map[int]*float64, string) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	nvidia, native := readGPUSources(ctx, collectNVIDIA, c.collectPlatformGPU)
	devices := mergeGPUDevices(nvidia.devices, native.devices)
	if len(devices) > 32 {
		devices = devices[:32]
		native.partial = true
	}
	memory, load := mergeGPUProcessCounters(nvidia, native)
	rows := make([]gpu, 0, len(devices))
	for _, d := range devices {
		if d.Supported["utilization"] {
			d.History = []float64{d.Utilization}
		}
		rows = append(rows, d.gpu)
	}
	status := "unavailable: no GPU detected"
	if len(rows) > 0 {
		status = "available: per-metric driver support"
		if native.partial {
			status = "available: partial GPU telemetry or process visibility"
		}
	}
	return rows, memory, load, status
}

func readGPUSources(ctx context.Context, readNVIDIA, readPlatform func(context.Context) gpuReading) (gpuReading, gpuReading) {
	var nvidia, native gpuReading
	var workers sync.WaitGroup
	workers.Go(func() { nvidia = readNVIDIA(ctx) })
	workers.Go(func() { native = readPlatform(ctx) })
	workers.Wait()
	return nvidia, native
}

func mergeGPUProcessCounters(nvidia, native gpuReading) (map[int]*float64, map[int]*float64) {
	memory, load := map[int]*float64{}, map[int]*float64{}
	for pid, v := range nvidia.memory {
		memory[pid] = v
	}
	for pid, v := range nvidia.load {
		load[pid] = v
	}
	if native.processDevices != nil {
		preferred := map[string]bool{}
		for _, d := range nvidia.devices {
			preferred[d.busID] = true
		}
		for bus, processes := range native.processDevices {
			for pid, v := range processes.memory {
				if preferred[bus] && nvidia.memory[pid] != nil {
					continue
				}
				value := *v + valueOrZero(memory[pid])
				memory[pid] = &value
			}
			for pid, v := range processes.load {
				value := max(*v, valueOrZero(load[pid]))
				load[pid] = &value
			}
		}
		return memory, load
	}
	// WDDM already covers all adapters; its PID memory replaces the
	// NVIDIA compute-only subset instead of being added to it.
	for pid, v := range native.memory {
		memory[pid] = v
	}
	for pid, v := range native.load {
		value := max(*v, valueOrZero(load[pid]))
		load[pid] = &value
	}
	return memory, load
}
