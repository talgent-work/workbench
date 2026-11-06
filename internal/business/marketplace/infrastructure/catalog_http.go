package infrastructure

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

const catalogArchiveLimit int64 = 256 << 20

func catalogHTTPClient(config CatalogAcquisitionConfig) (*http.Client, error) {
	transport := &http.Transport{MaxIdleConns: 16, MaxIdleConnsPerHost: 8, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil {
			return nil, e
		}
		for _, ip := range ips {
			if ip.IP.IsLoopback() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsLinkLocalMulticast() || ip.IP.IsUnspecified() || ip.IP.IsMulticast() {
				return nil, errors.New("marketplace: unsafe acquisition address")
			}
		}
		if len(ips) == 0 {
			return nil, errors.New("marketplace: acquisition host has no address")
		}
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
	if config.CertificateAuthority != "" {
		body, e := os.ReadFile(config.CertificateAuthority)
		if e != nil {
			return nil, e
		}
		pool, e := x509.SystemCertPool()
		if e != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(body) {
			return nil, errors.New("marketplace: invalid certificate authority")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: transport, Timeout: time.Duration(config.RequestTimeoutSeconds) * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if !catalogHTTPS(req.URL) || len(via) >= 5 {
			return errors.New("marketplace: unsafe acquisition redirect")
		}
		if len(via) > 0 && req.URL.Host != via[0].URL.Host {
			req.Header = make(http.Header)
		}
		return nil
	}}, nil
}
func catalogHTTPS(u *url.URL) bool {
	return u != nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}
func (a *catalogAcquisition) response(ctx context.Context, address string, headers map[string]string) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if e != nil || !catalogHTTPS(req.URL) {
		return nil, errors.New("marketplace: acquisition URL must use HTTPS without user information")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	response, e := a.http.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("marketplace: download failed")
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		if response.StatusCode == http.StatusNotFound {
			return nil, os.ErrNotExist
		}
		return nil, errors.New("marketplace: download request failed")
	}
	return response, nil
}
func (a *catalogAcquisition) readHTTP(ctx context.Context, address string, headers map[string]string, limit int64) ([]byte, error) {
	response, e := a.response(ctx, address, headers)
	if e != nil {
		return nil, e
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(body)) > limit {
		return nil, errors.New("marketplace: metadata exceeds size limit")
	}
	return body, nil
}
func (a *catalogAcquisition) download(ctx context.Context, address string, headers map[string]string, file, hash io.Writer) error {
	response, e := a.response(ctx, address, headers)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	n, e := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, catalogArchiveLimit+1))
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("marketplace: incomplete package download")
	}
	if n > catalogArchiveLimit {
		return errors.New("marketplace: package download exceeds size limit")
	}
	return nil
}
