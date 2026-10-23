// Command metrics-provider publishes metrics over io.systemd.Metrics, the
// interface systemd v260 services use under /run/systemd/report/. It is
// the server-side counterpart of examples/metrics, and shows how to serve
// a systemd interface with the generated types: the handler signatures use
// metrics.ListOut and metrics.DescribeOut, and the IDL comes from
// metrics.InterfaceDescription.
//
//	go run ./examples/metrics-provider -socket /tmp/demo.metrics
//	go run ./examples/metrics /tmp/demo.metrics
//	varlinkctl call --more /tmp/demo.metrics io.systemd.Metrics.List '{}'
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rossedman/sdvarlink/systemd/v260/metrics"
	"github.com/rossedman/sdvarlink/varlink"
)

// family describes one metric family and how to read its values.
type family struct {
	name, description string
	kind              metrics.MetricFamilyType
	read              func() []sample
}

type sample struct {
	object string // optional: what the value is about
	value  any
}

func main() {
	socket := flag.String("socket", "/run/systemd/report/io.example.MetricsDemo", "socket path when not socket-activated")
	flag.Parse()

	start := time.Now()
	var calls atomic.Int64
	families := []family{
		{"io.example.MetricsDemo.goroutines", "Goroutines in the provider process", metrics.MetricFamilyTypeGauge,
			func() []sample { return []sample{{value: runtime.NumGoroutine()}} }},
		{"io.example.MetricsDemo.uptimeSeconds", "Seconds since the provider started", metrics.MetricFamilyTypeCounter,
			func() []sample { return []sample{{value: int64(time.Since(start).Seconds())}} }},
		{"io.example.MetricsDemo.listCalls", "List calls served", metrics.MetricFamilyTypeCounter,
			func() []sample { return []sample{{value: calls.Load()}} }},
		{"io.example.MetricsDemo.build", "Build information", metrics.MetricFamilyTypeString,
			func() []sample { return []sample{{object: "go", value: runtime.Version()}} }},
	}

	srv := varlink.NewServer(varlink.ServerInfo{
		Vendor: "sdvarlink examples", Product: "metrics-provider", Version: "1.0.0",
		URL: "https://github.com/rossedman/sdvarlink/tree/main/examples/metrics-provider",
	})
	iface := srv.Interface(metrics.InterfaceName, metrics.InterfaceDescription)

	// List and Describe return finite lists, so the last value is the
	// final reply (LastIsFinal), as systemd's own providers do.
	varlink.HandleStream(iface, "List", func(ctx context.Context, call *varlink.Call, _ struct{}, send func(metrics.ListOut) error) error {
		calls.Add(1)
		for _, f := range families {
			for _, s := range f.read() {
				v, err := json.Marshal(s.value)
				if err != nil {
					return err
				}
				out := metrics.ListOut{Name: f.name, Value: v}
				if s.object != "" {
					out.Object = varlink.Ptr(s.object)
				}
				if err := send(out); err != nil {
					return err
				}
			}
		}
		return nil
	}, varlink.LastIsFinal())

	varlink.HandleStream(iface, "Describe", func(ctx context.Context, call *varlink.Call, _ struct{}, send func(metrics.DescribeOut) error) error {
		for _, f := range families {
			if err := send(metrics.DescribeOut{Name: f.name, Description: f.description, Type: f.kind}); err != nil {
				return err
			}
		}
		return nil
	}, varlink.LastIsFinal())

	l, err := varlink.ListenActivated("")
	if errors.Is(err, varlink.ErrNotActivated) {
		l, err = varlink.Listen(*socket, varlink.WithSocketMode(0o666))
	}
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("metrics-provider: serving %s on %s", metrics.InterfaceName, l.Addr())
	if err := srv.Serve(ctx, l); err != nil {
		log.Fatal(err)
	}
}
