package modem

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Candidate is one physical USB modem and the serial ports it exposes.
type Candidate struct {
	USBPath string   // sysfs name of the USB device, e.g. "1-1.2"; stable per physical port
	VIDPID  string   // "1e0e:9011"
	Ports   []string // device paths ordered by USB interface number, e.g. /dev/ttyUSB0..4
}

// Discover lists USB serial ports under sysfsRoot (normally "/sys") and groups
// them by parent USB device. Only devices whose "vid:pid" is in usbIDs are returned.
// devRoot is the directory holding the tty nodes (normally "/dev").
func Discover(sysfsRoot, devRoot string, usbIDs []string) ([]Candidate, error) {
	allowed := make(map[string]bool, len(usbIDs))
	for _, id := range usbIDs {
		allowed[strings.ToLower(strings.TrimSpace(id))] = true
	}

	entries, err := os.ReadDir(filepath.Join(sysfsRoot, "class", "tty"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	type port struct {
		dev   string
		iface int
	}
	groups := make(map[string]*Candidate)
	ports := make(map[string][]port)

	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "ttyUSB") && !strings.HasPrefix(name, "ttyACM") {
			continue
		}
		devPath, err := filepath.EvalSymlinks(filepath.Join(sysfsRoot, "class", "tty", name, "device"))
		if err != nil {
			continue
		}
		usbDir, iface, ok := findUSBDevice(devPath)
		if !ok {
			continue
		}
		vidpid := readAttr(usbDir, "idVendor") + ":" + readAttr(usbDir, "idProduct")
		if !allowed[vidpid] {
			continue
		}
		key := filepath.Base(usbDir)
		if groups[key] == nil {
			groups[key] = &Candidate{USBPath: key, VIDPID: vidpid}
		}
		ports[key] = append(ports[key], port{dev: filepath.Join(devRoot, name), iface: iface})
	}

	result := make([]Candidate, 0, len(groups))
	for key, c := range groups {
		ps := ports[key]
		sort.Slice(ps, func(i, j int) bool { return ps[i].iface < ps[j].iface })
		for _, p := range ps {
			c.Ports = append(c.Ports, p.dev)
		}
		result = append(result, *c)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].USBPath < result[j].USBPath })
	return result, nil
}

// findUSBDevice walks up from a tty's device directory to the USB device
// (the first ancestor with an idVendor attribute). It also returns the USB
// interface number found on the way (-1 if none).
func findUSBDevice(devPath string) (string, int, bool) {
	iface := -1
	dir := devPath
	for i := 0; i < 6; i++ {
		if iface < 0 {
			if v := readAttr(dir, "bInterfaceNumber"); v != "" {
				if n, err := strconv.ParseInt(v, 16, 32); err == nil {
					iface = int(n)
				}
			}
		}
		if readAttr(dir, "idVendor") != "" {
			return dir, iface, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", -1, false
}

func readAttr(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(string(b)))
}
