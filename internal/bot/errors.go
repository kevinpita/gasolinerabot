package bot

import (
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"

	"github.com/jackc/pgx/v5/pgconn"
)

// Operation names are fixed strings in code. Never include raw upstream errors,
// response bodies, request URLs, message contents, or database values in logs.
type operationError struct {
	Operation string
	Cause     error
}

func (e *operationError) Error() string           { return e.Operation + ": " + safeError(e.Cause) }
func (e *operationError) Unwrap() error           { return e.Cause }
func failure(operation string, cause error) error { return &operationError{operation, cause} }
func classifiedError(err error) string {
	var db *pgconn.PgError
	if errors.As(err, &db) {
		if len(db.Code) == 5 {
			valid := true
			for _, c := range db.Code {
				if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
					valid = false
				}
			}
			if valid {
				return fmt.Sprintf("postgres_sqlstate_%s", db.Code)
			}
		}
		return "postgres_error"
	}
	var cert x509.UnknownAuthorityError
	if errors.As(err, &cert) {
		return "untrusted_certificate"
	}
	var host x509.HostnameError
	if errors.As(err, &host) {
		return "certificate_hostname_mismatch"
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return "network_timeout"
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "connection_closed"
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return "connection_reset"
	}
	return "request or storage operation failed"
}
