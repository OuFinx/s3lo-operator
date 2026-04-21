package proxy

import "github.com/prometheus/client_golang/prometheus"

// Metrics holds Prometheus counters for the OCI proxy.
type Metrics struct {
	ManifestRequests *prometheus.CounterVec // labels: source="cache"|"s3"|"error"
	BlobRequests     *prometheus.CounterVec // labels: status="redirect"|"error"
	S3Requests       *prometheus.CounterVec // labels: op="manifest_get"|"blob_head"|"blob_presign"
}

// NewMetrics registers and returns proxy metrics using the given registerer.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		ManifestRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "s3lo_manifest_requests_total",
			Help: "Manifest requests by cache source (cache/s3/error).",
		}, []string{"source"}),
		BlobRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "s3lo_blob_requests_total",
			Help: "Blob requests by outcome (redirect/error).",
		}, []string{"status"}),
		S3Requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "s3lo_s3_requests_total",
			Help: "Outbound S3 API calls by operation.",
		}, []string{"op"}),
	}
	reg.MustRegister(m.ManifestRequests, m.BlobRequests, m.S3Requests)
	return m
}

func (m *Metrics) incManifest(source string) {
	if m != nil {
		m.ManifestRequests.WithLabelValues(source).Inc()
	}
}

func (m *Metrics) incBlob(status string) {
	if m != nil {
		m.BlobRequests.WithLabelValues(status).Inc()
	}
}

func (m *Metrics) incS3(op string) {
	if m != nil {
		m.S3Requests.WithLabelValues(op).Inc()
	}
}
