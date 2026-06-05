package main

import (
	"net"
	"testing"

	"github.com/256dpi/newdns"
	"github.com/miekg/dns"
)

func startUDPServer(t *testing.T, handler dns.Handler) string {
	t.Helper()

	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}

	server := &dns.Server{
		Net:        "udp",
		PacketConn: packetConn,
		Handler:    handler,
	}

	go func() {
		_ = server.ActivateAndServe()
	}()

	t.Cleanup(func() {
		_ = server.Shutdown()
	})

	return packetConn.LocalAddr().String()
}

func TestProxyRouterForwardsOriginalQuestionToUpstream(t *testing.T) {
	upstreamAddr := startUDPServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		res := new(dns.Msg)
		res.SetReply(req)

		if req.Question[0].Qtype == dns.TypeAAAA {
			res.Answer = append(res.Answer, &dns.AAAA{
				Hdr: dns.RR_Header{
					Name:   req.Question[0].Name,
					Rrtype: dns.TypeAAAA,
					Class:  dns.ClassINET,
					Ttl:    60,
				},
				AAAA: net.ParseIP("2001:db8::1"),
			})
		}

		_ = w.WriteMsg(res)
	}))

	state.mutex.Lock()
	oldProxyFallback := state.proxyFallback
	oldUpstreamDNS := state.upstreamDNS
	oldZoneMap := state.zoneMap
	state.proxyFallback = true
	state.upstreamDNS = upstreamAddr
	state.zoneMap = map[string]*newdns.Zone{}
	state.mutex.Unlock()

	t.Cleanup(func() {
		state.mutex.Lock()
		state.proxyFallback = oldProxyFallback
		state.upstreamDNS = oldUpstreamDNS
		state.zoneMap = oldZoneMap
		state.mutex.Unlock()
	})

	authoritative := newdns.NewServer(newdns.Config{
		Handler: func(name string) (*newdns.Zone, error) {
			return nil, nil
		},
	})

	routerAddr := startUDPServer(t, proxyRouter{
		authoritative: authoritative,
	})

	req := new(dns.Msg)
	req.SetQuestion("example.org.", dns.TypeAAAA)

	res, _, err := new(dns.Client).Exchange(req, routerAddr)
	if err != nil {
		t.Fatalf("query router: %v", err)
	}

	if len(res.Answer) != 1 {
		t.Fatalf("expected one proxied answer, got %d", len(res.Answer))
	}

	aaaa, ok := res.Answer[0].(*dns.AAAA)
	if !ok {
		t.Fatalf("expected AAAA answer, got %T", res.Answer[0])
	}

	if got := aaaa.AAAA.String(); got != "2001:db8::1" {
		t.Fatalf("expected proxied AAAA address, got %s", got)
	}
}
