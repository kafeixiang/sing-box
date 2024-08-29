package main

import "C"

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sagernet/sing-box/experimental/libbox"

	"github.com/matsuridayo/libneko/neko_common"
	"github.com/matsuridayo/libneko/speedtest"
)

var mainInstance *libbox.BoxInstance

//export BoxStart
func BoxStart(CoreConfig *C.char) *C.char {
	coreConfig := C.GoString(CoreConfig)

	if neko_common.Debug {
		log.Println("Start:", coreConfig)
	}

	if mainInstance != nil {
		return C.CString("instance already started")
	}

	instance, err := libbox.NewSingBoxInstance(coreConfig, false)
	if err == nil {
		err = instance.Start()
	}
	if err != nil {
		return C.CString(err.Error())
	}

	mainInstance = instance
	return nil
}

//export BoxStop
func BoxStop() *C.char {
	if mainInstance != nil {
		err := mainInstance.Close()
		mainInstance = nil
		if err != nil {
			return C.CString(err.Error())
		}
	}
	return nil
}

//export BoxTest
func BoxTest(Mode C.int, Address *C.char, Url *C.char, Timeout C.int, SpeedUrl *C.char, SpeedTimeout C.int, CoreConfig *C.char) *C.char {
	mode := int(Mode)
	address := C.GoString(Address)
	url := C.GoString(Url)
	timeout := int32(Timeout)
	speedUrl := C.GoString(SpeedUrl)
	speedTimeout := int32(SpeedTimeout)
	coreConfig := C.GoString(CoreConfig)
	const (
		TcpPing   = 1 << 0 // 1
		UrlTest   = 1 << 1 // 2
		UdpTest   = 1 << 2 // 4
		SpeedTest = 1 << 3 // 8
		IpTest    = 1 << 4 // 16
	)
	const (
		KiB = 1024
		MiB = 1024 * KiB
	)
	var (
		i          *libbox.BoxInstance
		httpClient *http.Client
		results    []string
	)
	if mode&(UrlTest|UdpTest|SpeedTest|IpTest) != 0 {
		if coreConfig != "" {
			// Test instance
			var err error
			i, err = libbox.NewSingBoxInstance(coreConfig, true)
			if err == nil {
				err = i.Start()
			}
			if err != nil {
				return C.CString(err.Error())
			}
			defer i.Close()
		} else {
			// Test running instance
			i = mainInstance
			if i == nil {
				return C.CString("no interface")
			}
		}
		httpClient = libbox.CreateProxyHttpClient(i)
	}
	if mode&TcpPing != 0 {
		ms, err := speedtest.TcpPing(address, timeout)
		if err == nil {
			results = append(results, strconv.Itoa(int(ms)))
		} else {
			results = append(results, err.Error())
		}
	}
	if mode&UrlTest != 0 {
		ms, err := speedtest.UrlTest(httpClient, url, timeout, speedtest.UrlTestStandard_Handshake)
		if err == nil {
			results = append(results, strconv.Itoa(int(ms)))
		} else {
			results = append(results, err.Error())
		}
	}
	if mode&UdpTest != 0 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Millisecond)
		defer cancel()

		start := time.Now()
		pc, err := libbox.DialContext(ctx, i, "udp", "8.8.8.8:53")
		if err == nil {
			defer pc.Close()
			_ = pc.SetDeadline(time.Now().Add(time.Duration(timeout) * time.Millisecond))
			dnsPacket, _ := hex.DecodeString("0000010000010000000000000377777706676f6f676c6503636f6d0000010001")
			_, err = pc.Write(dnsPacket)
			if err == nil {
				var buf [1400]byte
				_, err = pc.Read(buf[:])
			}
		}
		if err == nil {
			results = append(results, fmt.Sprint(time.Since(start).Milliseconds()))
		} else {
			results = append(results, err.Error())
		}
	}
	if mode&SpeedTest != 0 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(speedTimeout))
		defer cancel()

		var n int64
		req, err := http.NewRequestWithContext(ctx, "GET", speedUrl, nil)
		start := time.Now()
		if err == nil {
			var resp *http.Response
			resp, err = httpClient.Do(req)
			if err == nil {
				defer resp.Body.Close()
				n, err = io.Copy(io.Discard, resp.Body)
			}
		}
		if err == nil {
			duration := math.Max(time.Since(start).Seconds(), 0.000001)
			results = append(results, fmt.Sprintf("%.2f", float64(n)/duration/MiB))
		} else {
			results = append(results, err.Error())
		}
	}
	if mode&IpTest != 0 {
		getBetweenStr := func(str, start, end string) string {
			n := strings.Index(str, start)
			if n == -1 {
				return ""
			}
			str = str[n+len(start):]
			m := strings.Index(str, end)
			if m == -1 {
				return str
			}
			return str[:m]
		}

		var in_ip string
		if host, _, err := net.SplitHostPort(address); err != nil {
			in_ip = err.Error()
		} else if ipaddr, err := net.ResolveIPAddr("ip", host); err != nil {
			in_ip = err.Error()
		} else {
			in_ip = ipaddr.String()
		}

		var out_ip string
		resp, err := httpClient.Get("https://www.cloudflare.com/cdn-cgi/trace")
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			out_ip = getBetweenStr(string(b), "ip=", "\n")
			resp.Body.Close()
		} else {
			out_ip = err.Error()
		}

		results = append(results, "In: "+in_ip+" / Out: "+out_ip)
	}
	return C.CString(strings.Join(results, "\n"))
}

//export BoxStats
func BoxStats() *C.char {
	if mainInstance != nil {
		return C.CString(mainInstance.QueryStats2JSON())
	}
	return nil
}
