package mieru

import (
	"context"
	"fmt"
	"net"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	mieruclient "github.com/enfein/mieru/v3/apis/client"
	mierucommon "github.com/enfein/mieru/v3/apis/common"
	mierutp "github.com/enfein/mieru/v3/apis/trafficpattern"
	mierupb "github.com/enfein/mieru/v3/pkg/appctl/appctlpb"
	"google.golang.org/protobuf/proto"
)

func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[option.MieruOutboundOptions](registry, C.TypeMieru, NewOutbound)
}

type Outbound struct {
	outbound.Adapter
	logger logger.ContextLogger
	dialer N.Dialer
	client mieruclient.Client
}

func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.MieruOutboundOptions) (adapter.Outbound, error) {
	outboundDialer, err := dialer.New(ctx, options.DialerOptions, options.ServerIsDomain())
	if err != nil {
		return nil, err
	}

	config, err := buildMieruClientConfig(options)
	if err != nil {
		return nil, fmt.Errorf("failed to build mieru client config: %w", err)
	}

	c := mieruclient.NewClient()
	if err := c.Store(config); err != nil {
		return nil, fmt.Errorf("failed to store mieru client config: %w", err)
	}

	return &Outbound{
		Adapter: outbound.NewAdapterWithDialerOptions(C.TypeMieru, tag, options.Network.Build(), options.DialerOptions),
		logger:  logger,
		dialer:  outboundDialer,
		client:  c,
	}, nil
}

func (h *Outbound) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	if err := h.client.Start(); err != nil {
		return fmt.Errorf("failed to start mieru client: %w", err)
	}
	h.logger.Info("mieru client is started")
	return nil
}

func (h *Outbound) Close() error {
	if h.client.IsRunning() {
		return h.client.Stop()
	}
	return nil
}

func (h *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	h.logger.InfoContext(ctx, "outbound connection to ", destination)

	conn, err := h.client.DialContext(ctx, destination.NetAddr(network))
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func (h *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	conn, err := h.client.DialContext(ctx, destination.UDPAddr())
	if err != nil {
		return nil, err
	}
	return &mieruPacketConn{
		PacketConn:  mierucommon.NewPacketOverStreamTunnel(conn),
		destination: destination,
	}, nil
}

func buildMieruClientConfig(options option.MieruOutboundOptions) (*mieruclient.ClientConfig, error) {
	var transportProtocol *mierupb.TransportProtocol
	switch options.Transport {
	case "TCP":
		transportProtocol = mierupb.TransportProtocol_TCP.Enum()
	case "UDP":
		transportProtocol = mierupb.TransportProtocol_UDP.Enum()
	default:
		return nil, E.New("unsupported transport: ", options.Transport)
	}

	var portBindings []*mierupb.PortBinding
	serverAddr := options.ServerOptions.Build()
	portBindings = append(portBindings, &mierupb.PortBinding{
		Port:     proto.Int32(int32(serverAddr.Port)),
		Protocol: transportProtocol,
	})

	for _, portRange := range options.ServerPortRanges {
		// Parse port ranges if needed, but for now we just use the main server port
		_ = portRange
	}

	var trafficPattern *mierupb.TrafficPattern
	if options.TrafficPattern != "" {
		var err error
		trafficPattern, err = mierutp.Decode(options.TrafficPattern)
		if err != nil {
			return nil, fmt.Errorf("failed to decode traffic pattern %q: %w", options.TrafficPattern, err)
		}
	}

	return &mieruclient.ClientConfig{
		Profile: &mierupb.ClientProfile{
			ProfileName: proto.String("default"),
			User: &mierupb.User{
				Name:     proto.String(options.UserName),
				Password: proto.String(options.Password),
			},
			Servers: []*mierupb.ServerEndpoint{
				{
					IpAddress:    proto.String(serverAddr.Addr.String()),
					PortBindings: portBindings,
				},
			},
			TrafficPattern: trafficPattern,
		},
	}, nil
}

type mieruPacketConn struct {
	net.PacketConn
	destination M.Socksaddr
}

func (c *mieruPacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	return c.PacketConn.WriteTo(p, c.destination.UDPAddr())
}

func (c *mieruPacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	n, _, err = c.PacketConn.ReadFrom(p)
	return n, c.destination.UDPAddr(), err
}

func (c *mieruPacketConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4zero, Port: 0}
}

func (c *mieruPacketConn) RemoteAddr() net.Addr {
	return c.destination.UDPAddr()
}
