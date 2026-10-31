// Package imageref extracts a git revision from the places a deploy usually
// carries it: the image tag, pod-template annotations, or labels
// (OPEN_QUESTIONS.md #9). All three are supported; the tag convention is the
// one we document first.
package imageref

import (
	"regexp"
	"strings"
)

// Annotation and label keys consulted, in priority order.
var (
	RevisionAnnotations = []string{
		"hoofprint.dev/revision",
		"org.opencontainers.image.revision",
	}
	RevisionLabels = []string{
		"hoofprint.dev/revision",
		"app.kubernetes.io/version",
	}
)

// A hex run of 7 to 40 chars, delimited by start/end or by non-hex characters.
var shaRe = regexp.MustCompile(`(?i)(?:^|[^0-9a-f])([0-9a-f]{7,40})(?:$|[^0-9a-f])`)

// Parse splits an image reference into repository, tag and digest.
// Examples:
//
//	ghcr.io/acme/api:sha-1a2b3c4 -> ghcr.io/acme/api, sha-1a2b3c4, ""
//	api@sha256:abcd...           -> api, "", sha256:abcd...
//	localhost:5000/api:v1        -> localhost:5000/api, v1, ""
func Parse(image string) (repo, tag, digest string) {
	repo = image
	if i := strings.Index(repo, "@"); i >= 0 {
		digest = repo[i+1:]
		repo = repo[:i]
	}
	// The tag separator is the last ':' after the last '/', so registry ports
	// are not mistaken for tags.
	slash := strings.LastIndex(repo, "/")
	if colon := strings.LastIndex(repo, ":"); colon > slash {
		tag = repo[colon+1:]
		repo = repo[:colon]
	}
	return repo, tag, digest
}

// RevisionFromTag returns the git SHA embedded in an image tag, or "". It
// accepts bare SHAs (abc1234, full 40-hex) and prefixed/suffixed forms such as
// sha-abc1234, git-abc1234, v1.2.3-abc1234, main-abc1234-amd64. Tags that look
// like plain versions (v1.2.3, latest) yield "".
func RevisionFromTag(tag string) string {
	if tag == "" {
		return ""
	}
	m := shaRe.FindStringSubmatch(tag)
	if m == nil {
		return ""
	}
	sha := strings.ToLower(m[1])
	// A run that is all digits (e.g. a build number 20240101) is not a SHA.
	if strings.Trim(sha, "0123456789") == "" {
		return ""
	}
	return sha
}

// Revision picks the best revision available for a workload: annotations
// first (explicit beats inferred), then labels, then the image tags in order.
func Revision(annotations, labels map[string]string, images []string) string {
	for _, k := range RevisionAnnotations {
		if v := annotations[k]; v != "" {
			return v
		}
	}
	for _, k := range RevisionLabels {
		if v := labels[k]; v != "" {
			if sha := RevisionFromTag(v); sha != "" {
				return sha
			}
		}
	}
	for _, img := range images {
		_, tag, _ := Parse(img)
		if sha := RevisionFromTag(tag); sha != "" {
			return sha
		}
	}
	return ""
}
