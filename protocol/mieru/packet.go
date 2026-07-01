package mieru

import (
	"io"
	"net"
	"net/netip"

	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	mierumodel "github.com/enfein/mieru/v3/apis/model"
)

type mieruPacketConn struct {
	net.PacketConn
	destination M.Socksaddr
}

var _ N.PacketConn = (*mieruPacketConn)(nil)

func (c *mieruPacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	return c.PacketConn.WriteTo(p, c.destination.UDPAddr())
}

func (c *mieruPacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	n, _, err = c.PacketConn.ReadFrom(p)
	return n, c.destination.UDPAddr(), err
}

func (c *mieruPacketConn) LocalAddr() net.Addr {
	if addr, ok := c.PacketConn.(N.LocalAddr); ok {
		return addr.LocalAddr()
	}
	return &net.UDPAddr{IP: net.IPv4zero, Port: 0}
}

func (c *mieruPacketConn) RemoteAddr() net.Addr {
	return c.destination.UDPAddr()
}

func (c *mieruPacketConn) ReadPacket(buffer *buf.Buffer) (destination M.Socksaddr, err error) {
	n, _, err = c.PacketConn.ReadFrom(buffer.FreeBytes())
	if err != nil {
		return M.Socksaddr{}, err
	}
	buffer.Truncate(n)
	if buffer.Len() < 3 {
		return M.Socksaddr{}, io.ErrShortBuffer
	}

	// Skip RSV (2 bytes) and FRAG (1 byte).
	buffer.Advance(3)

	var addr mierumodel.AddrSpec
	if err := addr.ReadFromSocks5(buffer); err != nil {
		return M.Socksaddr{}, err
	}
	if addr.FQDN != "" {
		destination = M.Socksaddr{
			Fqdn: addr.FQDN,
			Port: uint16(addr.Port),
		}
	} else if addr.IP != nil {
		netAddr, _ := netip.AddrFromSlice(addr.IP)
		destination = M.Socksaddr{
			Addr: netAddr.Unmap(),
			Port: uint16(addr.Port),
		}
	}
	return destination, nil
}

func (c *mieruPacketConn) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	header := buf.NewSize(3 + M.MaxSocksaddrLength)
	defer header.Release()

	// RSV (2 bytes) + FRAG (1 byte)
	common.Must(header.WriteZeroN(3))

	var addr mierumodel.AddrSpec
	if destination.IsFqdn() {
		addr.FQDN = destination.Fqdn
	} else {
		addr.IP = destination.Addr.AsSlice()
	}
	addr.Port = int(destination.Port)
	if err := addr.WriteToSocks5(header); err != nil {
		return err
	}

	packet := buf.NewSize(header.Len() + buffer.Len())
	defer packet.Release()
	common.Must1(packet.Write(header.Bytes()))
	common.Must1(packet.Write(buffer.Bytes()))
	_, err := c.PacketConn.WriteTo(packet.Bytes(), nil)
	return err
}
