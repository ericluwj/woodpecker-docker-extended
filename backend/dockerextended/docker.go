package dockerextended

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"

	"github.com/containerd/errdefs"
	"github.com/docker/go-connections/tlsconfig"
	"github.com/moby/moby/client"
	"github.com/urfave/cli/v3"
	"go.woodpecker-ci.org/woodpecker/v3/shared/httputil"
)

// Match upstream v3.20.0's Docker host/API/TLS options. Docker daemon policy
// supplies mirror HTTPS trust; this client never reads Docker auth files.
func newDockerImages(c *cli.Command) (*dockerImages, error) {
	opts := []client.Opt{client.WithUserAgent(httputil.UserAgent("backend-docker-extended"))}
	if certPath := c.String("backend-docker-cert"); certPath != "" {
		tlsConf, err := tlsconfig.Client(tlsconfig.Options{
			CAFile: filepath.Join(certPath, "ca.pem"), CertFile: filepath.Join(certPath, "cert.pem"),
			KeyFile: filepath.Join(certPath, "key.pem"), InsecureSkipVerify: !c.Bool("backend-docker-tls-verify"),
		})
		if err != nil {
			return nil, fmt.Errorf("invalid Docker TLS configuration")
		}
		opts = append(opts, client.WithHTTPClient(&http.Client{
			Transport:     &http.Transport{TLSClientConfig: tlsConf},
			CheckRedirect: client.CheckRedirect,
		}))
	}
	if host := c.String("backend-docker-host"); host != "" {
		opts = append(opts, client.WithHost(host))
	}
	if version := c.String("backend-docker-api-version"); version != "" {
		opts = append(opts, client.WithAPIVersion(version))
	}
	cl, err := client.New(opts...)
	if err != nil {
		return nil, err
	}
	return &dockerImages{client: cl}, nil
}

type dockerImageClient interface {
	ImageInspect(context.Context, string, ...client.ImageInspectOption) (client.ImageInspectResult, error)
	ImagePull(context.Context, string, client.ImagePullOptions) (client.ImagePullResponse, error)
	Close() error
}
type dockerImages struct{ client dockerImageClient }

func (d *dockerImages) cached(ctx context.Context, image string) (bool, error) {
	_, err := d.client.ImageInspect(ctx, image)
	if errdefs.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}
func (d *dockerImages) pull(ctx context.Context, image string) error {
	body, err := d.client.ImagePull(ctx, image, client.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer body.Close()
	return consumePull(ctx, body)
}

// HTTP 200 can contain a failed layer/auth response. A pull is successful only
// after its complete JSON stream has been decoded without error.
func consumePull(ctx context.Context, body io.Reader) error {
	decoder := json.NewDecoder(body)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var message struct {
			Error       string `json:"error"`
			ErrorDetail *struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
			} `json:"errorDetail"`
		}
		err := decoder.Decode(&message)
		if err == io.EOF {
			return ctx.Err()
		}
		if err != nil {
			return fmt.Errorf("read Docker image pull stream: %w", err)
		}
		if message.Error != "" || message.ErrorDetail != nil {
			return fmt.Errorf("Docker daemon reported an image pull failure")
		}
	}
}
