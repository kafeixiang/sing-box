package boxapi

import (
	"context"
	"net"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing/common/metadata"
)

func DialContext(ctx context.Context, box *box.Box, network, addr string) (net.Conn, error) {
	defOutboundTag := box.Outbound().Default().Tag()
	conn, err := dialer.NewDetour(box.Outbound(), defOutboundTag).DialContext(ctx, network, metadata.ParseSocksaddr(addr))
	if err != nil {
		return nil, err
	}
	if vs := box.Router().GetTracker(); vs != nil {
		if ss, ok := vs.(*SbStatsService); ok {
			conn = ss.RoutedConnectionInternal("", defOutboundTag, "", conn, false)
		}
	}
	return conn, nil
}

func DialUDP(ctx context.Context, box *box.Box) (net.PacketConn, error) {
	return dialer.NewDetour(box.Outbound(), box.Outbound().Default().Tag()).ListenPacket(ctx, metadata.Socksaddr{})
}
