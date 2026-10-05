//go:build cgo

package gpu

import (
	"context"
	"fmt"
	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

type native struct{ lib nvml.Interface }

func newNative() (p provider, err error) {
	// The binding loads libnvidia-ml at runtime; hosts without it remain usable.
	defer func() {
		if r := recover(); r != nil {
			p = nil
			err = fmt.Errorf("NVML initialization: %v", r)
		}
	}()
	lib := nvml.New()
	if ret := lib.Init(); ret != nvml.SUCCESS {
		return nil, fmt.Errorf("NVML initialization: %v", ret)
	}
	return &native{lib: lib}, nil
}
func (n *native) Close() error {
	if ret := n.lib.Shutdown(); ret != nvml.SUCCESS {
		return fmt.Errorf("NVML shutdown: %v", ret)
	}
	return nil
}
func (n *native) Collect(ctx context.Context) (devices []GPU, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("NVML collection: %v", r)
		}
	}()
	count, ret := n.lib.DeviceGetCount()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("NVML count: %v", ret)
	}
	devices = []GPU{}
	for i := 0; i < count; i++ {
		if err := ctx.Err(); err != nil {
			return devices, err
		}
		d, ret := n.lib.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			continue
		}
		g := GPU{Index: i, Source: "nvml"}
		g.UUID, _ = d.GetUUID()
		g.Name, _ = d.GetName()
		if v, r := d.GetUtilizationRates(); r == nvml.SUCCESS {
			g.Utilization = ptr(float64(v.Gpu))
			g.MemoryUtilization = ptr(float64(v.Memory))
		}
		if v, r := d.GetMemoryInfo(); r == nvml.SUCCESS {
			g.MemoryUsedMB = ptr(float64(v.Used) / 1048576)
			g.MemoryTotalMB = ptr(float64(v.Total) / 1048576)
		}
		if v, r := d.GetTemperature(nvml.TEMPERATURE_GPU); r == nvml.SUCCESS {
			g.TemperatureC = ptr(float64(v))
		}
		if v, r := d.GetPowerUsage(); r == nvml.SUCCESS {
			g.PowerW = ptr(float64(v) / 1000)
		}
		if v, r := d.GetPowerManagementLimit(); r == nvml.SUCCESS {
			g.PowerLimitW = ptr(float64(v) / 1000)
		}
		if v, r := d.GetClockInfo(nvml.CLOCK_SM); r == nvml.SUCCESS {
			g.SMClockMHz = ptr(float64(v))
		}
		if v, r := d.GetClockInfo(nvml.CLOCK_MEM); r == nvml.SUCCESS {
			g.MemoryClockMHz = ptr(float64(v))
		}
		if v, r := d.GetPcieThroughput(nvml.PCIE_UTIL_RX_BYTES); r == nvml.SUCCESS {
			g.PCIeRxMBs = ptr(float64(v) / 1024)
		}
		if v, r := d.GetPcieThroughput(nvml.PCIE_UTIL_TX_BYTES); r == nvml.SUCCESS {
			g.PCIeTxMBs = ptr(float64(v) / 1024)
		}
		if v, r := d.GetTotalEccErrors(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, nvml.VOLATILE_ECC); r == nvml.SUCCESS {
			g.ECCErrors = &v
		}
		if v, r := d.GetCurrentClocksThrottleReasons(); r == nvml.SUCCESS { // Idle is not performance throttling.
			active := v & ^uint64(nvml.ClocksThrottleReasonGpuIdle) != 0
			g.Throttled = &active
		}
		for link := 0; link < 18; link++ {
			if state, r := d.GetNvLinkState(link); r == nvml.SUCCESS && state == nvml.FEATURE_ENABLED {
				description := fmt.Sprintf("NVLink %d active", link)
				if pci, r := d.GetNvLinkRemotePciInfo(link); r == nvml.SUCCESS {
					busID := make([]byte, 0, len(pci.BusId))
					for _, char := range pci.BusId {
						if char == 0 {
							break
						}
						busID = append(busID, byte(char))
					}
					if len(busID) > 0 {
						description += " to " + string(busID)
					}
				}
				g.Topology = append(g.Topology, description)
			}
		}
		devices = append(devices, g)
	}
	return devices, nil
}
