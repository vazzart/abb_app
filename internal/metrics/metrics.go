package metrics

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

var (
	SMSReceived = promauto.NewCounter(prometheus.CounterOpts{
		Name: "abb_sms_received_total",
		Help: "New SMS messages read from Android and saved to the database.",
	})
	SMSSent = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "abb_sms_sent_total",
		Help: "Outbox items successfully delivered, labelled by channel.",
	}, []string{"channel"})
	SMSFailed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "abb_sms_failed_total",
		Help: "Outbox delivery attempts that failed, labelled by channel.",
	}, []string{"channel"})
)

// StartServer starts the Prometheus /metrics HTTP server and shuts it down when ctx is cancelled.
func StartServer(ctx context.Context, addr string, log *zap.Logger) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Error("metrics server: listen failed", zap.Error(err))
		return
	}

	go func() {
		log.Info("metrics server listening", zap.String("addr", addr))
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Error("metrics server error", zap.Error(err))
		}
	}()

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx) //nolint:errcheck
	}()
}
