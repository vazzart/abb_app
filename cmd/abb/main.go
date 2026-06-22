package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
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
	"abb/internal/translator"
)

const version = "1.1.1"

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

	tr := buildTranslator(cfg, log)
	senders := buildSenders(cfg, log)

	sendStartupNotification(ctx, &adbClient, senders, log)

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
				var deviceName string
				var simInfo map[string]string
				if devErr != nil {
					log.Warn("could not list devices for startup cursor", zap.Error(devErr))
				} else {
					if dbID, err := database.GetMaxAndroidID(ctx); err == nil && dbID > startID {
						startID = dbID
					}
					for i := range devices {
						if devices[i].Serial() == event.Serial {
							id, err := adb.FetchMaxAndroidID(&devices[i])
							if err != nil {
								log.Warn("could not fetch device max android_id, starting from db", zap.Error(err))
							} else if id > startID {
								startID = id
							}
							deviceName = fetchDeviceName(&devices[i])
							si, err := adb.FetchSimInfo(&devices[i])
							if err != nil {
								log.Warn("could not fetch SIM info", zap.Error(err))
							} else {
								simInfo = si
								log.Info("loaded SIM info", zap.Any("sims", si))
							}
							break
						}
					}
				}
				log.Info("starting SMS poller",
					zap.String("serial", event.Serial),
					zap.String("device", deviceName),
					zap.Int64("last_android_id", startID),
				)
				var pollerCtx context.Context
				pollerCtx, pollerCancel = context.WithCancel(ctx)
				poller := adb.NewPoller(adbClient, event.Serial, deviceName, cfg.ADB.PollInterval, startID, simInfo, log)
				go poller.Run(pollerCtx)
				go processMessages(pollerCtx, poller, database, cfg.Channels, disp, tr, log)

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
	tr *translator.Yandex,
	log *zap.Logger,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-poller.Messages:
			saveAndEnqueue(ctx, msg, database, channels, disp, tr, log)
		}
	}
}

func saveAndEnqueue(
	ctx context.Context,
	msg model.Message,
	database *db.DB,
	channels []string,
	disp *dispatcher.Dispatcher,
	tr *translator.Yandex,
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

	if tr != nil {
		translated, lang, err := tr.Translate(ctx, msg.Body)
		if err != nil {
			log.Warn("translate failed", zap.Error(err))
		} else if lang != tr.TargetLang() {
			if err := database.UpdateTranslation(ctx, id, translated); err != nil {
				log.Error("update translation", zap.Error(err))
			} else {
				log.Info("translated message",
					zap.String("from", lang),
					zap.String("to", tr.TargetLang()),
				)
			}
		}
	}

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

func buildTranslator(cfg *config.Config, log *zap.Logger) *translator.Yandex {
	if !cfg.Translate.Enabled {
		return nil
	}
	if cfg.Translate.APIKey == "" || cfg.Translate.FolderID == "" {
		log.Warn("translate enabled but api_key or folder_id is missing, skipping")
		return nil
	}
	log.Info("yandex translate enabled", zap.String("target_lang", cfg.Translate.TargetLang))
	return translator.NewYandex(cfg.Translate.APIKey, cfg.Translate.FolderID, cfg.Translate.TargetLang)
}

func fetchDeviceName(device *gadb.Device) string {
	out, err := device.RunShellCommand("getprop ro.product.model")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func sendStartupNotification(ctx context.Context, adbClient *gadb.Client, senders map[string]sender.Sender, log *zap.Logger) {
	devices, err := adbClient.DeviceList()

	connected := 0
	var lines []string
	if err != nil {
		log.Warn("startup notification: could not list devices", zap.Error(err))
	} else {
		for i := range devices {
			connected++
			serial := devices[i].Serial()
			name := fetchDeviceName(&devices[i])
			if name != "" {
				lines = append(lines, fmt.Sprintf("  %s (%s)", name, serial))
			} else {
				lines = append(lines, fmt.Sprintf("  %s", serial))
			}
		}
	}

	body := fmt.Sprintf("Started. Version: %s\nDevices connected: %d", version, connected)
	if len(lines) > 0 {
		body += "\n" + strings.Join(lines, "\n")
	}

	msg := model.Message{Address: "ABB", Body: body, ReceivedAt: time.Now()}
	for name, s := range senders {
		if err := s.Send(ctx, msg); err != nil {
			log.Warn("startup notification send failed", zap.String("channel", name), zap.Error(err))
		}
	}
}
