package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var wddmInstance = regexp.MustCompile(`(?i)(?:pid_(\d+)_)?luid_0x([0-9a-f]{8})_0x([0-9a-f]{8})_phys_(\d+)(?:_eng_(\d+))?`)

type wddmCounter struct {
	Name                  string
	UtilizationPercentage float64
	DedicatedUsage        uint64
}

type wddmReadings struct{ Engines, Adapters, Processes []wddmCounter }

func applyWDDM(devices []gpuDevice, counters wddmReadings) gpuReading {
	result := gpuReading{devices: devices, memory: map[int]*float64{}, load: map[int]*float64{}}
	engines := map[string]map[string]float64{}
	for _, v := range counters.Engines {
		m := wddmInstance.FindStringSubmatch(strings.ToLower(v.Name))
		if m == nil || m[5] == "" {
			continue
		}
		pid, _ := strconv.Atoi(m[1])
		luid := fmt.Sprintf("%s:%s", m[2], m[3])
		engine := m[4] + ":" + m[5]
		if engines[luid] == nil {
			engines[luid] = map[string]float64{}
		}
		engines[luid][engine] += clamp(v.UtilizationPercentage)
		if pid > 0 {
			load := max(valueOrZero(result.load[pid]), clamp(v.UtilizationPercentage))
			result.load[pid] = &load
		}
	}
	for i := range result.devices {
		d := &result.devices[i]
		if values, ok := engines[d.luid]; ok {
			for _, v := range values {
				d.Utilization = max(d.Utilization, clamp(v))
			}
			d.Supported["utilization"] = true
		}
		for _, v := range counters.Adapters {
			m := wddmInstance.FindStringSubmatch(strings.ToLower(v.Name))
			if m != nil && m[2]+":"+m[3] == d.luid {
				d.MemoryUsed += float64(v.DedicatedUsage) / gib
				d.Supported["memoryUsed"] = true
			}
		}
	}
	for _, v := range counters.Processes {
		m := wddmInstance.FindStringSubmatch(strings.ToLower(v.Name))
		if m == nil {
			continue
		}
		pid, _ := strconv.Atoi(m[1])
		if pid <= 0 {
			continue
		}
		memory := float64(v.DedicatedUsage)/mib + valueOrZero(result.memory[pid])
		result.memory[pid] = &memory
	}
	return result
}
