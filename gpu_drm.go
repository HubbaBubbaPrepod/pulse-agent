package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type drmClient struct {
	device   string
	id       string
	engines  map[string]uint64
	cycles   map[string]uint64
	totals   map[string]uint64
	capacity map[string]uint64
	memory   *float64
	at       time.Time
}

func readGPUFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 64*1024))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func gpuKeyValues(text string, delimiter string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, delimiter)
		if ok {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return values
}

func discoverDRM(root string) []gpuDevice {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	devices := []gpuDevice{}
	seen := map[string]bool{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "card") {
			continue
		}
		if _, err := strconv.ParseUint(strings.TrimPrefix(entry.Name(), "card"), 10, 32); err != nil {
			continue
		}
		path := filepath.Join(root, entry.Name(), "device")
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			continue
		}
		values := gpuKeyValues(readGPUFile(filepath.Join(path, "uevent")), "=")
		vendor, deviceID, _ := strings.Cut(strings.ToLower(values["PCI_ID"]), ":")
		if vendor == "" {
			vendor = strings.TrimPrefix(readGPUFile(filepath.Join(path, "vendor")), "0x")
		}
		bus := normalizedPCIBus(values["PCI_SLOT_NAME"])
		id := "pci:" + bus
		if bus == "" {
			id = "drm:" + entry.Name()
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		brand := map[string]string{"1002": "AMD", "8086": "Intel", "10de": "NVIDIA"}[vendor]
		if brand == "" {
			brand = "GPU"
		}
		name := readGPUFile(filepath.Join(path, "product_name"))
		if name == "" {
			name = strings.TrimSpace(brand + " " + values["DRIVER"] + " " + deviceID)
		}
		g := gpuDevice{gpu: emptyGPU(id, truncate(name, 256)), busID: bus, vendor: vendor}
		readings := []struct {
			file, key string
			target    *float64
			scale     float64
		}{
			{"gpu_busy_percent", "utilization", &g.Utilization, 1},
			{"mem_info_vram_used", "memoryUsed", &g.MemoryUsed, gib},
			{"mem_info_vram_total", "memoryTotal", &g.MemoryTotal, gib},
		}
		for _, r := range readings {
			if value, ok := finiteGPUValue(readGPUFile(filepath.Join(path, r.file))); ok {
				*r.target = value / r.scale
				g.Supported[r.key] = true
			}
		}
		g.Utilization = clamp(g.Utilization)
		readGPUHwmon(path, &g.gpu)
		devices = append(devices, g)
		if len(devices) == 64 {
			break
		}
	}
	return devices
}

func readGPUHwmon(device string, g *gpu) {
	hwmons, _ := filepath.Glob(filepath.Join(device, "hwmon", "hwmon*"))
	for _, path := range hwmons {
		if v, ok := finiteGPUValue(readGPUFile(filepath.Join(path, "temp1_input"))); ok {
			g.Temperature = v / 1000
			g.Supported["temperature"] = true
		}
		for _, file := range []string{"power1_average", "power1_input"} {
			if g.Supported["power"] {
				break
			}
			if v, ok := finiteGPUValue(readGPUFile(filepath.Join(path, file))); ok {
				g.Power = v / 1000000
				g.Supported["power"] = true
			}
		}
		// fan1_input is RPM, while the report's fan field is a percentage.
		pwm, pwmOK := finiteGPUValue(readGPUFile(filepath.Join(path, "pwm1")))
		limit, limitOK := finiteGPUValue(readGPUFile(filepath.Join(path, "pwm1_max")))
		if pwmOK && limitOK && limit > 0 {
			g.Fan = clamp(pwm / limit * 100)
			g.Supported["fan"] = true
		}
	}
}

func parseDRMClient(text string, at time.Time) drmClient {
	values := gpuKeyValues(text, ":")
	c := drmClient{device: normalizedPCIBus(values["drm-pdev"]), id: values["drm-client-id"], engines: map[string]uint64{}, cycles: map[string]uint64{}, totals: map[string]uint64{}, capacity: map[string]uint64{}, at: at}
	for key, text := range values {
		fields := strings.Fields(text)
		if len(fields) == 0 {
			continue
		}
		value, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch {
		case strings.HasPrefix(key, "drm-engine-capacity-"):
			if value > 0 {
				c.capacity[strings.TrimPrefix(key, "drm-engine-capacity-")] = value
			}
		case strings.HasPrefix(key, "drm-engine-"):
			if len(fields) == 2 && fields[1] == "ns" {
				c.engines[strings.TrimPrefix(key, "drm-engine-")] = value
			}
		case strings.HasPrefix(key, "drm-total-cycles-"):
			c.totals[strings.TrimPrefix(key, "drm-total-cycles-")] = value
		case strings.HasPrefix(key, "drm-cycles-"):
			c.cycles[strings.TrimPrefix(key, "drm-cycles-")] = value
		}
	}
	// Dedicated, resident regions only. System/shared memory is not VRAM.
	for key, text := range values {
		region, ok := strings.CutPrefix(key, "drm-resident-")
		if !ok {
			region, ok = strings.CutPrefix(key, "drm-memory-")
			if _, exists := values["drm-resident-"+region]; exists {
				continue
			}
		}
		if !ok || !(strings.HasPrefix(region, "vram") || strings.HasPrefix(region, "local")) {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) == 0 {
			continue
		}
		value, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		scale := float64(1)
		if len(fields) == 2 {
			switch fields[1] {
			case "KiB":
				scale = 1024
			case "MiB":
				scale = mib
			case "B":
			default:
				continue
			}
		}
		memory := float64(value)*scale/mib + valueOrZero(c.memory)
		c.memory = &memory
	}
	return c
}

