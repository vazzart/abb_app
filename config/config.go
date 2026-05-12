package config

import (
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	ADB        ADBConfig
	DB         DBConfig
	Telegram   TelegramConfig
	Email      EmailConfig
	Dispatcher DispatcherConfig
	Metrics    MetricsConfig
	Log        LogConfig
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
	v.SetDefault("db.path", "./abb.db")
	v.SetDefault("dispatcher.max_attempts", 5)
	v.SetDefault("dispatcher.retry_base_interval", "1m")
	v.SetDefault("dispatcher.dispatch_interval", "10s")
	v.SetDefault("email.enabled", false)
	v.SetDefault("email.port", 587)
	v.SetDefault("metrics.enabled", false)
	v.SetDefault("metrics.addr", ":9090")
	v.SetDefault("log.max_size_mb", 100)
	v.SetDefault("log.max_age_days", 7)
	v.SetDefault("log.compress", true)

	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	return &Config{
		ADB: ADBConfig{
			ServerHost:    v.GetString("adb.server_host"),
			ServerPort:    v.GetInt("adb.server_port"),
			PollInterval:  v.GetDuration("adb.poll_interval"),
			WatchInterval: v.GetDuration("adb.watch_interval"),
			DeviceSerial:  v.GetString("adb.device_serial"),
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
		Channels: v.GetStringSlice("channels"),
	}, nil
}
