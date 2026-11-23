package manage

import (
	"strings"
	"time"
)

// What the web panel's Connection Test section needs from the test that the
// menu runs. The panel drives the same StartConnTestIran → Joined → Run →
// Fetched sequence connTestIranMenu does; these are the few facts it would
// otherwise have to copy.

// ConnTestJoinWait is how long the Iran side waits for the kharej to check in.
func ConnTestJoinWait() time.Duration { return connTestJoinWait }

// ConnTestRunTime is about how long the tunnels take to be judged once the
// kharej has joined: they are tried side by side, each given its connect wait
// and its one-second echoes, and then the speed runs and the path MTU arrives.
// It is what the panel counts down from, so it errs a little long rather than
// reaching zero with rows still filling.
func ConnTestRunTime() time.Duration {
	return connTestConnectWait + time.Duration(connTestSoak)*time.Second + 45*time.Second +
		ConnTestSpeedTime(len(connTestReverse)-1+len(connTestDirect)) +
		time.Duration(len(ctExtraCases()))*ctExtraTime()
}

// ConnTestSpeedTime estimates the serial speed phase for healthy tunnels:
// both directions at every connection count, then both once more in the
// second pass.
func ConnTestSpeedTime(tunnels int) time.Duration {
	return time.Duration(tunnels) * (ctSpeedFirstPass() + ctSpeedSecondPass())
}

// ConnTestSpeedLeft estimates what is left of the speed phase from the rows'
// phases, or 0 when it has not started.
func ConnTestSpeedLeft(phases []string) time.Duration {
	var left time.Duration
	started := false
	for _, p := range phases {
		switch p {
		case "speed-queued":
			left += ctSpeedFirstPass() + ctSpeedSecondPass()
		case "speed":
			started = true
			left += ctSpeedFirstPass() + ctSpeedSecondPass()
		case ctPhaseAgain, ctPhaseSecond:
			started = true
			left += ctSpeedSecondPass()
		}
	}
	if !started {
		return 0
	}
	return left
}

func ctExtraTime() time.Duration {
	return connTestConnectWait + time.Duration(connTestSoak)*time.Second + ConnTestSpeedTime(1) + 70*time.Second
}

// ConnTestTimeLeft estimates the serial work that remains once speed tests or
// one of the helper transports has started. While parallel echoes are still
// running, the caller keeps the overall estimate.
func ConnTestTimeLeft(rows []ConnTestResult) time.Duration {
	var native []string
	var helpers time.Duration
	active, nativeProbing := false, false
	for _, row := range rows {
		if row.Transport != ctGostDirect && !strings.HasPrefix(row.Transport, ctOmniPrefix) {
			native = append(native, row.Phase)
			nativeProbing = nativeProbing || (row.Status == ctTesting && row.Phase == "")
			continue
		}
		switch row.Phase {
		case ctPhaseTransportQueued:
			helpers += ctExtraTime()
		case ctPhaseTransportSetup, ctPhaseTransportConnect:
			helpers += ctExtraTime()
			active = true
		case "speed":
			helpers += ConnTestSpeedTime(1)
			active = true
		case ctPhaseSecond:
			helpers += ctSpeedSecondPass()
			active = true
		case ctPhaseTransportCleanup:
			helpers += 40 * time.Second
			active = true
		case ctPhaseTransportEcho:
			remaining := max(0, row.Total-row.Tried)
			helpers += time.Duration(remaining)*time.Second + ConnTestSpeedTime(1)
			active = true
		case "speed-queued":
			helpers += ConnTestSpeedTime(1)
			active = true
		}
	}
	left := ConnTestSpeedLeft(native)
	if nativeProbing || (left == 0 && !active) {
		return 0
	}
	if left == 0 {
		// A helper can be active while all native speed tests are still queued.
		for _, phase := range native {
			if phase == "speed-queued" {
				left += ConnTestSpeedTime(1)
			}
		}
	}
	return left + helpers
}

// ConnTestSpeedPhase is a phase in which a row is being measured, in either
// pass.
func ConnTestSpeedPhase(phase string) bool { return phase == "speed" || phase == ctPhaseSecond }

func ctSpeedFirstPass() time.Duration {
	return time.Duration(2*len(ctSpeedStages))*(ctSpeedWarmup+ctSpeedMaximum) + time.Second
}

func ctSpeedSecondPass() time.Duration { return 2*(ctSpeedWarmup+ctSpeedMaximum) + time.Second }

// ConnTestName is a tested transport's name as the menus print it: "TCP MUX",
// "KCP FEC", "Iran→Kharej".
func ConnTestName(tr string) string { return ctName(tr) }
