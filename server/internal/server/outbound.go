package server

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// errPrivateAddress is returned when a webhook URL resolves to an address
// that isn't on the public internet.
var errPrivateAddress = errors.New("the address is not on the public internet")

// cgnat is the shared address space carriers (and some cloud networks) use
// internally; netip has no predicate for it.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// isPublicAddr reports whether addr is somewhere on the public internet,
// rather than this machine or a private network.
func isPublicAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsValid() && addr.IsGlobalUnicast() && !addr.IsPrivate() && !cgnat.Contains(addr)
}

// newOutboundClient returns the HTTP client used to call URLs that users
// supply (webhooks). Unless allowPrivate is set it refuses to connect to
// loopback, link-local and private addresses, so a webhook can't be pointed
// at the server's own network. The check runs on the address actually
// dialed, after DNS resolution, which also covers DNS rebinding.
func newOutboundClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	if !allowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			addrPort, err := netip.ParseAddrPort(address)
			if err != nil || !isPublicAddr(addrPort.Addr()) {
				return errPrivateAddress
			}
			return nil
		}
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			// No proxy: the dial check must see the real destination.
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			MaxIdleConnsPerHost:   2,
			IdleConnTimeout:       30 * time.Second,
		},
		// A redirect is the receiver's answer, not somewhere else to send data.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
