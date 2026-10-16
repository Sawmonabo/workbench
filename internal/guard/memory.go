package guard

import (
	"fmt"
	"strings"
	"time"
)

// action is what the machine's memory calls for.
type action int

const (
	nothing action = iota
	polite         // SIGTERM
	forced         // SIGKILL
)

const (
	// criticalLevel is macOS's critical pressure level, at which Jetsam starts
	// killing processes. The warning level (2) only asks programs to trim
	// caches and is common in ordinary use, so it calls for nothing.
	criticalLevel = 4
	// criticalHold is how long the critical level must last before it calls
	// for a forced stop.
	criticalHold = 2 * time.Second
	// softHold is how long a soft condition must last before it calls for a
	// polite stop, as systemd-oomd's DefaultMemoryPressureDurationSec does
	// (Fedora ships 20 s).
	softHold = 20 * time.Second
)

// memory is one reading of the machine's memory. On macOS Level is
// kern.memorystatus_vm_pressure_level (1 normal, 2 warning, 4 critical),
// which decides with the swap. On Linux Level is 0, Total is earlyoom's
// figure (MemAvailable + AnonPages), so Available/Total is the share user
// memory can still take, and FullAvg10 is the memory pressure file's full
// avg10, -1 when absent. Installed is the machine's RAM, which sets the floor
// below which the agent commands are too small to stop politely, and is named
// in the reason text. In a cgroup with a lower memory limit, Linux reads that
// cgroup instead and Installed is its limit (see confined).
type memory struct {
	Level               int
	Available, Total    uint64
	Installed           uint64
	SwapFree, SwapTotal uint64
	FullAvg10           float64
}

func (m memory) availablePercent() float64 {
	if m.Total == 0 {
		return 100
	}
	return float64(m.Available) * 100 / float64(m.Total)
}

// swapFreePercent counts no swap as none free, so the swap condition is met.
func (m memory) swapFreePercent() float64 {
	if m.SwapTotal == 0 {
		return 0
	}
	return float64(m.SwapFree) * 100 / float64(m.SwapTotal)
}

// decide applies spec section 6.2 to one reading. soft marks a polite stop
// called for by Linux's pressure condition alone, which counts only once it
// has lasted softHold (see watcher.demand): pressure stays high for seconds
// after memory is freed, so it counts only with low memory too. On macOS the
// critical level calls for a polite stop, forced when at most a tenth of the
// swap is free.
func decide(m memory) (act action, soft bool) {
	if m.Level > 0 {
		switch {
		case m.Level >= criticalLevel && m.swapFreePercent() <= 10:
			return forced, false
		case m.Level >= criticalLevel:
			return polite, false
		}
		return nothing, false
	}
	available, swap := m.availablePercent(), m.swapFreePercent()
	switch {
	case available <= 5 && swap <= 5:
		return forced, false
	case available <= 10 && swap <= 10:
		return polite, false
	case m.FullAvg10 >= 50 && available <= 20:
		return polite, true
	}
	return nothing, false
}

// floor is the least the agent commands must hold together for the largest
// to be stopped at a polite stop: a tenth of the installed memory, at most
// 1 GiB. Stopping one of commands that hold less frees too little to end a
// shortage they did not cause, so the next command would be stopped after the
// cooldown, and the next. A forced stop has no floor.
func (m memory) floor() uint64 {
	return min(m.Installed/10, 1<<30)
}

// delay is the time to the next tick. On Linux it follows earlyoom: the room
// left above the polite-stop line over fill rates of 6000 MiB/s for memory
// and 800 MiB/s for swap, clamped to 100 ms–1 s. On macOS it is 100 ms at
// warning or above, so critical is seen at once, else 1 s.
func delay(m memory) time.Duration {
	if m.Level > 0 {
		if m.Level >= 2 {
			return 100 * time.Millisecond
		}
		return time.Second
	}
	const mib = 1 << 20
	memoryRoom := max((m.availablePercent()-10)/100*float64(m.Total), 0)
	swapRoom := max((m.swapFreePercent()-10)/100*float64(m.SwapTotal), 0)
	seconds := memoryRoom/(6000*mib) + swapRoom/(800*mib)
	return min(max(time.Duration(seconds*float64(time.Second)), 100*time.Millisecond), time.Second)
}

// reason is what the agent is told: the command, its size, the machine's
// state and what to try.
func reason(command string, children int, size uint64, m memory) string {
	var state string
	switch {
	case m.Level > 0:
		state = fmt.Sprintf("memory pressure critical, %s installed, %s of swap in use",
			gigabytes(m.Installed), gigabytes(m.SwapTotal-m.SwapFree))
	case m.SwapTotal == 0:
		state = fmt.Sprintf("%s of %s available, no swap",
			gigabytes(m.Available), gigabytes(m.Installed))
	default:
		state = fmt.Sprintf("%s of %s available, swap %.0f %% used",
			gigabytes(m.Available), gigabytes(m.Installed), 100-m.swapFreePercent())
	}
	var processes string
	switch {
	case children == 1:
		processes = " and its 1 child process"
	case children > 1:
		processes = fmt.Sprintf(" and its %d child processes", children)
	}
	return fmt.Sprintf("workbench guard: stopped `%s`%s. The machine was nearly out of memory "+
		"(%s) and this was the largest agent command, at %s. Retry with less parallelism or a "+
		"smaller input.", clip(command), processes, state, gigabytes(size))
}

// clip makes a command fit one line of the reason: no newlines or
// backticks, at most 120 characters.
func clip(command string) string {
	command = strings.Join(strings.Fields(strings.ReplaceAll(command, "`", "'")), " ")
	if runes := []rune(command); len(runes) > 120 {
		return string(runes[:119]) + "…"
	}
	return command
}

func gigabytes(bytes uint64) string {
	value := float64(bytes) / (1 << 30)
	if value >= 10 {
		return fmt.Sprintf("%.0f GB", value)
	}
	return fmt.Sprintf("%.1f GB", value)
}
