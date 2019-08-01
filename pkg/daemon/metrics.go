package daemon

import (
	"net/http"

	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var DefaultBindAddress = ":8080"

func StartMetricsListener(addr string) {
	if addr == "" {
		addr = DefaultBindAddress
	}

	glog.Infof("Starting metrics listener on %s", addr)
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	if err := http.ListenAndServe(addr, mux); err != nil {
		glog.Exitf("Unable to start metrics listener: %v", err)
	}
}
