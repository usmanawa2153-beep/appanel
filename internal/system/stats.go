// Package system reads host metrics from /proc and the filesystem.
package system

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	historyLen     = 60
	sampleInterval = 2 * time.Second
)

type Mem struct {
	Total     uint64 `json:"total"`
	Used      uint64 `json:"used"`
	Available uint64 `json:"available"`
	SwapTotal uint64 `json:"swap_total"`
	SwapUsed  uint64 `json:"swap_used"`
}

type Disk struct {
	Path  string `json:"path"`
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
	Free  uint64 `json:"free"`
}

type Net struct {
	RxBytes uint64  `json:"rx_bytes"`
	TxBytes uint64  `json:"tx_bytes"`
	RxRate  float64 `json:"rx_rate"` // bytes/sec
	TxRate  float64 `json:"tx_rate"`
}

// Snapshot is what GET /api/system returns.
type Snapshot struct {
	Hostname      string     `json:"hostname"`
	OS            string     `json:"os"`
	Kernel        string     `json:"kernel"`
	Arch          string     `json:"arch"`
	Cores         int        `json:"cores"`
	UptimeSeconds float64    `json:"uptime_seconds"`
	Load          [3]float64 `json:"load"`
	CPUPercent    float64    `json:"cpu_percent"`
	CPUHistory    []float64  `json:"cpu_history"`
	Mem           Mem        `json:"mem"`
	Disk          Disk       `json:"disk"`
	Net           Net        `json:"net"`
	Time          time.Time  `json:"time"`
}

type cpuTimes struct{ total, idle uint64 }
type netTotals struct{ rx, tx uint64 }

// Sampler keeps CPU and network rates, which need two readings to compute.
type Sampler struct {
	mu      sync.RWMutex
	cpu     float64
	hist    []float64
	rxRate  float64
	txRate  float64
	lastCPU cpuTimes
	lastNet netTotals
	lastAt  time.Time
	primed  bool
}

func NewSampler() *Sampler { return &Sampler{} }

// Start samples every 2s until ctx is cancelled.
func (s *Sampler) Start(ctx context.Context) {
	s.sample()
	go func() {
		t := time.NewTicker(sampleInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.sample()
			}
		}
	}()
}

func (s *Sampler) sample() {
	now := time.Now()
	c, cerr := readCPU()
	n, nerr := readNet()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.primed {
		if cerr == nil {
			if dt := c.total - s.lastCPU.total; dt > 0 && c.total >= s.lastCPU.total {
				idle := c.idle - s.lastCPU.idle
				s.cpu = clamp(100 * (1 - float64(idle)/float64(dt)))
			}
			s.hist = append(s.hist, s.cpu)
			if len(s.hist) > historyLen {
				s.hist = s.hist[len(s.hist)-historyLen:]
			}
		}
		if secs := now.Sub(s.lastAt).Seconds(); nerr == nil && secs > 0 && n.rx >= s.lastNet.rx && n.tx >= s.lastNet.tx {
			s.rxRate = float64(n.rx-s.lastNet.rx) / secs
			s.txRate = float64(n.tx-s.lastNet.tx) / secs
		}
	}
	if cerr == nil {
		s.lastCPU = c
	}
	if nerr == nil {
		s.lastNet = n
	}
	s.lastAt = now
	s.primed = true
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// Snapshot gathers current metrics. Missing /proc entries yield zero values.
func (s *Sampler) Snapshot() Snapshot {
	host, _ := os.Hostname()
	snap := Snapshot{
		Hostname: host,
		OS:       osName(),
		Kernel:   strings.TrimSpace(readFile("/proc/sys/kernel/osrelease")),
		Arch:     runtime.GOARCH,
		Cores:    runtime.NumCPU(),
		Time:     time.Now().UTC(),
	}
	snap.UptimeSeconds = parseUptime(readFile("/proc/uptime"))
	snap.Load = parseLoad(readFile("/proc/loadavg"))
	snap.Mem = parseMeminfo(readFile("/proc/meminfo"))
	snap.Disk = diskUsage("/")

	s.mu.RLock()
	snap.CPUPercent = s.cpu
	snap.CPUHistory = append([]float64(nil), s.hist...)
	snap.Net = Net{RxBytes: s.lastNet.rx, TxBytes: s.lastNet.tx, RxRate: s.rxRate, TxRate: s.txRate}
	s.mu.RUnlock()
	if snap.CPUHistory == nil {
		snap.CPUHistory = []float64{}
	}
	return snap
}

func readFile(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func readCPU() (cpuTimes, error) { return parseCPU(readFile("/proc/stat")) }

func parseCPU(stat string) (cpuTimes, error) {
	for _, line := range strings.Split(stat, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] != "cpu" {
			continue
		}
		var vals []uint64
		for _, x := range f[1:] {
			v, err := strconv.ParseUint(x, 10, 64)
			if err != nil {
				return cpuTimes{}, err
			}
			vals = append(vals, v)
		}
		// user nice system idle iowait irq softirq steal
		n := len(vals)
		if n > 8 {
			n = 8
		}
		var t cpuTimes
		for _, v := range vals[:n] {
			t.total += v
		}
		t.idle = vals[3]
		if len(vals) > 4 {
			t.idle += vals[4]
		}
		return t, nil
	}
	return cpuTimes{}, fmt.Errorf("no cpu line in /proc/stat")
}

func readNet() (netTotals, error) { return parseNet(readFile("/proc/net/dev")) }

func parseNet(dev string) (netTotals, error) {
	var t netTotals
	lines := strings.Split(dev, "\n")
	if len(lines) < 3 {
		return t, fmt.Errorf("no /proc/net/dev data")
	}
	for _, line := range lines[2:] {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "lo" || strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "br-") {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		rx, err1 := strconv.ParseUint(f[0], 10, 64)
		tx, err2 := strconv.ParseUint(f[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		t.rx += rx
		t.tx += tx
	}
	return t, nil
}

func parseUptime(s string) float64 {
	f := strings.Fields(s)
	if len(f) < 1 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

func parseLoad(s string) [3]float64 {
	var l [3]float64
	f := strings.Fields(s)
	for i := 0; i < 3 && i < len(f); i++ {
		l[i], _ = strconv.ParseFloat(f[i], 64)
	}
	return l
}

func parseMeminfo(s string) Mem {
	kv := map[string]uint64{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		kv[k] = n * 1024 // values are in kB
	}
	m := Mem{Total: kv["MemTotal"], Available: kv["MemAvailable"], SwapTotal: kv["SwapTotal"]}
	if m.Available == 0 { // very old kernels
		m.Available = kv["MemFree"] + kv["Buffers"] + kv["Cached"]
	}
	if m.Total >= m.Available {
		m.Used = m.Total - m.Available
	}
	if sf := kv["SwapFree"]; m.SwapTotal >= sf {
		m.SwapUsed = m.SwapTotal - sf
	}
	return m
}

func diskUsage(path string) Disk {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Disk{Path: path}
	}
	bs := uint64(st.Bsize)
	return Disk{
		Path:  path,
		Total: uint64(st.Blocks) * bs,
		Free:  uint64(st.Bavail) * bs,
		Used:  (uint64(st.Blocks) - uint64(st.Bfree)) * bs,
	}
}

func osName() string {
	for _, line := range strings.Split(readFile("/etc/os-release"), "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return runtime.GOOS
}
