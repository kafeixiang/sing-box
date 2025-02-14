package boxapi

import (
	"context"
	"net"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/experimental/v2rayapi"
	"github.com/sagernet/sing/common/metadata"
)

func DialContext(ctx context.Context, box *box.Box, network, addr string) (net.Conn, error) {
	defaultOutbound := box.Outbound().Default()
	conn, err := defaultOutbound.DialContext(ctx, network, metadata.ParseSocksaddr(addr))
	if err != nil {
		return nil, err
	}
	if ss, ok := box.Router().Tracker().(*v2rayapi.StatsService); ok {
		conn = ss.RoutedConnection(ctx, conn, adapter.InboundContext{}, nil, defaultOutbound)
	}
	return conn, nil
}

func DialUDP(ctx context.Context, box *box.Box) (net.PacketConn, error) {
	return box.Outbound().Default().ListenPacket(ctx, metadata.Socksaddr{})
}
