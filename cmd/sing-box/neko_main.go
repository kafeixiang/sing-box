package main

/*
#include <stdlib.h>
typedef void (*CallbackFunc)(const char*);
static void call_callback(CallbackFunc callback, const char* s) {
	callback(s);
}
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/experimental/libbox"

	"github.com/matsuridayo/libneko/neko_common"
)

type logWriter struct {
	callback C.CallbackFunc
}

func (m *logWriter) DisableColors() bool {
	return true
}

func (m *logWriter) WriteMessage(level uint8, message string) {
	log := C.CString(message)
	defer C.free(unsafe.Pointer(log))
	C.call_callback(m.callback, log)
}

//export BoxMain
func BoxMain(logFunc C.CallbackFunc) {
	libbox.NekoLogWriter = &logWriter{logFunc}
	libbox.NekoLogWriter.WriteMessage(0, fmt.Sprintf("sing-box: %s", constant.Version))

	neko_common.RunMode = neko_common.RunMode_NekoBox_Core
}
