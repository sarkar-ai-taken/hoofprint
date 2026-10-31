package imageref

import "testing"

func TestParse(t *testing.T) {
	cases := []struct{ in, repo, tag, digest string }{
		{"ghcr.io/acme/api:sha-1a2b3c4", "ghcr.io/acme/api", "sha-1a2b3c4", ""},
		{"api@sha256:deadbeef", "api", "", "sha256:deadbeef"},
		{"localhost:5000/api:v1", "localhost:5000/api", "v1", ""},
		{"localhost:5000/api", "localhost:5000/api", "", ""},
		{"nginx", "nginx", "", ""},
		{"ghcr.io/acme/api:v1@sha256:abc", "ghcr.io/acme/api", "v1", "sha256:abc"},
	}
	for _, c := range cases {
		repo, tag, digest := Parse(c.in)
		if repo != c.repo || tag != c.tag || digest != c.digest {
			t.Errorf("Parse(%q) = %q %q %q, want %q %q %q", c.in, repo, tag, digest, c.repo, c.tag, c.digest)
		}
	}
}

func TestRevisionFromTag(t *testing.T) {
	cases := map[string]string{
		"":                   "",
		"latest":             "",
		"v1.2.3":             "",
		"20240101":           "",
		"1a2b3c4":            "1a2b3c4",
		"sha-1a2b3c4":        "1a2b3c4",
		"git-ABCDEF1":        "abcdef1",
		"v1.2.3-9f8e7d6":     "9f8e7d6",
		"main-9f8e7d6-amd64": "9f8e7d6",
		"0123456789abcdef0123456789abcdef01234567": "0123456789abcdef0123456789abcdef01234567",
		"deadbee": "deadbee", // 7 hex chars, accepted by design
		"abc12":   "",        // too short
	}
	for in, want := range cases {
		if got := RevisionFromTag(in); got != want {
			t.Errorf("RevisionFromTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRevision(t *testing.T) {
	imgs := []string{"nginx:1.25", "ghcr.io/acme/api:sha-1a2b3c4"}
	if got := Revision(nil, nil, imgs); got != "1a2b3c4" {
		t.Errorf("tag fallback: %q", got)
	}
	ann := map[string]string{"org.opencontainers.image.revision": "fffffff"}
	if got := Revision(ann, nil, imgs); got != "fffffff" {
		t.Errorf("annotation should win: %q", got)
	}
	lbl := map[string]string{"app.kubernetes.io/version": "v2-eeeeeee"}
	if got := Revision(nil, lbl, imgs); got != "eeeeeee" {
		t.Errorf("label should beat tag: %q", got)
	}
	if got := Revision(nil, map[string]string{"app.kubernetes.io/version": "v2"}, []string{"api:latest"}); got != "" {
		t.Errorf("no revision anywhere: %q", got)
	}
}
