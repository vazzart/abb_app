package modem

import (
	"os"
	"path/filepath"
	"testing"
)

// buildSysfs creates a fake /sys with two A7670E modems (5 ttys each) and one
// unrelated USB serial adapter.
func buildSysfs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, name string) {
		dir := filepath.Join(root, "class", "tty", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "device")); err != nil {
			t.Skipf("symlinks not supported here: %v", err)
		}
	}
	addDevice := func(usb, vid, pid string, firstTTY int, ifaces []int) {
		dev := filepath.Join(root, "devices", "platform", "usb1", usb)
		write(filepath.Join(dev, "idVendor"), vid)
		write(filepath.Join(dev, "idProduct"), pid)
		for i, iface := range ifaces {
			// Real sysfs names are "1-1.2:1.4"; ':' is not allowed on Windows and the name is irrelevant.
			intf := filepath.Join(dev, usb+"_1."+string(rune('0'+iface)))
			write(filepath.Join(intf, "bInterfaceNumber"), "0"+string(rune('0'+iface)))
			name := "ttyUSB" + string(rune('0'+firstTTY+i))
			port := filepath.Join(intf, name)
			if err := os.MkdirAll(port, 0o755); err != nil {
				t.Fatal(err)
			}
			link(port, name)
		}
	}

	// Interfaces listed out of order to check sorting by interface number.
	addDevice("1-1.2", "1e0e", "9011", 0, []int{6, 2, 3, 4, 5})
	addDevice("1-1.3", "1e0e", "9011", 5, []int{2, 3, 4})
	addDevice("1-1.4", "0403", "6001", 8, []int{0}) // FTDI adapter
	return root
}

func TestDiscover_GroupsPortsPerModem(t *testing.T) {
	root := buildSysfs(t)
	cands, err := Discover(root, "/dev", []string{"1e0e:9011"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 {
		t.Fatalf("got %d candidates, want 2: %+v", len(cands), cands)
	}

	first := cands[0]
	if first.USBPath != "1-1.2" || first.VIDPID != "1e0e:9011" {
		t.Errorf("first = %+v", first)
	}
	want := []string{"/dev/ttyUSB1", "/dev/ttyUSB2", "/dev/ttyUSB3", "/dev/ttyUSB4", "/dev/ttyUSB0"}
	if len(first.Ports) != len(want) {
		t.Fatalf("ports = %v, want %v", first.Ports, want)
	}
	for i := range want {
		if filepath.ToSlash(first.Ports[i]) != want[i] {
			t.Errorf("ports = %v, want %v (sorted by interface)", first.Ports, want)
			break
		}
	}
	if len(cands[1].Ports) != 3 {
		t.Errorf("second modem ports = %v", cands[1].Ports)
	}
}

func TestDiscover_NoSysfs(t *testing.T) {
	cands, err := Discover(filepath.Join(t.TempDir(), "missing"), "/dev", []string{"1e0e:9011"})
	if err != nil || len(cands) != 0 {
		t.Fatalf("got %v, %v", cands, err)
	}
}
