package field

import (
	"os"
	"strings"
)

// DeviceIDPrefix tags every device ID derived from a hardware serial (DESIGN §3).
const DeviceIDPrefix = "e6-"

// DeviceID derives the stable device ID from a hardware serial. The same flashed
// image yields a unique, stable ID per unit — identity that survives DHCP lease
// changes because it never lived in the address (DESIGN §3).
func DeviceID(serial string) string {
	return DeviceIDPrefix + strings.TrimSpace(serial)
}

// ReadSerial reads a stable hardware serial for the device. It prefers the Pi
// /proc/cpuinfo "Serial" line and falls back to /etc/machine-id. The path is
// overridable for testing.
func ReadSerial() (string, error) {
	if s, err := serialFromCPUInfo("/proc/cpuinfo"); err == nil && s != "" {
		return s, nil
	}
	b, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func serialFromCPUInfo(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "Serial") {
			if i := strings.IndexByte(line, ':'); i >= 0 {
				return strings.TrimSpace(line[i+1:]), nil
			}
		}
	}
	return "", nil
}
