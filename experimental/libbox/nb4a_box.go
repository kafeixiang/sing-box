package libbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/matsuridayo/libneko/protect_server"
	"github.com/matsuridayo/libneko/speedtest"
	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/boxapi"
	"github.com/sagernet/sing-box/common/conntrack"
	"github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/experimental/clashapi"
	"github.com/sagernet/sing-box/experimental/deprecated"
	"github.com/sagernet/sing-box/experimental/libbox/platform"
	boxLog "github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/protocol/group"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
)

var mainInstance *BoxInstance

func VersionBox() string {
	version := []string{
		"sing-box: " + constant.Version,
		runtime.Version() + "@" + runtime.GOOS + "/" + runtime.GOARCH,
	}

	var tags string
	debugInfo, loaded := debug.ReadBuildInfo()
	if loaded {
		for _, setting := range debugInfo.Settings {
			switch setting.Key {
			case "-tags":
				tags = setting.Value
			}
		}
	}

	if tags != "" {
		version = append(version, tags)
	}

	return strings.Join(version, "\n")
}

func ResetAllConnections(system bool) {
	if system {
		conntrack.Close()
		log.Println("[Debug] Reset system connections done")
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

	selector     *group.Selector
	pauseManager pause.Manager
	trafficStats TrafficStats
}

func NewSingBoxInstance(configContent string, forTest bool) (b *BoxInstance, err error) {
	defer DeferPanicToError("NewSingBoxInstance", func(err_ error) { err = err_ })

	ctx := BaseContext(intfBox)
	options, err := parseConfig(ctx, configContent)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	ctx = service.ContextWithDefaultRegistry(ctx)
	platformWrapper := &boxPlatformInterfaceWrapper{
		platformInterfaceWrapper: platformInterfaceWrapper{
			iif:       intfBox,
			useProcFS: intfBox.UseProcFS(),
			isTest:    forTest,
		},
	}
	service.MustRegister[platform.Interface](ctx, platformWrapper)
	var platformLogWriter boxLog.PlatformWriter
	if !forTest {
		platformLogWriter = platformWrapper
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
	}

	if !forTest {
		// selector
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
	b.Box.Close()

	return nil
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

func (b *BoxInstance) QueryStats() TrafficStats {
	trafficStats := TrafficStats{Ups: make(map[string]int64), Downs: make(map[string]int64)}
	if b.trafficStats.Ups == nil || b.trafficStats.Downs == nil {
		b.trafficStats.Ups = make(map[string]int64)
		b.trafficStats.Downs = make(map[string]int64)
	}
	if clashServer, ok := b.Router().GetClashTracker().(*clashapi.Server); ok {
		for _, out := range b.Outbound().Outbounds() {
			tag := out.Tag()
			up, down := clashServer.TrafficManager().TotalOutbound(tag)
			trafficStats.Ups[tag] = up - b.trafficStats.Ups[tag]
			trafficStats.Downs[tag] = down - b.trafficStats.Downs[tag]
			b.trafficStats.Ups[tag] = up
			b.trafficStats.Downs[tag] = down
		}
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

func UrlTest(i *BoxInstance, link string, timeout int32) (latency int32, err error) {
	defer DeferPanicToError("box.UrlTest", func(err_ error) { err = err_ })
	if i == nil {
		// test current
		return speedtest.UrlTest(boxapi.CreateProxyHttpClient(mainInstance.Box), link, timeout, speedtest.UrlTestStandard_Handshake)
	}
	return speedtest.UrlTest(boxapi.CreateProxyHttpClient(i.Box), link, timeout, speedtest.UrlTestStandard_Handshake)
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
