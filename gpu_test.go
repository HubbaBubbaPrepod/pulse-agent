package main

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestNVIDIAPartialReadings(t *testing.T) {
	rows := parseNVIDIAGPUs([]byte("GPU-a, NVIDIA RTX, 25, 1024, 8192, N/A, NaN, [Not Supported], 00000000:01:00.0\n"))
	if len(rows) != 1 {
		t.Fatalf("devices: %v", rows)
	}
	g := rows[0]
	if g.MemoryUsed != 1 || g.MemoryTotal != 8 || g.Utilization != 25 {
		t.Fatalf("wrong units: %+v", g)
	}
	if g.Supported["temperature"] || g.Supported["power"] || g.Supported["fan"] {
		t.Fatalf("invalid readings supported: %+v", g)
	}
	if g.busID != "0000:01:00.0" || math.IsNaN(g.Power) {
		t.Fatalf("invalid identity/JSON number: %+v", g)
	}
}

func TestGPUSourcesDoNotStarveEachOther(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := make(chan struct{})
	nvidia, native := readGPUSources(ctx, func(ctx context.Context) gpuReading {
		select {
		case <-started:
		case <-ctx.Done():
		}
		return gpuReading{}
	}, func(ctx context.Context) gpuReading {
		close(started)
		return gpuReading{devices: []gpuDevice{{gpu: emptyGPU("intel", "Intel")}}}
	})
	if ctx.Err() != nil || len(nvidia.devices) != 0 || len(native.devices) != 1 {
		t.Fatal("slow source consumed the other source's deadline")
	}
}

func TestGPUProcessSourcesDoNotDoubleCountNVIDIA(t *testing.T) {
	nvMemory, amdMemory := 10.0, 20.0
	nvidia := gpuReading{devices: []gpuDevice{{busID: "0000:01:00.0"}}, memory: map[int]*float64{5: &nvMemory}, load: map[int]*float64{}}
	native := gpuReading{processDevices: map[string]*gpuProcessCounters{
		"0000:01:00.0": {memory: map[int]*float64{5: &nvMemory}, load: map[int]*float64{}},
		"0000:02:00.0": {memory: map[int]*float64{5: &amdMemory}, load: map[int]*float64{}},
	}}
	memory, _ := mergeGPUProcessCounters(nvidia, native)
	if memory[5] == nil || *memory[5] != 30 {
		t.Fatalf("double counted or dropped dedicated memory: %v", memory[5])
	}
}

func TestGPUProcessDRMFillsMissingNVIDIAMetrics(t *testing.T) {
	v := 12.0
	nvidia := gpuReading{devices: []gpuDevice{{busID: "0000:01:00.0"}}}
	native := gpuReading{processDevices: map[string]*gpuProcessCounters{
		"0000:01:00.0": {memory: map[int]*float64{7: &v}, load: map[int]*float64{7: &v}},
	}}
	memory, load := mergeGPUProcessCounters(nvidia, native)
	if memory[7] == nil || *memory[7] != 12 || load[7] == nil || *load[7] != 12 {
		t.Fatal("missing NVIDIA process metrics suppressed DRM fallback")
	}
}

func TestMergeGPUDevicesKeepsMixedVendors(t *testing.T) {
	nv := gpuDevice{gpu: emptyGPU("GPU-a", "NVIDIA RTX"), busID: "0000:01:00.0", vendor: "10de"}
	nv.Utilization = 35
	nv.Supported["utilization"] = true
	intel := gpuDevice{gpu: emptyGPU("pci:0000:00:02.0", "Intel"), busID: "0000:00:02.0", vendor: "8086"}
	nvDRM := gpuDevice{gpu: emptyGPU("pci:0000:01:00.0", "NVIDIA"), busID: nv.busID, vendor: "10de"}
	rows := mergeGPUDevices([]gpuDevice{nv}, []gpuDevice{intel, nvDRM})
	if len(rows) != 2 || rows[0].ID != "GPU-a" || rows[0].Utilization != 35 || rows[1].ID != intel.ID {
		t.Fatalf("lost or duplicated GPU: %+v", rows)
	}
}

func TestMergeGPUDevicesDoesNotGuessIdenticalWindowsCards(t *testing.T) {
	nv := []gpuDevice{{gpu: emptyGPU("GPU-a", "NVIDIA RTX"), vendor: "10de"}, {gpu: emptyGPU("GPU-b", "NVIDIA RTX"), vendor: "10de"}}
	native := []gpuDevice{{gpu: emptyGPU("luid:a", "NVIDIA RTX"), luid: "a", vendor: "10de"}, {gpu: emptyGPU("luid:b", "NVIDIA RTX"), luid: "b", vendor: "10de"}, {gpu: emptyGPU("luid:c", "Intel"), luid: "c", vendor: "8086"}}
	rows := mergeGPUDevices(nv, native)
	if len(rows) != 3 || rows[0].ID != "luid:a" || rows[1].ID != "luid:b" {
		t.Fatalf("ambiguous identity: %+v", rows)
	}
}

func TestMergeGPUDevicesKeepsNVIDIATCCAdapter(t *testing.T) {
	native := []gpuDevice{{gpu: emptyGPU("luid:a", "Intel"), luid: "a", vendor: "8086"}}
	nv := []gpuDevice{{gpu: emptyGPU("GPU-tcc", "NVIDIA Tesla"), vendor: "10de"}}
	got := mergeGPUDevices(nv, native)
	if len(got) != 2 || got[1].ID != "GPU-tcc" {
		t.Fatalf("compute-only GPU lost: %+v", got)
	}
}
