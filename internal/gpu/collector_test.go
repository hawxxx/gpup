package gpu

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseSMI(t *testing.T) {
	devices, err := parseSMI("0, GPU-abc, NVIDIA Test, 47, 1024, 8192, 12, 55, 120.5, 250, 1500, 5000\n1, GPU-def, Other, [N/A], [Not Supported], 4096, N/A, 40, N/A, N/A, N/A, N/A\n")
	if err != nil || len(devices) != 2 {
		t.Fatalf("devices=%v err=%v", devices, err)
	}
	if *devices[0].PowerW != 120.5 || *devices[0].MemoryUsedMB != 1024 {
		t.Fatal("incorrect units")
	}
	if devices[1].Utilization != nil || devices[1].MemoryUsedMB != nil {
		t.Fatal("unsupported values must remain unavailable")
	}
}

func TestDCGMSupplement(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# fixture\nDCGM_FI_DEV_XID_ERRORS{UUID=\"GPU-abc\"} 31\nDCGM_FI_DEV_ECC_DBE_VOL_TOTAL{UUID=\"GPU-abc\"} NaN\nDCGM_FI_DEV_XID_ERRORS{UUID=\"GPU-other\"} 99\n"))
	}))
	defer server.Close()
	c := &Collector{dcgmURL: server.URL, runSMI: func(context.Context) ([]GPU, error) { return []GPU{{UUID: "GPU-abc", Source: "nvidia-smi"}}, nil }}
	got, err := c.Collect(context.Background())
	if err != nil || got[0].XIDErrors == nil || *got[0].XIDErrors != 31 || got[0].ECCErrors != nil {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestCanceledSMI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := collectSMI(ctx); err == nil {
		t.Fatal("canceled command succeeded")
	}
}

func TestMalformedSMI(t *testing.T) {
	if _, err := parseSMI("invalid,row\n"); err == nil {
		t.Fatal("malformed input accepted")
	}
}

func TestMissingProvider(t *testing.T) {
	c := &Collector{runSMI: func(context.Context) ([]GPU, error) { return nil, errUnavailable }}
	got, err := c.Collect(context.Background())
	if err == nil || got == nil || len(got) != 0 {
		t.Fatalf("got=%v err=%v", got, err)
	}
}
