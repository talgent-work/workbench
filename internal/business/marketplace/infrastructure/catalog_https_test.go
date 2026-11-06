//go:build integration

package infrastructure_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Native archive retrieval deliberately rejects loopback hosts. Use a private
// interface and a matching trusted certificate, without bypassing TLS or the
// native client's network restrictions.
func newCatalogHTTPSServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	addresses, err := net.InterfaceAddrs()
	require.NoError(t, err)
	var ip net.IP
	for _, address := range addresses {
		candidate, _, err := net.ParseCIDR(address.String())
		if err == nil && candidate.To4() != nil && candidate.IsPrivate() {
			ip = candidate
			break
		}
	}
	require.NotNil(t, ip, "native archive fixture requires a private network interface")
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true, IPAddresses: []net.IP{ip},
	}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	listener, err := net.Listen("tcp", net.JoinHostPort(ip.String(), "0"))
	require.NoError(t, err)
	server := httptest.NewUnstartedServer(handler)
	require.NoError(t, server.Listener.Close())
	server.Listener = listener
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{certificate}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}
