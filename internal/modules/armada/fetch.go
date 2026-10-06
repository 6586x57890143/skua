// SPDX-License-Identifier: AGPL-3.0-only
// A modified Go port of armada-discord-bridge; see NOTICE in this directory.

package armada

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// fetchBudget covers a whole guarded fetch: DNS, every redirect and the
// body. Without it a server that dribbles a byte a minute holds the socket
// for as long as it likes; the byte cap bounds bytes, not time.
const fetchBudget = 30 * time.Second

// blocked is every range a URL from outside must not reach beyond the
// private, loopback, link-local and multicast ones netip knows: shared
// address space, this-network, the protocol-assignment and benchmarking
// blocks, the reserved block, and NAT64 and 6to4, which embed an IPv4
// address of the sender's choosing.
var blocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("2002::/16"),
}

var errBlocked = errors.New("armada: that address is not on the public internet")

// public is whether a may be fetched. It is judged by value after
// unmapping, so ::ffff:127.0.0.1 is loopback however it was spelled.
func public(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsMulticast() || a.IsUnspecified() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// guarded fetches URLs that came from outside the trust boundary: imeta
// tags on rumors and attachment links. The check runs on the address
// actually being connected to, in the dialer, so it holds on every redirect
// hop and against a name that resolves public once and private the next
// time. No proxy from the environment is used, since it would connect
// somewhere the check never saw.
var guarded = &http.Client{
	Timeout: fetchBudget,
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
			Control: func(_, address string, _ syscall.RawConn) error {
				ap, err := netip.ParseAddrPort(address)
				if err != nil || !public(ap.Addr()) {
					return errBlocked
				}
				return nil
			},
		}).DialContext,
		ForceAttemptHTTP2:     true,
		ResponseHeaderTimeout: 15 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("armada: too many redirects")
		}
		return httpOnly(req)
	},
}

func httpOnly(req *http.Request) error {
	if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
		return fmt.Errorf("armada: refusing a %s URL", req.URL.Scheme)
	}
	return nil
}

var errTooBig = errors.New("armada: the file is over the size cap")

// fetch downloads raw through the guard, refusing more than limit bytes,
// and returns the body and its served type.
func fetch(ctx context.Context, client *http.Client, raw string, limit int64) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", err
	}
	if err := httpOnly(req); err != nil {
		return nil, "", err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode/100 != 2 {
		return nil, "", fmt.Errorf("armada: fetch answered %s", res.Status)
	}
	if res.ContentLength > limit {
		return nil, "", errTooBig
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > limit {
		return nil, "", errTooBig
	}
	served, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if served == "" {
		served = "application/octet-stream"
	}
	return body, served, nil
}
