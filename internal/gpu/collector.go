// Package gpu collects real NVIDIA measurements. Unsupported measurements stay nil.
package gpu

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type GPU struct {
	Index             int      `json:"index"`
	UUID              string   `json:"uuid"`
	Name              string   `json:"name"`
	Utilization       *float64 `json:"utilization"`
	MemoryUsedMB      *float64 `json:"memoryUsedMB"`
	MemoryTotalMB     *float64 `json:"memoryTotalMB"`
	MemoryUtilization *float64 `json:"memoryUtilization"`
	TemperatureC      *float64 `json:"temperatureC"`
	PowerW            *float64 `json:"powerW"`
	PowerLimitW       *float64 `json:"powerLimitW"`
	SMClockMHz        *float64 `json:"smClockMHz"`
	MemoryClockMHz    *float64 `json:"memoryClockMHz"`
	PCIeRxMBs         *float64 `json:"pcieRxMBs"`
	PCIeTxMBs         *float64 `json:"pcieTxMBs"`
	NVLinkRxMBs       *float64 `json:"nvlinkRxMBs"`
	NVLinkTxMBs       *float64 `json:"nvlinkTxMBs"`
	ECCErrors         *uint64  `json:"eccErrors"`
	XIDErrors         *uint64  `json:"xidErrors"`
	Throttled         *bool    `json:"throttled"`
	Source            string   `json:"source"`
	Topology          []string `json:"topology,omitempty"`
}
type provider interface {
	Collect(context.Context) ([]GPU, error)
	Close() error
}

var errUnavailable = errors.New("NVIDIA GPU collection unavailable")

type Collector struct {
	mu          sync.Mutex
	native      provider
	nativeError error
	runSMI      func(context.Context) ([]GPU, error)
	dcgmURL     string
	closed      bool
}

func NewCollector(dcgmURL string) *Collector {
	p, err := newNative()
	return &Collector{native: p, nativeError: err, runSMI: collectSMI, dcgmURL: dcgmURL}
}
func (c *Collector) Collect(ctx context.Context) ([]GPU, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return []GPU{}, errors.New("GPU collector closed")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var devices []GPU
	var err error
	if c.native != nil {
		devices, err = c.native.Collect(ctx)
	}
	if c.native == nil || err != nil {
		devices, err = c.runSMI(ctx)
	}
	if devices == nil {
		devices = []GPU{}
	}
	if err != nil {
		return devices, fmt.Errorf("%w: NVML: %v; nvidia-smi: %v", errUnavailable, c.nativeError, err)
	}
	if c.dcgmURL != "" {
		if scrapeErr := c.supplement(ctx, devices); scrapeErr != nil {
			return devices, fmt.Errorf("DCGM: %w", scrapeErr)
		}
	}
	return devices, nil
}
func (c *Collector) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.native != nil {
		return c.native.Close()
	}
	return nil
}
func ptr(v float64) *float64 { return &v }
func collectSMI(ctx context.Context) ([]GPU, error) {
	out, err := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=index,uuid,name,utilization.gpu,memory.used,memory.total,utilization.memory,temperature.gpu,power.draw,power.limit,clocks.sm,clocks.mem", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil, err
	}
	return parseSMI(string(out))
}
func parseSMI(input string) ([]GPU, error) {
	rows, err := csv.NewReader(strings.NewReader(input)).ReadAll()
	if err != nil {
		return nil, err
	}
	devices := []GPU{}
	for _, row := range rows {
		if len(row) != 12 {
			return nil, errors.New("invalid nvidia-smi row")
		}
		index, err := strconv.Atoi(strings.TrimSpace(row[0]))
		if err != nil {
			return nil, err
		}
		g := GPU{Index: index, UUID: strings.TrimSpace(row[1]), Name: strings.TrimSpace(row[2]), Source: "nvidia-smi"}
		fields := []**float64{&g.Utilization, &g.MemoryUsedMB, &g.MemoryTotalMB, &g.MemoryUtilization, &g.TemperatureC, &g.PowerW, &g.PowerLimitW, &g.SMClockMHz, &g.MemoryClockMHz}
		for i, field := range fields {
			if v, err := strconv.ParseFloat(strings.TrimSpace(row[i+3]), 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 {
				*field = ptr(v)
			}
		}
		devices = append(devices, g)
	}
	return devices, nil
}
func (c *Collector) supplement(ctx context.Context, devices []GPU) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.dcgmURL, nil)
	if err != nil {
		return err
	}
	res, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024+1))
	if err != nil {
		return err
	}
	if len(body) > 2*1024*1024 {
		return errors.New("exporter response too large")
	}
	supplementDCGM(string(body), devices)
	return nil
}
func supplementDCGM(body string, devices []GPU) {
	for _, line := range strings.Split(body, "\n") {
		parts := strings.Fields(line)
		if len(parts) < 2 || strings.HasPrefix(parts[0], "#") {
			continue
		}
		v, err := strconv.ParseFloat(parts[1], 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1e15 {
			continue
		}
		for i := range devices {
			g := &devices[i]
			if !strings.Contains(parts[0], `UUID="`+g.UUID+`"`) {
				continue
			}
			switch strings.Split(parts[0], "{")[0] {
			case "DCGM_FI_DEV_XID_ERRORS":
				n := uint64(v)
				g.XIDErrors = &n
			case "DCGM_FI_DEV_ECC_DBE_VOL_TOTAL":
				n := uint64(v)
				g.ECCErrors = &n
			case "DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL": // Counter bytes are not a rate; leave rate unavailable.
			default:
				continue
			}
			if !strings.Contains(g.Source, "+dcgm") {
				g.Source += "+dcgm"
			}
		}
	}
}