func drmClientUsage(current *drmClient, previous drmClient) map[string]float64 {
	usage := map[string]float64{}
	seconds := current.at.Sub(previous.at).Seconds()
	if previous.at.IsZero() || seconds <= 0 {
		return usage
	}
	for engine, value := range current.engines {
		old, ok := previous.engines[engine]
		if !ok {
			continue
		}
		if value < old {
			current.engines[engine] = old
			continue
		}
		capacity := max(uint64(1), current.capacity[engine])
		usage[engine] = clamp(float64(value-old) / float64(time.Second) / seconds / float64(capacity) * 100)
	}
	for engine, value := range current.cycles {
		if _, exists := usage[engine]; exists {
			continue
		}
		old, ok := previous.cycles[engine]
		total, hasTotal := current.totals[engine]
		oldTotal, hadTotal := previous.totals[engine]
		if !ok || !hasTotal || !hadTotal {
			continue
		}
		if value < old || total < oldTotal {
			current.cycles[engine] = max(value, old)
			current.totals[engine] = max(total, oldTotal)
			continue
		}
		if total > oldTotal {
			usage[engine] = clamp(float64(value-old) / float64(total-oldTotal) / float64(max(uint64(1), current.capacity[engine])) * 100)
		}
	}
	return usage
}

func (c *gpuCollector) collectDRM(ctx context.Context, sysRoot, procRoot string, at time.Time) gpuReading {
	result := gpuReading{devices: discoverDRM(sysRoot), memory: map[int]*float64{}, load: map[int]*float64{}, processDevices: map[string]*gpuProcessCounters{}}
	deviceIndex := map[string]int{}
	for i, d := range result.devices {
		if d.busID != "" {
			deviceIndex[d.busID] = i
		}
	}
	if len(deviceIndex) == 0 {
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	next := map[string]drmClient{}
	deviceEngines := map[int]map[string]float64{}
	entries, _ := os.ReadDir(procRoot)
	filesRead := 0
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		if ctx.Err() != nil || filesRead >= 32768 {
			result.partial = true
			break
		}
		processRoot := filepath.Join(procRoot, entry.Name())
		fds, err := os.ReadDir(filepath.Join(processRoot, "fdinfo"))
		if err != nil {
			result.partial = true
			continue
		}
		seen := map[string]bool{}
		pidDeviceEngines := map[string]map[string]float64{}
		for _, fd := range fds {
			if ctx.Err() != nil || filesRead >= 32768 {
				result.partial = true
				break
			}
			filesRead++
			text := readGPUFile(filepath.Join(processRoot, "fdinfo", fd.Name()))
			if !strings.Contains(text, "drm-driver:") {
				continue
			}
			client := parseDRMClient(text, at)
			if client.device == "" {
				link, err := os.Readlink(filepath.Join(processRoot, "fd", fd.Name()))
				if err == nil {
					values := gpuKeyValues(readGPUFile(filepath.Join(sysRoot, filepath.Base(link), "device", "uevent")), "=")
					client.device = normalizedPCIBus(values["PCI_SLOT_NAME"])
				}
			}
			i, known := deviceIndex[client.device]
			if !known || client.id == "" {
				continue
			}
			key := fmt.Sprintf("%s/%s", client.device, client.id)
			if seen[key] {
				continue
			}
			seen[key] = true
			if result.processDevices[client.device] == nil {
				result.processDevices[client.device] = &gpuProcessCounters{memory: map[int]*float64{}, load: map[int]*float64{}}
			}
			processes := result.processDevices[client.device]
			usage := drmClientUsage(&client, c.drmPrevious[key])
			if _, sampled := next[key]; !sampled {
				next[key] = client
				if deviceEngines[i] == nil {
					deviceEngines[i] = map[string]float64{}
				}
				for engine, v := range usage {
					deviceEngines[i][engine] += v
				}
			}
			for engine, v := range usage {
				if pidDeviceEngines[client.device] == nil {
					pidDeviceEngines[client.device] = map[string]float64{}
				}
				pidDeviceEngines[client.device][engine] += v
			}
			if client.memory != nil {
				memory := *client.memory + valueOrZero(result.memory[pid])
				result.memory[pid] = &memory
				value := *client.memory + valueOrZero(processes.memory[pid])
				processes.memory[pid] = &value
			}
		}
		for bus, engines := range pidDeviceEngines {
			load := 0.0
			for _, v := range engines {
				load = max(load, clamp(v))
			}
			result.processDevices[bus].load[pid] = &load
			value := max(load, valueOrZero(result.load[pid]))
			result.load[pid] = &value
		}
	}
	c.drmPrevious = next
	for i, engines := range deviceEngines {
		if result.devices[i].Supported["utilization"] || len(engines) == 0 {
			continue
		}
		for _, v := range engines {
			result.devices[i].Utilization = max(result.devices[i].Utilization, clamp(v))
		}
		result.devices[i].Supported["utilization"] = true
		// DRM fdinfo measures visible clients, not a hardware-wide counter.
		result.partial = true
	}
	return result
}
