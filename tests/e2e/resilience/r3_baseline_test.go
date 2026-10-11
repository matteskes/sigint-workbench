// Package resilience — R3 host baseline pre-flight (§4.10.5; doc
// follow-up #5). Before the 100-SDR concurrency tests run, validate
// the host stack has the file descriptors and socket buffers the
// scenario needs — a host that cannot carry 100 UDP streams fails the
// load tests in confusing mid-burst ways, so the baseline fails with
// the exact remediation instead.
package resilience

import (
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

const (
	// r3MinFDSoft is the minimum soft RLIMIT_NOFILE for R3: 100 UDP
	// sockets plus the ingest/gateway/capture HTTP+WS connections,
	// with headroom for the Go runtime's own descriptors.
	r3MinFDSoft = 512

	// r3MinSockBuf is the minimum per-socket UDP read/write buffer for
	// 100 concurrent frame streams: 256 KiB absorbs a burst between
	// consumer reads. Linux ships 212992 by default (close); hardened
	// or containerized hosts often ship far less.
	r3MinSockBuf = 262144
)

// assertR3StackBaseline validates the R3 pre-conditions on this host.
// Run before TestMultiSDRConcurrentSignals / TestMultiSDRMixedTraffic;
// also exposed standalone as TestR3StackBaseline.
func assertR3StackBaseline(t *testing.T) {
	t.Helper()

	// 1. File descriptors: the scenario needs ~100 UDP sockets plus
	// the fan-out of HTTP/WebSocket connections across services.
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rl); err != nil {
		t.Logf("R3 baseline: Getrlimit unavailable (%v) — fd check skipped", err)
	} else if rl.Cur < r3MinFDSoft {
		t.Fatalf("R3 baseline: soft RLIMIT_NOFILE = %d, want >= %d — raise it "+
			"before the 100-SDR run (ulimit -n %d; macOS: launchctl limit maxfiles; "+
			"systemd: LimitNOFILE) so sockets + service connections don't exhaust the table",
			rl.Cur, r3MinFDSoft, r3MinFDSoft)
	} else {
		t.Logf("R3 baseline: RLIMIT_NOFILE soft = %d (>= %d) OK", rl.Cur, r3MinFDSoft)
	}

	// 2. Live probe: allocate 100 UDP sockets at R3 buffer sizes — the
	// same allocation pattern the 100-SDR load actually creates.
	sockets := make([]*net.UDPConn, 0, 100)
	defer func() {
		for _, c := range sockets {
			c.Close()
		}
	}()
	for i := 0; i < 100; i++ {
		c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatalf("R3 baseline: UDP socket %d/100: %v — fd table or ephemeral "+
				"port exhaustion; see the RLIMIT_NOFILE remediation above", i+1, err)
		}
		sockets = append(sockets, c)
		if err := c.SetReadBuffer(r3MinSockBuf); err != nil {
			t.Fatalf("R3 baseline: socket %d SetReadBuffer(%d): %v — kernel socket "+
				"buffer maxima too small for the 100-SDR run", i+1, r3MinSockBuf, err)
		}
		if err := c.SetWriteBuffer(r3MinSockBuf); err != nil {
			t.Fatalf("R3 baseline: socket %d SetWriteBuffer(%d): %v", i+1, r3MinSockBuf, err)
		}
	}
	t.Logf("R3 baseline: 100 UDP sockets @ %d-byte buffers OK", r3MinSockBuf)

	// 3. Kernel maxima: a hard requirement on Linux (the deployment
	// target), advisory on macOS (dev bench).
	switch runtime.GOOS {
	case "linux":
		for _, name := range []string{"net.core.rmem_max", "net.core.wmem_max"} {
			raw, err := os.ReadFile("/proc/sys/" + name)
			if err != nil {
				t.Logf("R3 baseline: %s unreadable (%v) — check skipped", name, err)
				continue
			}
			v, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil {
				t.Logf("R3 baseline: %s = %q not an int — check skipped", name, raw)
				continue
			}
			if v < r3MinSockBuf {
				t.Fatalf("R3 baseline: %s = %d, want >= %d — sysctl -w %s=%d "+
					"(persist under /etc/sysctl.d/) or the 100 concurrent streams "+
					"will drop frames", name, v, r3MinSockBuf, name, r3MinSockBuf)
			}
			t.Logf("R3 baseline: %s = %d OK", name, v)
		}
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "kern.ipc.maxsockbuf").Output()
		if err != nil {
			t.Logf("R3 baseline: kern.ipc.maxsockbuf unreadable (%v) — advisory check skipped", err)
			break
		}
		v, err := strconv.Atoi(strings.TrimSpace(string(out)))
		if err != nil {
			t.Logf("R3 baseline: kern.ipc.maxsockbuf = %q not an int — skipped", out)
			break
		}
		if v < 1<<20 {
			t.Fatalf("R3 baseline: kern.ipc.maxsockbuf = %d, want >= 1048576 — "+
				"sysctl -w kern.ipc.maxsockbuf=8388608 (macOS clamps SetReadBuffer "+
				"to sb_max)", v)
		}
		t.Logf("R3 baseline: kern.ipc.maxsockbuf = %d OK", v)
	default:
		t.Logf("R3 baseline: no socket-buffer sysctl check for %s", runtime.GOOS)
	}
}

// TestR3StackBaseline runs the pre-flight standalone so an operator can
// validate a deployment host without executing the full 100-SDR load
// (follow-up #5). Gated on the stack like its R-suite siblings; the
// checks themselves are host-level.
func TestR3StackBaseline(t *testing.T) {
	if !serviceReady() {
		t.Skip("No services running")
	}
	assertR3StackBaseline(t)
}
