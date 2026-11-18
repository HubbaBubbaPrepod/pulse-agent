package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeGPUFixture(t *testing.T, root, path, body string) {
	t.Helper()
	path = filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDRMDiscoveryAMDAndIntelWithoutNVIDIA(t *testing.T) {
	root := t.TempDir()
	writeGPUFixture(t, root, "drm/card0/device/uevent", "DRIVER=amdgpu\nPCI_ID=1002:73BF\nPCI_SLOT_NAME=0000:03:00.0\n")
	writeGPUFixture(t, root, "drm/card0/device/gpu_busy_percent", "42\n")
	writeGPUFixture(t, root, "drm/card0/device/mem_info_vram_used", "2147483648\n")
	writeGPUFixture(t, root, "drm/card0/device/mem_info_vram_total", "8589934592\n")
	writeGPUFixture(t, root, "drm/card0/device/hwmon/hwmon0/temp1_input", "54000\n")
	writeGPUFixture(t, root, "drm/card0/device/hwmon/hwmon0/power1_average", "88000000\n")
	writeGPUFixture(t, root, "drm/card0/device/hwmon/hwmon0/fan1_input", "1800\n")
	writeGPUFixture(t, root, "drm/card1/device/uevent", "DRIVER=i915\nPCI_ID=8086:46A6\nPCI_SLOT_NAME=0000:00:02.0\n")
	writeGPUFixture(t, root, "drm/card1-DP-1/device/uevent", "DRIVER=i915\n")
	c := gpuCollector{}
	got := c.collectDRM(context.Background(), filepath.Join(root, "drm"), filepath.Join(root, "proc"), time.Unix(100, 0))
	if len(got.devices) != 2 {
		t.Fatalf("GPU discovery: %+v", got.devices)
	}
	a, b := got.devices[0], got.devices[1]
	if a.Utilization != 42 || a.MemoryUsed != 2 || a.MemoryTotal != 8 || a.Temperature != 54 || a.Power != 88 {
		t.Fatalf("sysfs units: %+v", a)
	}
	if a.Supported["fan"] {
		t.Fatal("fan RPM mislabeled as percentage")
	}
	if b.Supported["utilization"] || b.Supported["memoryTotal"] {
		t.Fatal("missing readings fabricated")
	}
}

func TestDRMProcessDeltaDeduplicatesSharedDescriptors(t *testing.T) {
	root := t.TempDir()
	writeGPUFixture(t, root, "drm/card0/device/uevent", "DRIVER=i915\nPCI_ID=8086:46A6\nPCI_SLOT_NAME=0000:00:02.0\n")
	for _, pid := range []int{20, 30} {
		writeGPUFixture(t, root, fmt.Sprintf("proc/%d/stat", pid), fmt.Sprintf("%d (render worker) S 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 123 0", pid))
	}
	write := func(ns uint64) {
		for _, path := range []string{"proc/20/fdinfo/3", "proc/20/fdinfo/4", "proc/30/fdinfo/7"} {
			writeGPUFixture(t, root, path, fmt.Sprintf("drm-driver: i915\ndrm-pdev: 0000:00:02.0\ndrm-client-id: 5\ndrm-engine-render: %d ns\ndrm-engine-capacity-render: 2\ndrm-engine-copy: 0 ns\n", ns))
		}
	}
	c := gpuCollector{}
	start := time.Unix(100, 0)
	write(100000000)
	a := c.collectDRM(context.Background(), filepath.Join(root, "drm"), filepath.Join(root, "proc"), start)
	if a.load[20] != nil {
		t.Fatal("first cumulative counter treated as interval usage")
	}
	write(350000000)
	b := c.collectDRM(context.Background(), filepath.Join(root, "drm"), filepath.Join(root, "proc"), start.Add(time.Second))
	if b.load[20] == nil || *b.load[20] != 12.5 || b.load[30] == nil || *b.load[30] != 12.5 {
		t.Fatalf("process deltas: %+v", b.load)
	}
	if b.devices[0].Utilization != 12.5 {
		t.Fatalf("shared client counted twice: %+v", b.devices[0])
	}
	write(200000000)
	c.collectDRM(context.Background(), filepath.Join(root, "drm"), filepath.Join(root, "proc"), start.Add(2*time.Second))
	write(400000000)
	d := c.collectDRM(context.Background(), filepath.Join(root, "drm"), filepath.Join(root, "proc"), start.Add(3*time.Second))
	if d.load[20] == nil || *d.load[20] != 2.5 {
		t.Fatalf("regressed counters lost high water mark: %+v", d.load)
	}
}

func TestDRMXeCyclesAndResidentVRAM(t *testing.T) {
	root := t.TempDir()
	writeGPUFixture(t, root, "drm/card0/device/uevent", "DRIVER=xe\nPCI_ID=8086:56A0\nPCI_SLOT_NAME=0000:02:00.0\n")
	writeGPUFixture(t, root, "proc/4/stat", "4 (xe) S 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 123 0")
	c := gpuCollector{}
	writeGPUFixture(t, root, "proc/4/fdinfo/1", "drm-driver: xe\ndrm-pdev: 0000:02:00.0\ndrm-client-id: 9\ndrm-cycles-rcs: 100\ndrm-total-cycles-rcs: 1000\ndrm-resident-vram0: 2048 KiB\ndrm-total-vram0: 8192 KiB\ndrm-resident-system: 32768 KiB\n")
	c.collectDRM(context.Background(), filepath.Join(root, "drm"), filepath.Join(root, "proc"), time.Unix(10, 0))
	writeGPUFixture(t, root, "proc/4/fdinfo/1", "drm-driver: xe\ndrm-pdev: 0000:02:00.0\ndrm-client-id: 9\ndrm-cycles-rcs: 400\ndrm-total-cycles-rcs: 2000\ndrm-resident-vram0: 2048 KiB\ndrm-total-vram0: 8192 KiB\ndrm-resident-system: 32768 KiB\n")
	got := c.collectDRM(context.Background(), filepath.Join(root, "drm"), filepath.Join(root, "proc"), time.Unix(11, 0))
	if got.load[4] == nil || *got.load[4] != 30 || got.memory[4] == nil || *got.memory[4] != 2 {
		t.Fatalf("xe counters: %+v", got)
	}
	if got.devices[0].Supported["memoryUsed"] {
		t.Fatal("client allocation advertised as whole-adapter memory")
	}
}
