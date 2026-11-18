//go:build integration && windows

package main

import (
	"context"
	"testing"
	"time"
)

func TestGPUWindowsHardware(t *testing.T) {
	devices := windowsGPUDevices()
	if len(devices) == 0 {
		t.Skip("no DXGI hardware adapters")
	}
	c := gpuCollector{}
	defer c.closePlatformGPU()
	c.collectPlatformGPU(context.Background())
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	<-timer.C
	started := time.Now()
	reading := c.collectPlatformGPU(context.Background())
	if time.Since(started) > 2*time.Second {
		t.Fatal("native GPU counter sampling blocked")
	}
	if len(reading.devices) != len(devices) {
		t.Fatalf("lost adapters: %d -> %d", len(devices), len(reading.devices))
	}
	for _, d := range reading.devices {
		if d.Name == "" || d.luid == "" {
			t.Fatalf("missing adapter identity: %+v", d)
		}
		t.Logf("%s: load=%v VRAM=%v capacity=%v", d.Name, d.Supported["utilization"], d.Supported["memoryUsed"], d.Supported["memoryTotal"])
	}
}
