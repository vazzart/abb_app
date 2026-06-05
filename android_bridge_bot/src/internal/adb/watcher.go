package adb

import (
	"context"
	"time"

	"github.com/electricbubble/gadb"
	"go.uber.org/zap"
)

type DeviceState int

const (
	Disconnected DeviceState = iota
	Connected
)

type DeviceEvent struct {
	Serial string
	State  DeviceState
}

// DeviceStatus is an observable snapshot of one ADB device, used for selection logic.
// Exported so tests can construct it without a real ADB server.
type DeviceStatus struct {
	Serial string
	Online bool
}

// PickOnlineDevice returns the serial of the first online device that matches
// preferredSerial, or any online device when preferredSerial is empty.
// Exported for unit testing.
func PickOnlineDevice(statuses []DeviceStatus, preferredSerial string) (string, bool) {
	for _, s := range statuses {
		if !s.Online {
			continue
		}
		if preferredSerial == "" || preferredSerial == s.Serial {
			return s.Serial, true
		}
	}
	return "", false
}

// Watcher polls the ADB server for connected devices and emits DeviceEvents.
type Watcher struct {
	client   gadb.Client
	serial   string
	interval time.Duration
	state    DeviceState
	log      *zap.Logger
	Events   chan DeviceEvent
}

func NewWatcher(client gadb.Client, serial string, interval time.Duration, log *zap.Logger) *Watcher {
	return &Watcher{
		client:   client,
		serial:   serial,
		interval: interval,
		state:    Disconnected,
		log:      log,
		Events:   make(chan DeviceEvent, 4),
	}
}

func (w *Watcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	w.check()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.check()
		}
	}
}

func (w *Watcher) check() {
	devices, err := w.client.DeviceList()
	if err != nil {
		w.log.Warn("adb DeviceList failed", zap.Error(err))
		return
	}

	statuses := toDeviceStatuses(devices)
	serial, online := PickOnlineDevice(statuses, w.serial)

	switch {
	case online && w.state == Disconnected:
		w.state = Connected
		w.log.Info("device connected", zap.String("serial", serial))
		w.Events <- DeviceEvent{Serial: serial, State: Connected}
	case !online && w.state == Connected:
		w.state = Disconnected
		w.log.Info("device disconnected")
		w.Events <- DeviceEvent{State: Disconnected}
	}
}

func toDeviceStatuses(devices []gadb.Device) []DeviceStatus {
	statuses := make([]DeviceStatus, 0, len(devices))
	for _, d := range devices {
		state, err := d.State()
		statuses = append(statuses, DeviceStatus{
			Serial: d.Serial(),
			Online: err == nil && state == gadb.StateOnline,
		})
	}
	return statuses
}
