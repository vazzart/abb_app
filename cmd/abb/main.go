package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/electricbubble/gadb"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"

	"net/http"

	"abb/config"
	"abb/internal/adb"
	"abb/internal/db"
	"abb/internal/dispatcher"
	"abb/internal/metrics"
	"abb/internal/model"
	"abb/internal/sender"
)

func main() {
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	log := buildLogger(cfg.Log)
	defer func() { _ = log.Sync() }()

	database, err := db.Open(cfg.DB.Path)
	if err != nil {
		log.Fatal("open db", zap.Error(err))
	}
	defer func() { _ = database.Close() }()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if cfg.Metrics.Enabled {
		metrics.StartServer(ctx, cfg.Metrics.Addr, log)
	}

	adbClient, err := gadb.NewClientWith(cfg.ADB.ServerHost, cfg.ADB.ServerPort)
	if err != nil {
		log.Fatal("connect to ADB server", zap.Error(err))
	}

	senders := buildSenders(cfg, log)

	disp := dispatcher.New(database, senders, cfg.Dispatcher.DispatchInterval, 100*time.Millisecond, log)
	disp.SetHooks(&dispatcher.DeliveryHooks{
		OnSent:   func(ch string) { metrics.SMSSent.WithLabelValues(ch).Inc() },
		OnFailed: func(ch string) { metrics.SMSFailed.WithLabelValues(ch).Inc() },
	})
	go disp.Run(ctx)

	rw := dispatcher.NewRetryWorker(
		database, disp,
		cfg.Dispatcher.MaxAttempts,
		cfg.Dispatcher.RetryBaseInterval,
		cfg.Dispatcher.RetryBaseInterval,
		log,
	)
	go rw.Run(ctx)

	if pending, err := database.CountOutbox(ctx, "pending"); err == nil && pending > 0 {
		log.Info("pending outbox items from previous run", zap.Int("count", pending))
	}

	watcher := adb.NewWatcher(adbClient, cfg.ADB.DeviceSerial, cfg.ADB.WatchInterval, log)
	go watcher.Run(ctx)

	log.Info("waiting for device...",
		zap.String("adb_server", fmt.Sprintf("%s:%d", cfg.ADB.ServerHost, cfg.ADB.ServerPort)),
	)

	var pollerCancel context.CancelFunc

	for {
		select {
		case <-ctx.Done():
			if pollerCancel != nil {
				pollerCancel()
			}
			log.Info("shutdown complete")
			return

		case event := <-watcher.Events:
			switch event.State {
			case adb.Connected:
				devices, devErr := adbClient.DeviceList()
				var startID int64
				if devErr != nil {
					log.Warn("could not list devices for startup cursor", zap.Error(devErr))
				} else {
					for i := range devices {
						if devices[i].Serial() == event.Serial {
							id, err := adb.FetchMaxAndroidID(&devices[i])
							if err != nil {
								log.Warn("could not fetch device max android_id, starting from 0", zap.Error(err))
							} else {
								startID = id
							}
							break
						}
					}
				}
				log.Info("starting SMS poller",
					zap.String("serial", event.Serial),
					zap.Int64("last_android_id", startID),
				)
				var pollerCtx context.Context
				pollerCtx, pollerCancel = context.WithCancel(ctx)
				poller := adb.NewPoller(adbClient, event.Serial, cfg.ADB.PollInterval, startID, log)
				go poller.Run(pollerCtx)
				go processMessages(pollerCtx, poller, database, cfg.Channels, disp, log)

			case adb.Disconnected:
				log.Warn("device disconnected, stopping poller")
				if pollerCancel != nil {
					pollerCancel()
					pollerCancel = nil
				}
			}
		}
	}
}

// buildLogger creates a zap logger that writes to stderr or a rotating log file.
func buildLogger(cfg config.LogConfig) *zap.Logger {
	enc := zap.NewProductionEncoderConfig()
	enc.EncodeTime = zapcore.ISO8601TimeEncoder

	var ws zapcore.WriteSyncer
	if cfg.File != "" {
		ws = zapcore.AddSync(&lumberjack.Logger{
			Filename: cfg.File,
			MaxSize:  cfg.MaxSizeMB,
			MaxAge:   cfg.MaxAgeDays,
			Compress: cfg.Compress,
		})
	} else {
		ws = zapcore.AddSync(os.Stderr)
	}

	core := zapcore.NewCore(zapcore.NewJSONEncoder(enc), ws, zap.InfoLevel)
	return zap.New(core, zap.AddCaller())
}

func buildSenders(cfg *config.Config, log *zap.Logger) map[string]sender.Sender {
	senders := make(map[string]sender.Sender)

	if cfg.Telegram.Token != "" && cfg.Telegram.Token != "BOT_TOKEN" {
		tg, err := sender.NewTelegramSender(cfg.Telegram.Token, cfg.Telegram.ChatID, cfg.Telegram.Proxy)
		if err != nil {
			log.Error("failed to create telegram sender (check token / proxy)", zap.Error(err))
		} else {
			senders["telegram"] = tg
			log.Info("telegram sender ready")
		}
	} else {
		log.Warn("telegram token not configured, skipping telegram sender")
	}

	if cfg.Email.Enabled {
		senders["email"] = sender.NewEmailSender(cfg.Email, nil)
		log.Info("email sender ready", zap.String("to", cfg.Email.To))
	}

	if cfg.Ntfy.Enabled {
		senders["ntfy"] = sender.NewNtfySender(cfg.Ntfy, &http.Client{Timeout: 10 * time.Second})
		log.Info("ntfy sender ready",
			zap.String("server", cfg.Ntfy.ServerURL),
			zap.String("topic", cfg.Ntfy.Topic),
		)
	}

	return senders
}

func processMessages(
	ctx context.Context,
	poller *adb.Poller,
	database *db.DB,
	channels []string,
	disp *dispatcher.Dispatcher,
	log *zap.Logger,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-poller.Messages:
			saveAndEnqueue(ctx, msg, database, channels, disp, log)
		}
	}
}

func saveAndEnqueue(
	ctx context.Context,
	msg model.Message,
	database *db.DB,
	channels []string,
	disp *dispatcher.Dispatcher,
	log *zap.Logger,
) {
	id, isNew, err := database.SaveMessage(ctx, msg)
	if err != nil {
		log.Error("save message", zap.Error(err), zap.String("android_id", msg.AndroidID))
		return
	}
	if !isNew {
		return
	}

	metrics.SMSReceived.Inc()

	for _, ch := range channels {
		if err := database.CreateOutboxEntry(ctx, id, ch); err != nil {
			log.Error("create outbox entry", zap.Error(err), zap.String("channel", ch))
		}
	}
	go disp.DispatchOnce(ctx)

	fmt.Printf("[%s] %s: %s\n",
		msg.ReceivedAt.Format("2006-01-02 15:04:05"),
		msg.Address,
		msg.Body,
	)
}
