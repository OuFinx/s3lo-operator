package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestMetrics_NilSafe(t *testing.T) {
	var m *Metrics
	// None of these must panic.
	m.incManifest("cache")
	m.incBlob("redirect")
	m.incS3("manifest_get")
}

func TestNewMetrics_RegistersCounters(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	m.incManifest("s3")
	m.incBlob("redirect")
	m.incS3("manifest_get")

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if len(families) != 3 {
		t.Errorf("expected 3 metric families, got %d", len(families))
	}
}

func TestHandleManifest_MetricsIncrement(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	s := newFakeStorage()
	s.put("my-bucket", "manifests/myapp/v1.0/manifest.json", singleManifestJSON)
	h := NewHandlers(s, time.Hour)
	h.metrics = m

	req := httptest.NewRequest("GET", "/v2/my-bucket/myapp/manifests/v1.0", nil)
	rec := httptest.NewRecorder()
	h.HandleManifest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var total float64
	for _, f := range families {
		if f.GetName() == "s3lo_manifest_requests_total" {
			for _, metric := range f.GetMetric() {
				total += metric.GetCounter().GetValue()
			}
		}
	}
	if total != 1 {
		t.Errorf("s3lo_manifest_requests_total = %v, want 1", total)
	}
}
