package boxapi

import (
	"context"
	"net"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/metadata"
)

func DialContext(ctx context.Context, box *box.Box, network, addr string) (net.Conn, error) {
	defaultOutbound := box.Outbound().Default()
	conn, err := defaultOutbound.DialContext(ctx, network, metadata.ParseSocksaddr(addr))
	if err != nil {
		return nil, err
	}
	if tracker := box.Router().GetClashTracker(); tracker != nil {
		conn = tracker.RoutedConnection(ctx, conn, adapter.InboundContext{}, nil, defaultOutbound)
	}
	return conn, nil
}

func DialUDP(ctx context.Context, box *box.Box) (net.PacketConn, error) {
	return box.Outbound().Default().ListenPacket(ctx, metadata.Socksaddr{})
}
