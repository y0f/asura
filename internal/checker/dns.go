package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/y0f/asura/internal/safenet"
	"github.com/y0f/asura/internal/storage"
)

type DNSChecker struct {
	AllowPrivate bool
}

func (c *DNSChecker) Type() string { return "dns" }

func (c *DNSChecker) Check(ctx context.Context, monitor *storage.Monitor) (*Result, error) {
	var settings storage.DNSSettings
	if len(monitor.Settings) > 0 {
		if err := json.Unmarshal(monitor.Settings, &settings); err != nil {
			return &Result{Status: "down", Message: fmt.Sprintf("invalid settings: %v", err)}, nil
		}
	}

	recordType := settings.RecordType
	if recordType == "" {
		recordType = "A"
	}

	baseDial := (&net.Dialer{
		Timeout: time.Duration(monitor.Timeout) * time.Second,
		Control: safenet.MaybeDialControl(c.AllowPrivate),
	}).DialContext

	dialFn := baseDial
	if socks := ProxyDialer(monitor.ProxyURL, baseDial, c.AllowPrivate); socks != nil {
		dialFn = socks
	}

	resolver := net.DefaultResolver
	if settings.Server != "" {
		resolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				if monitor.ProxyURL != "" {
					network = "tcp"
				}
				return dialFn(ctx, network, settings.Server+":53")
			},
		}
	}

	start := time.Now()
	records, supported, err := lookupDNSRecords(ctx, resolver, recordType, monitor.Target)
	elapsed := time.Since(start).Milliseconds()

	if !supported {
		return &Result{
			Status:  "down",
			Message: fmt.Sprintf("unsupported record type: %s", recordType),
		}, nil
	}

	if err != nil {
		return &Result{
			Status:       "down",
			ResponseTime: elapsed,
			Message:      fmt.Sprintf("DNS lookup failed: %v", err),
		}, nil
	}

	if len(records) == 0 {
		return &Result{
			Status:       "down",
			ResponseTime: elapsed,
			Message:      "no records found",
		}, nil
	}

	return &Result{
		Status:       "up",
		ResponseTime: elapsed,
		DNSRecords:   records,
		Message:      fmt.Sprintf("found %d record(s)", len(records)),
	}, nil
}

func lookupDNSRecords(ctx context.Context, resolver *net.Resolver, recordType, target string) (records []string, supported bool, err error) {
	switch recordType {
	case "A":
		var addrs []net.IP
		addrs, err = resolver.LookupIP(ctx, "ip4", target)
		for _, a := range addrs {
			records = append(records, a.String())
		}
	case "AAAA":
		var addrs []net.IP
		addrs, err = resolver.LookupIP(ctx, "ip6", target)
		for _, a := range addrs {
			records = append(records, a.String())
		}
	case "CNAME":
		var cname string
		cname, err = resolver.LookupCNAME(ctx, target)
		if cname != "" {
			records = append(records, cname)
		}
	case "MX":
		var mxs []*net.MX
		mxs, err = resolver.LookupMX(ctx, target)
		for _, mx := range mxs {
			records = append(records, fmt.Sprintf("%d %s", mx.Pref, mx.Host))
		}
	case "TXT":
		records, err = resolver.LookupTXT(ctx, target)
	case "NS":
		var nss []*net.NS
		nss, err = resolver.LookupNS(ctx, target)
		for _, ns := range nss {
			records = append(records, ns.Host)
		}
	default:
		return nil, false, nil
	}
	return records, true, err
}
