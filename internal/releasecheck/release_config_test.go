package releasecheck

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The release workflow attests the container image by name, and that
// name is written in two places: dockers_v2.images in .goreleaser.yaml,
// and the digest-resolution step in release.yml. If they drift, the
// workflow attests an image that was never pushed — or silently attests
// the wrong one. Neither shows up until a release runs.
func TestWorkflowImageMatchesGoreleaser(t *testing.T) {
	goreleaser := read(t, "../../.goreleaser.yaml")
	workflow := read(t, "../../.github/workflows/release.yml")

	grImage := firstMatch(t, goreleaser,
		`(?m)^dockers_v2:\n\s+- images:\n\s+- (\S+)`, "dockers_v2.images in .goreleaser.yaml")
	wfImage := firstMatch(t, workflow,
		`(?m)^\s+image="(\S+)"`, "image= in the release workflow")

	if grImage != wfImage {
		t.Errorf("image name drifted:\n  .goreleaser.yaml: %s\n  release.yml:      %s", grImage, wfImage)
	}
}

// A prerelease must never claim :latest — anyone pulling the unqualified
// image would get a release candidate.
func TestLatestTagIsConditionalOnPrerelease(t *testing.T) {
	goreleaser := read(t, "../../.goreleaser.yaml")
	if !strings.Contains(goreleaser, `{{ if not .Prerelease }}latest{{ end }}`) {
		t.Error("the :latest tag is not gated on .Prerelease; a release candidate would claim it")
	}
	for _, line := range strings.Split(goreleaser, "\n") {
		if strings.TrimSpace(line) == "- latest" {
			t.Error("found an unconditional `- latest` tag")
		}
	}
}

// A reusable workflow cannot hold more permissions than its caller
// grants; exceeding it is a startup failure with no useful log, and only
// on a push to main where no pull request can catch it.
func TestCallerGrantsEveryPermissionReleaseNeeds(t *testing.T) {
	called := read(t, "../../.github/workflows/release.yml")
	caller := read(t, "../../.github/workflows/release-please.yml")

	need := perms(firstMatch(t, called, `(?ms)^permissions:\n((?:\s+.*\n)+?)\njobs:`, "release.yml permissions"))
	have := perms(firstMatch(t, caller,
		`(?ms)^  publish:\n.*?    permissions:\n((?:\s+.*\n)+?)    uses:`, "publish job permissions"))

	for p := range need {
		if !have[p] {
			t.Errorf("release.yml needs %q but the calling job does not grant it — this is a startup failure", p)
		}
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func firstMatch(t *testing.T, s, pattern, what string) string {
	t.Helper()
	m := regexp.MustCompile(pattern).FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("could not find %s", what)
	}
	return m[1]
}

func perms(block string) map[string]bool {
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s+([a-z-]+):\s*(?:write|read)`).FindAllStringSubmatch(block, -1) {
		out[m[1]] = true
	}
	return out
}
