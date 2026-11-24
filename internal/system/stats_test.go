package system

import "testing"

func TestParseMeminfo(t *testing.T) {
	m := parseMeminfo("MemTotal: 8000 kB\nMemFree: 1000 kB\nMemAvailable: 6000 kB\nSwapTotal: 2000 kB\nSwapFree: 500 kB\n")
	if m.Total != 8000*1024 || m.Available != 6000*1024 || m.Used != 2000*1024 {
		t.Fatalf("mem = %+v", m)
	}
	if m.SwapTotal != 2000*1024 || m.SwapUsed != 1500*1024 {
		t.Fatalf("swap = %+v", m)
	}
}

func TestParseCPU(t *testing.T) {
	c, err := parseCPU("cpu  100 0 50 800 50 0 0 0 0 0\ncpu0 1 1 1 1 1 1 1 1\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.total != 1000 || c.idle != 850 {
		t.Fatalf("cpu = %+v", c)
	}
	if _, err := parseCPU("nothing here"); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseNetSkipsLoopbackAndContainers(t *testing.T) {
	dev := "Inter-|   Receive\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\n" +
		"    lo: 999 1 0 0 0 0 0 0 999 1 0 0 0 0 0 0\n" +
		"  eth0: 1000 5 0 0 0 0 0 0 2000 6 0 0 0 0 0 0\n" +
		"docker0: 777 1 0 0 0 0 0 0 777 1 0 0 0 0 0 0\n" +
		"  ens3: 10 1 0 0 0 0 0 0 20 1 0 0 0 0 0 0\n"
	n, err := parseNet(dev)
	if err != nil {
		t.Fatal(err)
	}
	if n.rx != 1010 || n.tx != 2020 {
		t.Fatalf("net = %+v", n)
	}
}

func TestParseLoadAndUptime(t *testing.T) {
	if l := parseLoad("0.50 1.25 2.00 1/100 999"); l != [3]float64{0.5, 1.25, 2} {
		t.Fatalf("load = %v", l)
	}
	if u := parseUptime("12345.67 999.0"); u != 12345.67 {
		t.Fatalf("uptime = %v", u)
	}
	if parseUptime("") != 0 || parseLoad("") != [3]float64{} {
		t.Fatal("empty input should give zero values")
	}
}
