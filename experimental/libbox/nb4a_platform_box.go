package libbox

import (
	"strings"

	"github.com/matsuridayo/libneko/neko_log"
	"github.com/sagernet/sing-box/experimental/libbox/platform"
	"github.com/sagernet/sing-box/log"
)

var (
	_ platform.Interface = (*boxPlatformInterfaceWrapper)(nil)
	_ log.PlatformWriter = (*boxPlatformInterfaceWrapper)(nil)
)

type boxPlatformInterfaceWrapper struct {
	platformInterfaceWrapper
}

// io.Writer

func (w *boxPlatformInterfaceWrapper) WriteMessage(level log.Level, message string) {
	if !strings.HasSuffix(message, "\n") {
		message += "\n"
	}
	neko_log.LogWriter.Write([]byte(message))
}
