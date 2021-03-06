package main

import (
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/alecthomas/kingpin"
	"github.com/domainr/whois"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/log"
)

var (
	bind    = kingpin.Flag("bind", "addr to bind the server").Short('b').Default(":9222").String()
	debug   = kingpin.Flag("debug", "show debug logs").Default("false").Bool()
	version = "master"

	re = regexp.MustCompile(`(?i)(Registry Expiry Date|paid-till|Expiration Date|Expiration Time|Expiry.*|expires.*):\s?(.*)`)

	formats = []string{
		time.ANSIC,
		time.UnixDate,
		time.RubyDate,
		time.RFC822,
		time.RFC822Z,
		time.RFC850,
		time.RFC1123,
		time.RFC1123Z,
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02 15:04:05", // .cn time format
		"02-May-2006 15:04:05 UTC", // .in time format
		"20060102", // lol registro.br
	}
)

func main() {
	kingpin.Version("domain_exporter version " + version)
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()

	if *debug {
		_ = log.Base().SetLevel("debug")
		log.Debug("enabled debug mode")
	}

	log.Info("starting domain_exporter", version)

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/probe", probeHandler)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(
			w, `
			<html>
			<head><title>Domain Exporter</title></head>
			<body>
				<h1>Domain Exporter</h1>
				<p><a href="/metrics">Metrics</a></p>
				<p><a href="/probe?target=google.com">probe google.com</a></p>
			</body>
			</html>
			`,
		)
	})
	log.Info("listening on", *bind)
	if err := http.ListenAndServe(*bind, nil); err != nil {
		log.Fatalf("error starting server: %s", err)
	}
}

func probeHandler(w http.ResponseWriter, r *http.Request) {
	var params = r.URL.Query()
	var target = params.Get("target")
	var registry = prometheus.NewRegistry()
	var start = time.Now()
	var log = log.With("domain", target)
	var expiryGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "domain_expiry_days",
		Help: "time in days until the domain expires",
	})
	var probeDurationGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "probe_duration_seconds",
		Help: "Returns how long the probe took to complete in seconds",
	})
	registry.MustRegister(expiryGauge)
	registry.MustRegister(probeDurationGauge)
	if target == "" {
		http.Error(w, "target parameter is missing", http.StatusBadRequest)
		return
	}
	req, err := whois.NewRequest(target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := whois.DefaultClient.Fetch(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var result = re.FindStringSubmatch(resp.String())
	if len(result) < 2 {
		http.Error(
			w,
			fmt.Sprintf("couldnt parse whois for domain: %s", target),
			http.StatusInternalServerError,
		)
		return
	}
	var dateStr = strings.TrimSpace(result[2])
	for _, format := range formats {
		if date, err := time.Parse(format, dateStr); err == nil {
			var days = math.Floor(time.Until(date).Hours() / 24)
			log.With("days", days).With("date", date).Debug("got info")
			expiryGauge.Set(days)
			probeDurationGauge.Set(time.Since(start).Seconds())
			promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(w, r)
			return
		}
	}
	http.Error(
		w,
		fmt.Sprintf("couldnt parse date from whois of domain: %s", target),
		http.StatusInternalServerError,
	)
}
