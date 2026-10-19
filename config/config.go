package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	ADB        ADBConfig
	Modem      ModemConfig
	DB         DBConfig
	Telegram   TelegramConfig
	Email      EmailConfig
	Ntfy       NtfyConfig
	Dispatcher DispatcherConfig
	Metrics    MetricsConfig
	Log        LogConfig
	Translate  TranslateConfig
	Channels   []string
}

type DBConfig struct {
	Path string
}

type ADBConfig struct {
	ServerHost    string
	ServerPort    int
	PollInterval  time.Duration
	WatchInterval time.Duration
	DeviceSerial  string
}

// ModemConfig configures USB GSM modems (e.g. SIMCOM A7670E) read via AT commands.
type ModemConfig struct {
	Enabled         bool
	USBIDs          []string // "vid:pid" pairs, lowercase hex
	WatchInterval   time.Duration
	PollInterval    time.Duration
	Storage         string // SMS storage: "SM" (SIM) or "ME" (modem memory)
	DeleteAfterRead bool
	Labels          []ModemLabel
}

// ModemLabel names a modem in notifications. Matched by IMEI or ICCID.
type ModemLabel struct {
	IMEI  string `mapstructure:"imei"`
	ICCID string `mapstructure:"iccid"`
	Name  string `mapstructure:"name"`
}

type TelegramConfig struct {
	Token  string
	ChatID int64
	Proxy  ProxyConfig
}

type ProxyConfig struct {
	Enabled  bool
	Address  string
	Username string
	Password string
}

type EmailConfig struct {
	Enabled  bool
	Host     string
	Port     int
	Username string
	Password string
	From     string
	To       string
}

type TranslateConfig struct {
	Enabled    bool
	APIKey     string
	FolderID   string
	TargetLang string
}

type NtfyConfig struct {
	Enabled   bool
	ServerURL string // e.g. "https://ntfy.example.com"
	Topic     string
	Token     string // optional Bearer token
	Priority  string // "default", "low", "high", "urgent", "max"
}

type MetricsConfig struct {
	Enabled bool
	Addr    string
}

type LogConfig struct {
	File       string // empty = stderr (journald under systemd)
	MaxSizeMB  int
	MaxAgeDays int
	Compress   bool
}

type DispatcherConfig struct {
	MaxAttempts       int
	RetryBaseInterval time.Duration // base backoff for exponential retry
	DispatchInterval  time.Duration // how often the dispatcher sweeps pending outbox
}

func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)

	v.SetDefault("adb.server_host", "localhost")
	v.SetDefault("adb.server_port", 5037)
	v.SetDefault("adb.poll_interval", "30s")
	v.SetDefault("adb.watch_interval", "5s")
	v.SetDefault("modem.enabled", false)
	v.SetDefault("modem.usb_ids", []string{"1e0e:9011", "1e0e:9001"})
	v.SetDefault("modem.watch_interval", "10s")
	v.SetDefault("modem.poll_interval", "30s")
	v.SetDefault("modem.storage", "SM")
	v.SetDefault("modem.delete_after_read", true)
	v.SetDefault("db.path", "./abb.db")
	v.SetDefault("dispatcher.max_attempts", 5)
	v.SetDefault("dispatcher.retry_base_interval", "1m")
	v.SetDefault("dispatcher.dispatch_interval", "10s")
	v.SetDefault("email.enabled", false)
	v.SetDefault("email.port", 587)
	v.SetDefault("translate.enabled", false)
	v.SetDefault("translate.target_lang", "ru")
	v.SetDefault("ntfy.enabled", false)
	v.SetDefault("ntfy.priority", "default")
	v.SetDefault("metrics.enabled", false)
	v.SetDefault("metrics.addr", ":9090")
	v.SetDefault("log.max_size_mb", 100)
	v.SetDefault("log.max_age_days", 7)
	v.SetDefault("log.compress", true)

	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	var labels []ModemLabel
	if err := v.UnmarshalKey("modem.labels", &labels); err != nil {
		return nil, fmt.Errorf("modem.labels: %w", err)
	}

	return &Config{
		ADB: ADBConfig{
			ServerHost:    v.GetString("adb.server_host"),
			ServerPort:    v.GetInt("adb.server_port"),
			PollInterval:  v.GetDuration("adb.poll_interval"),
			WatchInterval: v.GetDuration("adb.watch_interval"),
			DeviceSerial:  v.GetString("adb.device_serial"),
		},
		Modem: ModemConfig{
			Enabled:         v.GetBool("modem.enabled"),
			USBIDs:          v.GetStringSlice("modem.usb_ids"),
			WatchInterval:   v.GetDuration("modem.watch_interval"),
			PollInterval:    v.GetDuration("modem.poll_interval"),
			Storage:         v.GetString("modem.storage"),
			DeleteAfterRead: v.GetBool("modem.delete_after_read"),
			Labels:          labels,
		},
		DB: DBConfig{
			Path: v.GetString("db.path"),
		},
		Telegram: TelegramConfig{
			Token:  v.GetString("telegram.token"),
			ChatID: v.GetInt64("telegram.chat_id"),
			Proxy: ProxyConfig{
				Enabled:  v.GetBool("telegram.proxy.enabled"),
				Address:  v.GetString("telegram.proxy.address"),
				Username: v.GetString("telegram.proxy.username"),
				Password: v.GetString("telegram.proxy.password"),
			},
		},
		Email: EmailConfig{
			Enabled:  v.GetBool("email.enabled"),
			Host:     v.GetString("email.host"),
			Port:     v.GetInt("email.port"),
			Username: v.GetString("email.username"),
			Password: v.GetString("email.password"),
			From:     v.GetString("email.from"),
			To:       v.GetString("email.to"),
		},
		Ntfy: NtfyConfig{
			Enabled:   v.GetBool("ntfy.enabled"),
			ServerURL: v.GetString("ntfy.server_url"),
			Topic:     v.GetString("ntfy.topic"),
			Token:     v.GetString("ntfy.token"),
			Priority:  v.GetString("ntfy.priority"),
		},
		Dispatcher: DispatcherConfig{
			MaxAttempts:       v.GetInt("dispatcher.max_attempts"),
			RetryBaseInterval: v.GetDuration("dispatcher.retry_base_interval"),
			DispatchInterval:  v.GetDuration("dispatcher.dispatch_interval"),
		},
		Metrics: MetricsConfig{
			Enabled: v.GetBool("metrics.enabled"),
			Addr:    v.GetString("metrics.addr"),
		},
		Log: LogConfig{
			File:       v.GetString("log.file"),
			MaxSizeMB:  v.GetInt("log.max_size_mb"),
			MaxAgeDays: v.GetInt("log.max_age_days"),
			Compress:   v.GetBool("log.compress"),
		},
		Translate: TranslateConfig{
			Enabled:    v.GetBool("translate.enabled"),
			APIKey:     v.GetString("translate.api_key"),
			FolderID:   v.GetString("translate.folder_id"),
			TargetLang: v.GetString("translate.target_lang"),
		},
		Channels: v.GetStringSlice("channels"),
	}, nil
}
