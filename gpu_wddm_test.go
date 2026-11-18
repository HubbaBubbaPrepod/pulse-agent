package main

import (
	"encoding/json"
	"testing"
)

func TestWDDMMapsAdaptersByLUIDAndUsesBusiestEngine(t *testing.T) {
	rows := []gpuDevice{{gpu: emptyGPU("luid:00000000:00001234", "Intel"), luid: "00000000:00001234"}, {gpu: emptyGPU("luid:00000000:00005678", "AMD"), luid: "00000000:00005678"}}
	raw := []byte(`{"engines":[{"Name":"pid_7_luid_0x00000000_0x00001234_phys_0_eng_0_engtype_3D","UtilizationPercentage":40},{"Name":"pid_7_luid_0x00000000_0x00001234_phys_0_eng_1_engtype_Copy","UtilizationPercentage":30},{"Name":"pid_9_luid_0x00000000_0x00001234_phys_0_eng_0_engtype_3D","UtilizationPercentage":15},{"Name":"pid_7_luid_0x00000000_0x00005678_phys_0_eng_0_engtype_3D","UtilizationPercentage":10}],"adapters":[{"Name":"luid_0x00000000_0x00001234_phys_0","DedicatedUsage":1048576},{"Name":"luid_0x00000000_0x00005678_phys_0","DedicatedUsage":2147483648}],"processes":[{"Name":"pid_7_luid_0x00000000_0x00001234_phys_0","DedicatedUsage":1048576},{"Name":"pid_7_luid_0x00000000_0x00005678_phys_0","DedicatedUsage":2097152}]}`)
	got := applyWDDMFixture(t, rows, raw)
	if len(got.devices) != 2 || got.devices[0].Utilization != 55 || got.devices[1].Utilization != 10 {
		t.Fatalf("adapter engine mapping: %+v", got.devices)
	}
	if got.load[7] == nil || *got.load[7] != 40 || got.memory[7] == nil || *got.memory[7] != 3 {
		t.Fatalf("PID counters: %+v", got)
	}
	if got.devices[1].MemoryUsed != 2 {
		t.Fatalf("64-bit dedicated usage: %+v", got.devices[1])
	}
}

func TestWDDMMissingCountersRemainUnavailable(t *testing.T) {
	got := applyWDDMFixture(t, []gpuDevice{{gpu: emptyGPU("luid:a", "Intel")}}, []byte(`{"engines":[],"adapters":[],"processes":[]}`))
	if got.devices[0].Supported["utilization"] || got.devices[0].Supported["memoryUsed"] || got.load[99] != nil {
		t.Fatal("unavailable counter represented as zero")
	}
}

func TestWDDMCaseInsensitiveLUID(t *testing.T) {
	got := applyWDDMFixture(t, []gpuDevice{{gpu: emptyGPU("luid:00000000:0000abcd", "Intel"), luid: "00000000:0000abcd"}}, []byte(`{"engines":[{"Name":"pid_3_luid_0x00000000_0x0000ABCD_phys_0_eng_0_engtype_3D","UtilizationPercentage":17}]}`))
	if got.devices[0].Utilization != 17 {
		t.Fatalf("case-sensitive LUID: %+v", got.devices)
	}
}

func applyWDDMFixture(t *testing.T, devices []gpuDevice, raw []byte) gpuReading {
	t.Helper()
	var counters wddmReadings
	if err := json.Unmarshal(raw, &counters); err != nil {
		t.Fatal(err)
	}
	return applyWDDM(devices, counters)
}
