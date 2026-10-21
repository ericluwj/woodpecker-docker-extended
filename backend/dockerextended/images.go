// Reference normalization adapted from ericluwj/woodpecker-apple-container,
// backend/apple/images.go (MIT, Copyright 2026 Eric Lu).
package dockerextended

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/distribution/reference"
	"github.com/rs/zerolog/log"
)

func parseRegistryMirrors(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("registry mirrors must be a JSON object of registry hosts")
	}
	mirrors := make(map[string]string)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("invalid registry mirror configuration")
		}
		source, ok := token.(string)
		var target string
		if !ok || decoder.Decode(&target) != nil {
			return nil, fmt.Errorf("registry mirrors require string host values")
		}
		source, target = strings.ToLower(source), strings.ToLower(target)
		if !validRegistryHost(source) || !validRegistryHost(target) {
			return nil, fmt.Errorf("registry mirrors require host[:port] values without schemes, credentials or paths")
		}
		if source == "index.docker.io" {
			source = "docker.io"
		}
		if _, exists := mirrors[source]; exists {
			return nil, fmt.Errorf("registry mirrors contain duplicate normalized source hosts")
		}
		mirrors[source] = target
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("invalid registry mirror configuration")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("invalid trailing registry mirror configuration")
	}

	return mirrors, nil
}

func validRegistryHost(host string) bool {
	if host == "" || strings.ContainsAny(host, "/@?# \t\r\n\x00") {
		return false
	}
	named, err := reference.ParseNormalizedNamed(host + "/mirror-probe")
	return err == nil && (reference.Domain(named) == host || host == "index.docker.io")
}

func resolveImage(image string, mirrors map[string]string) (mirror, origin string, err error) {
	if len(mirrors) == 0 {
		return image, image, nil
	}
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return "", "", fmt.Errorf("invalid image reference for registry cache resolution")
	}
	domain := reference.Domain(named)
	target, ok := mirrors[domain]
	if !ok || target == domain {
		return image, image, nil
	}
	return target + strings.TrimPrefix(named.String(), domain), named.String(), nil
}

type imageStore interface {
	cached(context.Context, string) (bool, error)
	pull(context.Context, string) error
}

func prepareImage(ctx context.Context, store imageStore, mirror, origin string, force bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !force {
		hit, err := store.cached(ctx, mirror)
		if err != nil {
			return "", err
		}
		if hit {
			return mirror, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	mirrorErr := store.pull(ctx, mirror)
	if mirrorErr == nil {
		return mirror, nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if errors.Is(mirrorErr, context.Canceled) || errors.Is(mirrorErr, context.DeadlineExceeded) {
		return "", mirrorErr
	}
	log.Warn().Str("mirror", mirror).Str("origin", origin).Msg("mirror image acquisition failed; trying origin")
	if !force {
		hit, err := store.cached(ctx, origin)
		if err != nil {
			return "", err
		}
		if hit {
			return origin, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := store.pull(ctx, origin); err != nil {
		return "", errors.Join(fmt.Errorf("mirror image pull failed: %w", mirrorErr), fmt.Errorf("origin image pull failed: %w", err))
	}
	return origin, nil
}
