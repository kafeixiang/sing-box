package libbox

import (
	"math"
	runtimeDebug "runtime/debug"

<<<<<<< HEAD
	C "github.com/sagernet/sing-box/constant"
=======
	"github.com/sagernet/sing-box/common/conntrack"
>>>>>>> 12dd1ac8 (Improve conntrack)
)

var memoryLimitEnabled bool

func SetMemoryLimit(enabled bool) {
<<<<<<< HEAD
	memoryLimitEnabled = enabled
	const memoryLimitGo = 45 * 1024 * 1024
	if enabled {
		runtimeDebug.SetGCPercent(10)
		if C.IsIos {
			runtimeDebug.SetMemoryLimit(memoryLimitGo)
		}
=======
	const memoryLimit = 45 * 1024 * 1024
	const memoryLimitGo = memoryLimit / 1.5
	if enabled {
		runtimeDebug.SetGCPercent(10)
		runtimeDebug.SetMemoryLimit(memoryLimitGo)
		conntrack.KillerEnabled = true
		conntrack.MemoryLimit = memoryLimit
>>>>>>> 12dd1ac8 (Improve conntrack)
	} else {
		runtimeDebug.SetGCPercent(100)
		if C.IsIos {
			runtimeDebug.SetMemoryLimit(math.MaxInt64)
		}
	}
}
