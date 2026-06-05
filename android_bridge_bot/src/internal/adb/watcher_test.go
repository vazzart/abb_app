package adb_test

import (
	"testing"

	"abb/internal/adb"
)

// T1-01 / T1-02: устройство найдено (любой serial)
func TestPickOnlineDevice_AnyOnline(t *testing.T) {
	statuses := []adb.DeviceStatus{{Serial: "1997fb4b", Online: true}}

	serial, ok := adb.PickOnlineDevice(statuses, "")
	if !ok {
		t.Fatal("expected device to be found")
	}
	if serial != "1997fb4b" {
		t.Errorf("serial: got %q, want %q", serial, "1997fb4b")
	}
}

// unauthorized device (Online=false) — не считается онлайн
func TestPickOnlineDevice_Unauthorized(t *testing.T) {
	statuses := []adb.DeviceStatus{{Serial: "1997fb4b", Online: false}}

	_, ok := adb.PickOnlineDevice(statuses, "")
	if ok {
		t.Fatal("offline/unauthorized device must not be reported as online")
	}
}

// T1-03: телефон не подключён — пустой список
func TestPickOnlineDevice_NoDevices(t *testing.T) {
	_, ok := adb.PickOnlineDevice(nil, "")
	if ok {
		t.Fatal("expected no device")
	}
}

// Пустой список DeviceStatus — не паникует
func TestPickOnlineDevice_EmptySlice(t *testing.T) {
	_, ok := adb.PickOnlineDevice([]adb.DeviceStatus{}, "")
	if ok {
		t.Fatal("expected no device on empty slice")
	}
}

// Поиск по конкретному serial — совпадает
func TestPickOnlineDevice_SerialMatch(t *testing.T) {
	statuses := []adb.DeviceStatus{
		{Serial: "aaa111", Online: true},
		{Serial: "bbb222", Online: true},
	}

	serial, ok := adb.PickOnlineDevice(statuses, "bbb222")
	if !ok {
		t.Fatal("expected device to be found")
	}
	if serial != "bbb222" {
		t.Errorf("serial: got %q, want %q", serial, "bbb222")
	}
}

// Поиск по конкретному serial — не совпадает
func TestPickOnlineDevice_SerialNoMatch(t *testing.T) {
	statuses := []adb.DeviceStatus{{Serial: "aaa111", Online: true}}

	_, ok := adb.PickOnlineDevice(statuses, "zzz999")
	if ok {
		t.Fatal("expected no match for unknown serial")
	}
}

// Несколько устройств без указания serial — возвращает первое онлайн
func TestPickOnlineDevice_MultipleDevices_NoPreference(t *testing.T) {
	statuses := []adb.DeviceStatus{
		{Serial: "aaa111", Online: true},
		{Serial: "bbb222", Online: true},
	}

	serial, ok := adb.PickOnlineDevice(statuses, "")
	if !ok {
		t.Fatal("expected a device")
	}
	if serial != "aaa111" {
		t.Errorf("expected first device aaa111, got %q", serial)
	}
}

// Оффлайн-устройства пропускаются, онлайн-устройство возвращается
func TestPickOnlineDevice_SkipsOffline(t *testing.T) {
	statuses := []adb.DeviceStatus{
		{Serial: "offline1", Online: false},
		{Serial: "online1", Online: true},
	}

	serial, ok := adb.PickOnlineDevice(statuses, "")
	if !ok {
		t.Fatal("expected online device")
	}
	if serial != "online1" {
		t.Errorf("serial: got %q, want %q", serial, "online1")
	}
}
