package libbox

import (
	"math"
	runtimeDebug "runtime/debug"

	"github.com/sagernet/sing-box/common/conntrack"
	C "github.com/sagernet/sing-box/constant"
)

var memoryLimitEnabled bool

func SetMemoryLimit(enabled bool) {
	memoryLimitEnabled = enabled
	const memoryLimit = 45 * 1024 * 1024
	const memoryLimitGo = memoryLimit / 1.5
	if enabled {
		runtimeDebug.SetGCPercent(10)
		if C.IsIos {
			runtimeDebug.SetMemoryLimit(int64(memoryLimitGo))
		}
		conntrack.KillerEnabled = true
		conntrack.MemoryLimit = memoryLimit
	} else {
		runtimeDebug.SetGCPercent(100)
		if C.IsIos {
			runtimeDebug.SetMemoryLimit(math.MaxInt64)
		}
		conntrack.KillerEnabled = false
	}
}
