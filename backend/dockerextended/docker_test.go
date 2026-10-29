package dockerextended

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/client"
	"github.com/urfave/cli/v3"
)

func TestPullStream(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		bad        bool
	}{
		{"success", `{"status":"pulling"}
{"status":"complete"}`, false},
		{"late error", `{"status":"pulling"}
{"error":"unauthorized"}`, true},
		{"error detail", `{"errorDetail":{"code":401,"message":"secret"}}`, true},
		{"truncated", `{"status":`, true},
		{"malformed", `not-json`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := consumePull(context.Background(), strings.NewReader(tc.body))
			if (err != nil) != tc.bad {
				t.Fatal(err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("daemon error detail leaked")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := consumePull(ctx, strings.NewReader("")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestDockerConnectionAndPull(t *testing.T) {
	var images []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Registry-Auth") != "" {
			t.Error("mapped pull contained credentials")
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/json"):
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"not found"}`)
		case strings.HasSuffix(r.URL.Path, "/images/create"):
			images = append(images, r.URL.Query().Get("fromImage"))
			if strings.Contains(images[len(images)-1], "cache.example") {
				io.WriteString(w, `{"error":"unauthorized"}`)
			} else {
				io.WriteString(w, `{"status":"complete"}`)
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	app := &cli.Command{Flags: New().Flags(), Action: func(ctx context.Context, c *cli.Command) error {
		store, err := newDockerImages(c)
		if err != nil {
			return err
		}
		defer store.client.Close()
		hit, err := store.cached(ctx, "cache.example/library/alpine:3.22")
		if err != nil || hit {
			t.Fatalf("cached %v %v", hit, err)
		}
		image, err := prepareImage(ctx, store, "cache.example/library/alpine:3.22", "docker.io/library/alpine:3.22", true)
		if err != nil || image != "docker.io/library/alpine:3.22" {
			t.Fatalf("%s %v", image, err)
		}
		return nil
	}}
	if err := app.Run(context.Background(), []string{"test", "--backend-docker-host", server.URL, "--backend-docker-api-version", "1.56"}); err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 {
		t.Fatal(images)
	}
}
func TestInvalidTLSFailsClosed(t *testing.T) {
	app := &cli.Command{Flags: New().Flags(), Action: func(ctx context.Context, c *cli.Command) error {
		_, err := newDockerImages(c)
		return err
	}}
	if err := app.Run(context.Background(), []string{"test", "--backend-docker-cert", t.TempDir()}); err == nil {
		t.Fatal("invalid TLS accepted")
	}
}

// Compile-time API check alongside the client-independent acquisition tests.
var _ dockerImageClient = (*client.Client)(nil)

// WithHost must configure a plain transport even when client TLS is enabled.
func TestTLSWithCustomDockerHost(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("User-Agent"), "backend-docker-extended") {
			t.Error("custom user agent missing")
		}
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message":"not found"}`)
	}))
	defer server.Close()
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	key, err := x509.MarshalPKCS8PrivateKey(server.TLS.Certificates[0].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"ca.pem": cert, "cert.pem": cert,
		"key.pem": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	app := &cli.Command{Flags: New().Flags(), Action: func(ctx context.Context, c *cli.Command) error {
		store, err := newDockerImages(c)
		if err != nil {
			return err
		}
		defer store.client.Close()
		hit, err := store.cached(ctx, "cache.example/library/alpine:3.22")
		if err != nil || hit {
			t.Fatalf("TLS inspect: cached=%v err=%v", hit, err)
		}
		return nil
	}}
	if err := app.Run(t.Context(), []string{"test", "--backend-docker-host", server.URL,
		"--backend-docker-cert", dir, "--backend-docker-tls-verify", "--backend-docker-api-version", "1.56"}); err != nil {
		t.Fatal(err)
	}
}
