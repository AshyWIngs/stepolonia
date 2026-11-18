// Package cmshttp provides the private, same-origin CMS authentication transport.
package cmshttp

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

const (
	originHeader       = "Origin"
	fetchSiteHeader    = "Sec-Fetch-Site"
	refererHeader      = "Referer"
	forwardedForHeader = "X-Forwarded-For"
	httpsScheme        = "https"
	sameOriginSite     = "same-origin"
)

// Config identifies the installation origin and explicitly trusted proxy peers.
type Config struct {
	CanonicalOrigin   string
	TrustedProxyCIDRs []string
}

type boundary struct {
	origin    string
	authority string
	proxies   []netip.Prefix
}

func newBoundary(cfg Config) (boundary, error) {
	u, err := url.Parse(cfg.CanonicalOrigin)
	if err != nil || u.Scheme != httpsScheme || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.ForceQuery {
		return boundary{}, errors.New("CMS requires one canonical HTTPS origin without path, credentials, query or fragment")
	}
	if !validAuthority(u) {
		return boundary{}, errors.New("invalid canonical CMS authority")
	}
	if !validPort(u) {
		return boundary{}, errors.New("invalid canonical CMS port")
	}
	b := boundary{origin: cfg.CanonicalOrigin, authority: u.Host}
	for _, raw := range cfg.TrustedProxyCIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return boundary{}, errors.New("invalid trusted CMS proxy CIDR")
		}
		b.proxies = append(b.proxies, prefix.Masked())
	}
	return b, nil
}

func (b boundary) clientIP(r *http.Request) (string, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "", false
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return "", false
	}
	trusted := false
	for _, prefix := range b.proxies {
		trusted = trusted || prefix.Contains(peer.Unmap())
	}
	if !trusted {
		return peer.Unmap().String(), r.TLS != nil
	}
	// The installation proxy must overwrite these headers with single values.
	forwarded := r.Header.Values(forwardedForHeader)
	scheme := r.Header.Values("X-Forwarded-Proto")
	if len(forwarded) != 1 || len(scheme) != 1 || scheme[0] != httpsScheme {
		return "", false
	}
	ip, err := netip.ParseAddr(forwarded[0])
	if err != nil {
		return "", false
	}
	return ip.Unmap().String(), true
}

func (b boundary) check(r *http.Request) (string, string) {
	ip, secure := b.clientIP(r)
	if !secure || r.Host != b.authority {
		return "", originCode
	}
	origins := r.Header.Values(originHeader)
	if !b.validRequestOrigin(r, origins) {
		return "", originCode
	}
	if (r.URL.RawQuery != "" || r.URL.ForceQuery) && (r.Method != http.MethodGet || (r.URL.Path != usersPath && r.URL.Path != rolesPath)) {
		return "", validationCode
	}
	return ip, ""
}

func validPort(u *url.URL) bool {
	if strings.HasSuffix(u.Host, ":") {
		return false
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		return err == nil && number >= 1 && number <= 65535
	}
	return true
}

func validAuthority(u *url.URL) bool {
	return u.Hostname() != "" && !strings.ContainsAny(u.Host, " \\%") && strings.ToLower(u.Host) == u.Host
}

func (b boundary) validRequestOrigin(r *http.Request, origins []string) bool {
	if len(origins) != 0 {
		return len(origins) == 1 && origins[0] == b.origin
	}
	if r.Method != http.MethodGet || !safeReadPath(r.URL.Path) {
		return false
	}
	sites, referers := r.Header.Values(fetchSiteHeader), r.Header.Values(refererHeader)
	if len(sites) != 1 || sites[0] != sameOriginSite || len(referers) != 1 {
		return false
	}
	ref, err := url.Parse(referers[0])
	return err == nil && ref.Scheme == httpsScheme && ref.Host == b.authority && ref.User == nil && ref.Opaque == "" && ref.Fragment == ""
}

func safeReadPath(raw string) bool {
	if raw == authPrefix+"/csrf" {
		return true
	}
	user, _ := userRoutePath(raw)
	if user == usersPath || user == userPath {
		return true
	}
	role, _ := roleRoutePath(raw)
	return isRoleRoute(role)
}
