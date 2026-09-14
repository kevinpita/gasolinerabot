package bot

import (
	"crypto/tls"
	"net/http"
	"time"
)

// NewPriceHTTPClient supports the legacy TLS 1.2 RSA key exchange used by the
// official MITECO endpoint. Go's default cipher set cannot connect to it.
// Keep this client separate from Telegram and other upstream clients. TLS 1.2+
// and normal certificate/hostname verification remain mandatory. RSA key
// exchange lacks forward secrecy, but this connection carries public prices
// only. Prefer modern ECDHE when the server supports it.
func NewPriceHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 35 * time.Second
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
		},
	}
	return &http.Client{Transport: transport, Timeout: 45 * time.Second}
}
