package waterwall

import (
	"os"
	"strings"
)

func localIP(cidr string) string {
	ip, _, _ := strings.Cut(cidr, "/")
	return ip
}

func standardCPU() bool {
	// The generic build targets x86-64-v3. A machine with incomplete
	// flags takes the compatible asset instead.
	for _, line := range strings.Split(cpuInfo(), "\n") {
		key, rest, _ := strings.Cut(line, ":")
		if strings.TrimSpace(key) != "flags" {
			continue
		}
		flags := " " + strings.Join(strings.Fields(rest), " ") + " "
		for _, needed := range []string{"avx", "avx2", "bmi1", "bmi2", "f16c", "fma", "abm", "movbe", "xsave"} {
			if !strings.Contains(flags, " "+needed+" ") {
				return false
			}
		}
		return true
	}
	return false
}

func cpuInfo() string {
	data, _ := os.ReadFile("/proc/cpuinfo")
	return string(data)
}
