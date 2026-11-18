package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type dxgiObject struct{ vtable *[14]uintptr }

var createDXGIFactory = windows.NewLazySystemDLL("dxgi.dll").NewProc("CreateDXGIFactory1")

type dxgiAdapterDescription struct {
	Description                                                     [128]uint16
	VendorID, DeviceID, SubSysID, Revision                          uint32
	DedicatedVideoMemory, DedicatedSystemMemory, SharedSystemMemory uintptr
	LUIDLow                                                         uint32
	LUIDHigh                                                        int32
	Flags                                                           uint32
}

func releaseDXGI(object *dxgiObject) {
	syscall.SyscallN(object.vtable[2], uintptr(unsafe.Pointer(object)))
}

func windowsGPUDevices() []gpuDevice {
	if createDXGIFactory.Find() != nil {
		return nil
	}
	iid := windows.GUID{Data1: 0x770aae78, Data2: 0xf26f, Data3: 0x4dba, Data4: [8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87}}
	var factory *dxgiObject
	hr, _, _ := createDXGIFactory.Call(uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&factory)))
	if int32(hr) < 0 || factory == nil {
		return nil
	}
	defer releaseDXGI(factory)
	devices := []gpuDevice{}
	for index := 0; index < 64; index++ {
		var adapter *dxgiObject
		hr, _, _ := syscall.SyscallN(factory.vtable[12], uintptr(unsafe.Pointer(factory)), uintptr(index), uintptr(unsafe.Pointer(&adapter)))
		if int32(hr) < 0 || adapter == nil {
			break
		}
		var desc dxgiAdapterDescription
		hr, _, _ = syscall.SyscallN(adapter.vtable[10], uintptr(unsafe.Pointer(adapter)), uintptr(unsafe.Pointer(&desc)))
		releaseDXGI(adapter)
		if int32(hr) < 0 || desc.Flags&2 != 0 {
			continue
		}
		luid := fmt.Sprintf("%08x:%08x", uint32(desc.LUIDHigh), desc.LUIDLow)
		d := gpuDevice{gpu: emptyGPU("luid:"+luid, windows.UTF16ToString(desc.Description[:])), luid: luid, vendor: fmt.Sprintf("%04x", desc.VendorID)}
		// DXGI uses SIZE_T. Win32_VideoController.AdapterRAM truncates cards
		// above 4 GiB and must not be used as the VRAM capacity.
		if desc.DedicatedVideoMemory > 0 {
			d.MemoryTotal = float64(desc.DedicatedVideoMemory) / gib
			d.Supported["memoryTotal"] = true
		}
		devices = append(devices, d)
	}
	return devices
}

var pdhDLL = windows.NewLazySystemDLL("pdh.dll")
var pdhOpenQuery = pdhDLL.NewProc("PdhOpenQueryW")
var pdhAddEnglishCounter = pdhDLL.NewProc("PdhAddEnglishCounterW")
var pdhCollect = pdhDLL.NewProc("PdhCollectQueryData")
var pdhArray = pdhDLL.NewProc("PdhGetFormattedCounterArrayW")
var pdhCloseQuery = pdhDLL.NewProc("PdhCloseQuery")

type gpuPlatformState struct{ query, engine, adapterMemory, processMemory uintptr }

type pdhCounterItem struct {
	Name   *uint16
	Status uint32
	_      uint32
	Value  uint64
}

func (c *gpuCollector) closePlatformGPU() {
	if c.platform.query != 0 {
		pdhCloseQuery.Call(c.platform.query)
		c.platform = gpuPlatformState{}
	}
}

func (c *gpuCollector) openGPUCounters() bool {
	for _, proc := range []*windows.LazyProc{pdhOpenQuery, pdhAddEnglishCounter, pdhCollect, pdhArray, pdhCloseQuery} {
		if proc.Find() != nil {
			return false
		}
	}
	if c.platform.query == 0 {
		status, _, _ := pdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&c.platform.query)))
		if status != 0 {
			return false
		}
	}
	for _, counter := range []struct {
		path   string
		handle *uintptr
	}{
		{`\GPU Engine(*)\Utilization Percentage`, &c.platform.engine},
		{`\GPU Adapter Memory(*)\Dedicated Usage`, &c.platform.adapterMemory},
		{`\GPU Process Memory(*)\Dedicated Usage`, &c.platform.processMemory},
	} {
		if *counter.handle != 0 {
			continue
		}
		path, err := windows.UTF16PtrFromString(counter.path)
		if err != nil {
			continue
		}
		status, _, _ := pdhAddEnglishCounter.Call(c.platform.query, uintptr(unsafe.Pointer(path)), 0, uintptr(unsafe.Pointer(counter.handle)))
		if status != 0 {
			*counter.handle = 0
		}
	}
	return true
}

func readGPUCounterArray(counter uintptr, format uintptr) []wddmCounter {
	if counter == 0 {
		return nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		var size, count uint32
		status, _, _ := pdhArray.Call(counter, format, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), 0)
		if status != 0x800007d2 || size == 0 || size > 2*1024*1024 {
			return nil
		}
		buffer := make([]byte, size)
		status, _, _ = pdhArray.Call(counter, format, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buffer[0])))
		if status == 0x800007d2 {
			continue
		}
		if status != 0 || uint64(count)*uint64(unsafe.Sizeof(pdhCounterItem{})) > uint64(len(buffer)) {
			return nil
		}
		items := unsafe.Slice((*pdhCounterItem)(unsafe.Pointer(&buffer[0])), count)
		result := make([]wddmCounter, 0, count)
		for _, item := range items {
			if item.Status > 1 || item.Name == nil {
				continue
			}
			row := wddmCounter{Name: windows.UTF16PtrToString(item.Name)}
			if format == 0x200 {
				v := math.Float64frombits(item.Value)
				if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
					continue
				}
				row.UtilizationPercentage = v
			} else {
				if int64(item.Value) < 0 {
					continue
				}
				row.DedicatedUsage = item.Value
			}
			result = append(result, row)
		}
		runtime.KeepAlive(buffer)
		return result
	}
	return nil
}

func (c *gpuCollector) collectPlatformGPU(ctx context.Context) gpuReading {
	devices := windowsGPUDevices()
	if len(devices) == 0 {
		raw, _ := commandOutput(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `ConvertTo-Json -Compress -InputObject @{Devices=@(Get-CimInstance Win32_VideoController | Select-Object Name,PNPDeviceID)}`)
		var inventory struct {
			Devices []struct{ Name, PNPDeviceID string }
		}
		if json.Unmarshal(raw, &inventory) == nil {
			for _, d := range inventory.Devices {
				if d.PNPDeviceID != "" {
					vendor := ""
					for _, id := range []string{"10de", "1002", "8086"} {
						if strings.Contains(strings.ToLower(d.PNPDeviceID), "ven_"+id) {
							vendor = id
							break
						}
					}
					devices = append(devices, gpuDevice{gpu: emptyGPU("windows:"+d.PNPDeviceID, d.Name), vendor: vendor})
				}
			}
		}
	}
	if ctx.Err() != nil || !c.openGPUCounters() {
		return gpuReading{devices: devices}
	}
	status, _, _ := pdhCollect.Call(c.platform.query)
	if status != 0 {
		c.closePlatformGPU()
		return gpuReading{devices: devices}
	}
	counters := wddmReadings{
		Engines:   readGPUCounterArray(c.platform.engine, 0x200),
		Adapters:  readGPUCounterArray(c.platform.adapterMemory, 0x400),
		Processes: readGPUCounterArray(c.platform.processMemory, 0x400),
	}
	return applyWDDM(devices, counters)
}
