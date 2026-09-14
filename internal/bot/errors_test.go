package bot

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestSafeOperationDiagnostics(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{failure("price_feed_fetch", failure("upstream_http", io.EOF)), "price_feed_fetch: upstream_http: connection_closed"},
		{failure("price_history_write", &pgconn.PgError{Code: "23514", Message: "secret-token", Detail: "private user data"}), "price_history_write: postgres_sqlstate_23514"},
		{failure("price_feed_fetch", errors.New("secret-token")), "price_feed_fetch: request or storage operation failed"},
	}
	for _, test := range tests {
		got := safeError(test.err)
		if got != test.want {
			t.Errorf("got %q, want %q", got, test.want)
		}
		if strings.Contains(got, "secret-token") || strings.Contains(got, "private user data") {
			t.Fatal("private data leaked")
		}
	}
}
