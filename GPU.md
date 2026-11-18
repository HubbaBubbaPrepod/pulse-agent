# GPU telemetry

[Русский](GPU.ru.md) · English

GPU collection supports Linux and Windows, including NVIDIA, AMD and Intel, integrated and discrete adapters, and mixed-vendor systems. It runs every five seconds. The wire format is unchanged. Each metric has an independent `supported` flag. Missing counters stay unavailable, including on adapters that are successfully detected.

The report includes at most 32 adapters, matching the Monitor report limit. Upgrade Monitor along with Agent so reports with measured usage and unknown VRAM capacity are accepted. NVIDIA and OS collectors run independently. External commands have a four-second timeout.

| Platform / driver         | Collection                                                                                                                                                                                   |
| ------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| NVIDIA proprietary driver | `nvidia-smi` GPU utilization, dedicated memory, temperature, power and fan percentage when exposed. Linux also reads `pmon`; compute-process memory depends on driver support.               |
| Linux AMD / amdgpu        | DRM discovery, sysfs busy percentage and dedicated VRAM, GPU hwmon temperature and power, available PWM fan percentage. DRM fdinfo supplies process counters.                                |
| Linux Intel i915 / xe     | DRM discovery and process fdinfo, including engine nanoseconds or busy/total cycle deltas. Busiest-engine utilization is derived from visible clients and normalized by engine capacity.     |
| Other Linux DRM drivers   | Device discovery and the same sysfs / standard fdinfo interfaces when implemented by the driver. NVIDIA cards can use DRM when `nvidia-smi` is unavailable.                                  |
| Windows WDDM 2+           | DXGI enumerates adapters and 64-bit dedicated VRAM capacity. Native PDH counters provide engine utilization and dedicated usage, associated by adapter LUID, including per-process readings. |

On Linux, fdinfo visibility depends on `/proc` permissions and the driver's implementation. The first sample warms up cumulative counters. Clients and shared descriptors are deduplicated by device and DRM client ID. Driver counter regressions retain the previous high-water mark. A one-second scan deadline and a 32,768-file budget bound process scanning. Client-derived utilization can undercount workloads the agent cannot inspect and does not replace an available hardware busy counter. Per-process resident local/VRAM regions are reported separately from system memory. Client buffer totals are not presented as whole-adapter VRAM usage.

On Windows, DXGI LUIDs match counters to their devices. English `Get-Counter` paths and the truncated `Win32_VideoController.AdapterRAM` field are not used. NVIDIA-specific sensors enrich a uniquely matching adapter name. Identical NVIDIA names keep their DXGI readings rather than guessing a sensor-to-card assignment. Compute-only NVIDIA adapters absent from DXGI remain visible through `nvidia-smi`. Missing PDH counters leave those fields unavailable. The native query is reused between polls and closed on shutdown. Engine rate counters warm up on their first sample. Older WDDM drivers and the Microsoft software renderer do not provide the same hardware measurements. Windows GPU process-memory counters can have OS-specific accounting limitations.

Temperature, power and fan sensors are not universally exposed by WDDM or DRM. A card can provide load without providing those sensors. Fan RPM is never labeled as a percentage. Shared system memory is never labeled as dedicated VRAM.

No driver, monitoring utility, kernel module or new Go dependency is installed automatically. NVIDIA telemetry uses the existing driver utility. Linux DRM/sysfs and Windows DXGI/PDH use interfaces already supplied by the OS and driver.

Run `pulse-agent -check` for the local adapter inventory and per-metric flags. Hardware integration checks are separated with the `integration` build tag. Automated fixtures cover both platforms, but physical validation on one machine does not validate every card/driver combination.

Sources: [Linux DRM usage ABI](https://www.kernel.org/doc/html/latest/gpu/drm-usage-stats.html), [AMD driver](https://www.kernel.org/doc/html/latest/gpu/amdgpu/), [DXGI adapter description](https://learn.microsoft.com/en-us/windows/win32/api/dxgi/ns-dxgi-dxgi_adapter_desc1), [WDDM telemetry](https://devblogs.microsoft.com/directx/gpus-in-the-task-manager/), [Windows process-memory counter limitations](https://learn.microsoft.com/en-us/troubleshoot/windows-client/performance/gpu-process-memory-counters-report-wrong-value).
