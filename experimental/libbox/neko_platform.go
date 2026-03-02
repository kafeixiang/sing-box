package libbox

import (
	"github.com/sagernet/sing-box/log"
)

var (
	intfBox  PlatformInterface
	intfNB4A NB4AInterface
)

type NB4AInterface interface {
	UseOfficialAssets() bool
	Selector_OnProxySelected(selectorTag string, tag string)
}

var NekoLogWriter log.PlatformWriter
