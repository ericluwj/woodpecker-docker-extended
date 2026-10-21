// Resolution/configuration cases adapted from ericluwj/woodpecker-apple-container
// backend/apple/images_test.go (MIT, Copyright 2026 Eric Lu).
package dockerextended

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const testMirrorsJSON = `{"docker.io":"docker-cache.example","registry.k8s.io":"k8s-cache.example","gcr.io":"gcr-cache.example","ghcr.io":"ghcr-cache.example"}`

func TestRegistryMirrorResolution(t *testing.T) {
	mirrors, err := parseRegistryMirrors(testMirrorsJSON)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct{ image, want string }{
		{"alpine", "docker-cache.example/library/alpine"},
		{"alpine:3.22", "docker-cache.example/library/alpine:3.22"},
		{"library/alpine:3.22", "docker-cache.example/library/alpine:3.22"},
		{"docker.io/alpine:3.22", "docker-cache.example/library/alpine:3.22"},
		{"index.docker.io/library/alpine:3.22", "docker-cache.example/library/alpine:3.22"},
		{"woodpeckerci/plugin-git:2.10.0", "docker-cache.example/woodpeckerci/plugin-git:2.10.0"},
		{"docker.io/woodpeckerci/plugin-git:2.10.0", "docker-cache.example/woodpeckerci/plugin-git:2.10.0"},
		{"registry.k8s.io/pause:3.10", "k8s-cache.example/pause:3.10"},
		{"gcr.io/project/image:tag", "gcr-cache.example/project/image:tag"},
		{"ghcr.io/org/image:tag", "ghcr-cache.example/org/image:tag"},
		{"alpine@" + digest, "docker-cache.example/library/alpine@" + digest},
		{"docker.io/library/alpine:3.22@" + digest, "docker-cache.example/library/alpine:3.22@" + digest},
		{"quay.io/woodpeckerci/plugin-git:2.10.0", "quay.io/woodpeckerci/plugin-git:2.10.0"},
		{"private.example/private/image:tag", "private.example/private/image:tag"},
		{"localhost:5000/image:tag", "localhost:5000/image:tag"},
		{"docker-cache.example/library/alpine:3.22", "docker-cache.example/library/alpine:3.22"},
	} {
		t.Run(tc.image, func(t *testing.T) {
			got, _, err := resolveImage(tc.image, mirrors)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	for _, image := range []string{"https://user:DO-NOT-LOG-ME@docker.io/alpine", "-bad", "alpine\nsecret"} {
		_, _, err := resolveImage(image, mirrors)
		if err == nil || strings.Contains(err.Error(), "DO-NOT-LOG-ME") {
			t.Fatal("invalid reference accepted or sensitive input disclosed")
		}
	}
	// No opt-in means no normalization or validation change to existing users.
	mirrors = nil
	for _, image := range []string{"alpine", "index.docker.io/library/alpine", "legacy-reference"} {
		got, _, err := resolveImage(image, mirrors)
		if err != nil || got != image {
			t.Fatal("unconfigured behavior changed")
		}
	}
}

func TestRegistryMirrorConfig(t *testing.T) {
	for _, raw := range []string{"", " ", "{}"} {
		got, err := parseRegistryMirrors(raw)
		if err != nil || len(got) != 0 {
			t.Fatalf("empty config: %v, %v", got, err)
		}
	}
	got, err := parseRegistryMirrors(`{"INDEX.DOCKER.IO":"cache.example:443"}`)
	if err != nil || !reflect.DeepEqual(got, map[string]string{"docker.io": "cache.example:443"}) {
		t.Fatalf("alias/port config: %v, %v", got, err)
	}
	for _, raw := range []string{
		`null`, `[]`, `true`, `{"docker.io":"cache.example","docker.io":"other.example"}`, `{} {}`, `{"docker.io":`, `{"docker.io":42}`, `invalid-json`,
		`{"docker.io":"https://cache.example"}`,
		`{"docker.io":"http://cache.example"}`,
		`{"docker.io":"user:DO-NOT-LOG-ME@cache.example"}`,
		`{"docker.io":"cache.example/path"}`,
		`{"docker.io":"cache.example?secret=DO-NOT-LOG-ME"}`,
		`{"docker.io":"cache.example#secret"}`,
		`{"docker.io":" cache.example"}`,
		`{"docker.io":"cache.example\n"}`,
		`{"docker.io":""}`, `{"docker.io":"cache"}`,
		`{"docker.io/path":"cache.example"}`,
		`{"docker.io":"cache.example","index.docker.io":"other.example"}`,
	} {
		_, err := parseRegistryMirrors(raw)
		if err == nil || strings.Contains(err.Error(), "DO-NOT-LOG-ME") {
			t.Fatal("invalid mapping accepted or sensitive input disclosed")
		}
	}
}

type fakeStore struct {
	cache       map[string]bool
	pullErrors  map[string]error
	inspectErr  error
	pulls       []string
	inspections []string
	onPull      func(string)
}

func (s *fakeStore) cached(ctx context.Context, image string) (bool, error) {
	s.inspections = append(s.inspections, image)
	return s.cache[image], s.inspectErr
}
func (s *fakeStore) pull(ctx context.Context, image string) error {
	s.pulls = append(s.pulls, image)
	if s.onPull != nil {
		s.onPull(image)
	}
	return s.pullErrors[image]
}
func TestAcquisition(t *testing.T) {
	failure := errors.New("unavailable")
	for _, tc := range []struct {
		name    string
		cache   map[string]bool
		force   bool
		errs    map[string]error
		want    string
		pulls   []string
		wantErr bool
	}{
		{name: "cached mirror", cache: map[string]bool{"mirror": true}, want: "mirror"},
		{name: "cold mirror", want: "mirror", pulls: []string{"mirror"}},
		{name: "forced mirror", cache: map[string]bool{"mirror": true}, force: true, want: "mirror", pulls: []string{"mirror"}},
		{name: "fallback", errs: map[string]error{"mirror": failure}, want: "origin", pulls: []string{"mirror", "origin"}},
		{name: "cached origin fallback", cache: map[string]bool{"origin": true}, errs: map[string]error{"mirror": failure}, want: "origin", pulls: []string{"mirror"}},
		{name: "forced origin fallback", cache: map[string]bool{"origin": true}, force: true, errs: map[string]error{"mirror": failure}, want: "origin", pulls: []string{"mirror", "origin"}},
		{name: "both fail", errs: map[string]error{"mirror": failure, "origin": failure}, pulls: []string{"mirror", "origin"}, wantErr: true},
		{name: "pull deadline", errs: map[string]error{"mirror": context.DeadlineExceeded}, pulls: []string{"mirror"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &fakeStore{cache: tc.cache, pullErrors: tc.errs}
			image, err := prepareImage(context.Background(), s, "mirror", "origin", tc.force)
			if (err != nil) != tc.wantErr || image != tc.want || !reflect.DeepEqual(s.pulls, tc.pulls) {
				t.Fatalf("image=%q err=%v pulls=%v", image, err, s.pulls)
			}
		})
	}
}
func TestAcquisitionCancellationAndInspectFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &fakeStore{pullErrors: map[string]error{"mirror": errors.New("fail")}, onPull: func(string) { cancel() }}
	if _, err := prepareImage(ctx, s, "mirror", "origin", false); !errors.Is(err, context.Canceled) || len(s.pulls) != 1 {
		t.Fatalf("%v %v", err, s.pulls)
	}
	s = &fakeStore{}
	if _, err := prepareImage(ctx, s, "mirror", "origin", true); !errors.Is(err, context.Canceled) || len(s.pulls) != 0 {
		t.Fatal("cancelled operation pulled")
	}
	s = &fakeStore{inspectErr: errors.New("daemon unavailable")}
	if _, err := prepareImage(context.Background(), s, "mirror", "origin", false); err == nil || len(s.pulls) > 0 {
		t.Fatal("inspect error ignored")
	}
}
func TestFallbackPreservesDigest(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	mirror, origin, err := resolveImage("alpine:3.22@"+digest, map[string]string{"docker.io": "cache.example"})
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeStore{pullErrors: map[string]error{mirror: errors.New("down")}}
	got, err := prepareImage(context.Background(), s, mirror, origin, true)
	if err != nil || got != "docker.io/library/alpine:3.22@"+digest {
		t.Fatalf("%q %v", got, err)
	}
}
