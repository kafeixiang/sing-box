package libbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"runtime/debug"
	"strings"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/experimental/clashapi"
	"github.com/sagernet/sing-box/experimental/deprecated"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/protocol/group"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
	E "github.com/sagernet/sing/common/exceptions"

	"github.com/matsuridayo/libneko/protect_server"
	"github.com/matsuridayo/libneko/speedtest"
)

var mainInstance *BoxInstance

func VersionBox() string {
	version := []string{
		"sing-box: " + constant.Version,
		runtime.Version() + "@" + runtime.GOOS + "/" + runtime.GOARCH,
	}

	if debugInfo, ok := debug.ReadBuildInfo(); ok {
		for _, s := range debugInfo.Settings {
			if s.Key == "-tags" && s.Value != "" {
				version = append(version, s.Value)
				break
			}
		}
	}
	return strings.Join(version, "\n")
}

func ResetAllConnections(system bool) {
	if system && mainInstance != nil {
		mainInstance.ResetConnections()
		NekoLogPrintln("[Debug] Reset system connections done")
	}
}

type TrafficStats struct {
	Ups   map[string]int64 `json:"ups"`
	Downs map[string]int64 `json:"downs"`
}

type BoxInstance struct {
	*box.Box
	ctx    context.Context
	cancel context.CancelFunc
	state  int

	clashServer  adapter.ClashServer
	pauseManager pause.Manager
	trafficStats *TrafficStats
	selector     *group.Selector
}

func NewSingBoxInstance(configContent string, forTest bool) (b *BoxInstance, err error) {
	defer DeferPanicToError("NewSingBoxInstance", func(err_ error) { err = err_ })

	ctx := baseContext(intfBox)
	options, err := parseConfig(ctx, configContent)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(service.ContextWithDefaultRegistry(ctx))
	var platformWrapper *platformInterfaceWrapper
	if intfBox != nil {
		platformWrapper = &platformInterfaceWrapper{
			iif:       intfBox,
			useProcFS: intfBox.UseProcFS(),
			isTest:    forTest,
		}
		service.MustRegister[adapter.PlatformInterface](ctx, platformWrapper)
	}
	var platformLogWriter log.PlatformWriter
	if !forTest {
		if platformWrapper != nil {
			platformLogWriter = platformWrapper
		} else {
			platformLogWriter = NekoLogWriter
		}
		service.MustRegister[deprecated.Manager](ctx, new(deprecatedManager))
	}
	instance, err := box.New(box.Options{
		Context:           ctx,
		Options:           options,
		PlatformLogWriter: platformLogWriter,
	})
	if err != nil {
		cancel()
		return nil, E.Cause(err, "create service")
	}

	b = &BoxInstance{
		Box:          instance,
		ctx:          ctx,
		cancel:       cancel,
		pauseManager: service.FromContext[pause.Manager](ctx),
		clashServer:  service.FromContext[adapter.ClashServer](ctx),
	}

	if !forTest {
		b.trafficStats = &TrafficStats{
			Ups:   make(map[string]int64),
			Downs: make(map[string]int64),
		}
		if proxy, ok := b.Box.Outbound().Outbound("proxy"); ok {
			if selector, ok := proxy.(*group.Selector); ok {
				b.selector = selector
			}
		}
	}

	return b, nil
}

func (b *BoxInstance) Start() (err error) {
	defer DeferPanicToError("box.Start", func(err_ error) { err = err_ })

	if b.state == 0 {
		b.state = 1
		return b.Box.Start()
	}
	return errors.New("already started")
}

func (b *BoxInstance) Close() (err error) {
	defer DeferPanicToError("box.Close", func(err_ error) { err = err_ })

	// no double close
	if b.state == 2 {
		return nil
	}
	b.state = 2

	// clear main instance
	if mainInstance == b {
		mainInstance = nil
		goServeProtect(false)
	}

	// close box
	b.cancel()
	return b.Box.Close()
}

func (b *BoxInstance) Sleep() {
	b.pauseManager.DevicePause()
	b.Box.Router().ResetNetwork()
}

func (b *BoxInstance) Wake() {
	b.pauseManager.DeviceWake()
}

func (b *BoxInstance) SetAsMain() {
	mainInstance = b
	goServeProtect(true)
}

func (b *BoxInstance) ResetConnections() {
	cm := service.FromContext[adapter.ConnectionManager](b.ctx)
	if cm != nil {
		cm.CloseAll()
	}
}

func (b *BoxInstance) QueryStats() *TrafficStats {
	if b.trafficStats == nil {
		return nil
	}
	trafficStats := &TrafficStats{Ups: make(map[string]int64), Downs: make(map[string]int64)}
	for _, out := range b.Outbound().Outbounds() {
		tag := out.Tag()
		up, down := b.clashServer.(*clashapi.Server).TrafficManager().TotalOutbound(tag)
		trafficStats.Ups[tag] = up - b.trafficStats.Ups[tag]
		trafficStats.Downs[tag] = down - b.trafficStats.Downs[tag]
		b.trafficStats.Ups[tag], b.trafficStats.Downs[tag] = up, down
	}
	return trafficStats
}

func (b *BoxInstance) QueryStats2JSON() string {
	data, err := json.Marshal(b.QueryStats())
	if err != nil {
		return "{}"
	}
	return string(data)
}

func (b *BoxInstance) SelectOutbound(tag string) bool {
	if b.selector != nil {
		return b.selector.SelectOutbound(tag)
	}
	return false
}

func UrlTest(boxInstance *BoxInstance, link string, timeout int32) (latency int32, err error) {
	defer DeferPanicToError("box.UrlTest", func(err_ error) { err = err_ })

	i := boxInstance
	if i == nil {
		// test current
		i = mainInstance
	}
	return speedtest.UrlTest(CreateProxyHttpClient(i), link, timeout, speedtest.UrlTestStandard_Handshake)
}

var protectCloser io.Closer

func goServeProtect(start bool) {
	if protectCloser != nil {
		protectCloser.Close()
		protectCloser = nil
	}
	if start {
		protectCloser = protect_server.ServeProtect("protect_path", false, 0, func(fd int) {
			intfBox.AutoDetectInterfaceControl(int32(fd))
		})
	}
}
