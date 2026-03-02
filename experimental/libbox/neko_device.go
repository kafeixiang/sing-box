package libbox

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

var DebugFunc func(interface{})

func GoDebug(any interface{}) {
	if DebugFunc != nil {
		go DebugFunc(any)
	}
}

func DeferPanicToError(name string, err func(error)) {
	if r := recover(); r != nil {
		s := fmt.Errorf("%s panic: %s\n%s", name, r, string(debug.Stack()))
		err(s)
	}
}

func NumUDPWorkers() int {
	numUDPWorkers := 4
	if num := runtime.GOMAXPROCS(0); num > numUDPWorkers {
		numUDPWorkers = num
	}
	return numUDPWorkers
}
