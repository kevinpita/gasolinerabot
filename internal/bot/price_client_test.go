package bot

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Reproduce the public endpoint's TLS 1.2 configuration without external access.
func TestPriceClientLegacyTLS(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"Fecha":"14/09/2026 08:00:00","ListaEESSPrecio":[{"IDEESS":"1","Latitud":"40,0","Longitud (WGS84)":"-3,0","Precio Gasolina 95 E5":"1,599"}]}`)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12, CipherSuites: []uint16{tls.TLS_RSA_WITH_AES_128_GCM_SHA256}}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	oldTransport := http.DefaultTransport.(*http.Transport).Clone()
	oldTransport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	old := PriceSource{HTTP: &http.Client{Transport: oldTransport}, URL: server.URL}
	if _, err := old.Fetch(t.Context()); err == nil {
		t.Fatal("default Go client unexpectedly supports the legacy-only server")
	}
	client := NewPriceHTTPClient()
	transport := client.Transport.(*http.Transport)
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("certificate verification disabled")
	}
	if transport.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.Fatal("TLS version below 1.2")
	}
	transport.TLSClientConfig.RootCAs = roots
	fixed := PriceSource{HTTP: client, URL: server.URL}
	snap, err := fixed.Fetch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Stations) != 1 {
		t.Fatal("feed not parsed")
	}
	// A separate client must still reject an untrusted certificate.
	untrusted := PriceSource{HTTP: NewPriceHTTPClient(), URL: server.URL}
	if _, err := untrusted.Fetch(t.Context()); err == nil {
		t.Fatal("untrusted certificate accepted")
	}
}
