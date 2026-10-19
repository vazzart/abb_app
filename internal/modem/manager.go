package modem

import (
	"context"
	"io"
	"sync"
	"time"

	"go.bug.st/serial"
	"go.uber.org/zap"

	"abb/config"
)

// Manager discovers USB modems and runs one Worker per modem, restarting
// workers when a modem is unplugged and plugged back in.
type Manager struct {
	cfg     config.ModemConfig
	handler Handler
	log     *zap.Logger

	SysfsRoot string   // "/sys"; overridable in tests
	DevRoot   string   // "/dev"
	Open      OpenFunc // serial opener; overridable in tests

	// OnConnect / OnDisconnect are optional callbacks (e.g. Telegram notifications).
	OnConnect    func(Info)
	OnDisconnect func(Info, error)

	mu      sync.Mutex
	running map[string]bool      // USBPath -> worker running
	fails   map[string]int       // USBPath -> consecutive init failures
	retryAt map[string]time.Time // USBPath -> no init attempt before this time
	now     func() time.Time
}

// maxRetryDelay caps the backoff between init attempts (e.g. modem without a SIM).
const maxRetryDelay = 5 * time.Minute

func NewManager(cfg config.ModemConfig, handler Handler, log *zap.Logger) *Manager {
	return &Manager{
		cfg:       cfg,
		handler:   handler,
		log:       log.Named("modem"),
		SysfsRoot: "/sys",
		DevRoot:   "/dev",
		Open:      openSerial,
		running:   make(map[string]bool),
		fails:     make(map[string]int),
		retryAt:   make(map[string]time.Time),
		now:       time.Now,
	}
}

func openSerial(path string) (io.ReadWriteCloser, error) {
	return serial.Open(path, &serial.Mode{BaudRate: 115200})
}

// Run scans for modems every watch interval until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) {
	interval := m.cfg.WatchInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	m.log.Info("modem manager started", zap.Strings("usb_ids", m.cfg.USBIDs))

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var wg sync.WaitGroup
	defer wg.Wait()

	m.scan(ctx, &wg)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.scan(ctx, &wg)
		}
	}
}

func (m *Manager) scan(ctx context.Context, wg *sync.WaitGroup) {
	cands, err := Discover(m.SysfsRoot, m.DevRoot, m.cfg.USBIDs)
	if err != nil {
		m.log.Warn("modem discovery failed", zap.Error(err))
		return
	}
	present := make(map[string]bool, len(cands))
	for _, c := range cands {
		present[c.USBPath] = true
	}

	m.mu.Lock()
	// A replugged modem is retried right away, without waiting out the old backoff.
	for usb := range m.fails {
		if !present[usb] {
			delete(m.fails, usb)
			delete(m.retryAt, usb)
		}
	}
	m.mu.Unlock()

	for _, c := range cands {
		m.mu.Lock()
		skip := m.running[c.USBPath] || m.now().Before(m.retryAt[c.USBPath])
		if !skip {
			m.running[c.USBPath] = true
		}
		m.mu.Unlock()
		if skip {
			continue
		}

		wg.Add(1)
		go func(c Candidate) {
			defer wg.Done()
			defer func() {
				m.mu.Lock()
				delete(m.running, c.USBPath)
				m.mu.Unlock()
			}()
			m.runWorker(ctx, c)
		}(c)
	}
}

func (m *Manager) runWorker(ctx context.Context, c Candidate) {
	log := m.log.With(zap.String("usb", c.USBPath))
	log.Info("modem detected", zap.String("id", c.VIDPID), zap.Strings("ports", c.Ports))

	w := NewWorker(m.cfg, c, m.Open, m.handler, m.log)
	if err := w.Init(ctx); err != nil {
		if ctx.Err() == nil {
			delay := m.backoff(c.USBPath)
			log.Warn("modem init failed, will retry", zap.Duration("retry_in", delay), zap.Error(err))
		}
		return
	}
	m.mu.Lock()
	delete(m.fails, c.USBPath)
	delete(m.retryAt, c.USBPath)
	m.mu.Unlock()

	info := w.Info()
	log.Info("modem connected",
		zap.String("name", info.Name),
		zap.String("port", info.Port),
		zap.String("imei", info.IMEI),
		zap.String("iccid", info.ICCID),
		zap.String("number", info.Number),
		zap.String("operator", info.Operator),
	)
	if m.OnConnect != nil {
		m.OnConnect(info)
	}

	err := w.Run(ctx)
	if ctx.Err() != nil {
		return
	}
	log.Warn("modem disconnected", zap.String("name", info.Name), zap.Error(err))
	if m.OnDisconnect != nil {
		m.OnDisconnect(info, err)
	}
}

// backoff records an init failure and returns the delay before the next attempt:
// watch interval, doubled per consecutive failure, capped at maxRetryDelay.
func (m *Manager) backoff(usb string) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fails[usb]++
	delay := m.cfg.WatchInterval
	if delay <= 0 {
		delay = 10 * time.Second
	}
	for i := 1; i < m.fails[usb] && delay < maxRetryDelay; i++ {
		delay *= 2
	}
	delay = min(delay, maxRetryDelay)
	m.retryAt[usb] = m.now().Add(delay)
	return delay
}
