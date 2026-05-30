package tunnel

import "testing"

func TestALPNForProtocol(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"postgres", alpnPostgres, false},
		{"postgresql", alpnPostgres, false},
		{"POSTGRES", alpnPostgres, false},
		{"mysql", alpnMySQL, false},
		{"mariadb", alpnMySQL, false},
		{"mongodb", alpnMongoDB, false},
		{"redis", alpnRedis, false},
		{"sqlserver", alpnSQLServer, false},
		{"snowflake", alpnSnowflake, false},
		{"cassandra", alpnCassandra, false},
		{"elasticsearch", alpnElasticsearch, false},
		{"oracle", alpnOracle, false},
		{"spanner", alpnSpanner, false},
		{"cockroachdb", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ALPNForProtocol(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ALPNForProtocol(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNewDBTunnelValidation(t *testing.T) {
	tests := []struct {
		name string
		opts DBOptions
	}{
		{"no proxy", DBOptions{Protocol: "postgres", ClientCertPEM: []byte("x"), ClientKeyPEM: []byte("y")}},
		{"no protocol", DBOptions{ProxyAddress: "p:443", ClientCertPEM: []byte("x"), ClientKeyPEM: []byte("y")}},
		{"no cert", DBOptions{ProxyAddress: "p:443", Protocol: "postgres"}},
		{"bad protocol", DBOptions{ProxyAddress: "p:443", Protocol: "nope", ClientCertPEM: []byte("x"), ClientKeyPEM: []byte("y")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewDBTunnel(t.Context(), tt.opts); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
