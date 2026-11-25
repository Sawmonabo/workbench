package release

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// Repository publishes Workbench releases; install.sh names the same one.
const Repository = "Sawmonabo/workbench"

// Published is one published release.
type Published struct {
	Tag       string    `json:"tag"`
	Published time.Time `json:"published"`
}

// Remote is a published release and this machine's bundle in it.
type Remote struct {
	Tag          string
	asset, token string
}

type githubRelease struct {
	Tag        string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Published  time.Time `json:"published_at"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"assets"`
}

// Releases lists published releases, newest first, without drafts or
// prereleases, from the first hundred GitHub returns.
func Releases(ctx context.Context, c operation.Context) ([]Published, error) {
	var releases []githubRelease
	token := githubToken(ctx, c)
	if err := githubAPI(ctx, token, "/releases?per_page=100", "", &releases); err != nil {
		return nil, err
	}
	published := []Published{}
	for _, r := range releases {
		if !r.Draft && !r.Prerelease {
			published = append(published, Published{Tag: r.Tag, Published: r.Published})
		}
	}
	return published, nil
}

// Find looks up release tag, or the latest release when tag is empty, and its
// bundle for this machine.
func Find(ctx context.Context, c operation.Context, tag string) (Remote, error) {
	path := "/releases/latest"
	if tag != "" {
		path = "/releases/tags/" + url.PathEscape(tag)
	}
	token := githubToken(ctx, c)
	var r githubRelease
	if err := githubAPI(ctx, token, path, tag, &r); err != nil {
		return Remote{}, err
	}
	name := "workbench-" + r.Tag + "-" + Target() + ".tar.gz"
	for _, asset := range r.Assets {
		if asset.Name == name {
			return Remote{Tag: r.Tag, asset: asset.URL, token: token}, nil
		}
	}
	return Remote{}, operation.Fail(
		operation.ExitBlocked,
		"release_unavailable",
		"Release "+r.Tag+" has no "+name,
	)
}

// Bundle downloads and verifies the release's bundle, reporting its bytes to
// progress when set.
func (r Remote) Bundle(ctx context.Context, progress func(done, total int64)) (Bundle, error) {
	data, _, err := get(
		ctx,
		r.asset,
		MaxDownload,
		request{accept: "application/octet-stream", token: r.token, progress: progress},
	)
	if err != nil {
		return Bundle{}, err
	}
	return Verify(bytes.NewReader(data), r.Tag, Target())
}

// githubAPI reads one JSON answer from the repository's GitHub REST API. tag
// names the release asked for, if any, when GitHub has none.
func githubAPI(ctx context.Context, token, path, tag string, answer any) error {
	data, _, err := get(
		ctx,
		"https://api.github.com/repos/"+Repository+path,
		4<<20,
		request{accept: "application/vnd.github+json", token: token},
	)
	if errors.Is(err, errNotFound) {
		message := "No published Workbench release found"
		if tag != "" {
			message = "No Workbench release " + tag + "; see workbench update --list"
		}
		return operation.Fail(operation.ExitBlocked, "release_unavailable", message)
	}
	if err != nil {
		return err
	}
	if json.Unmarshal(data, answer) != nil {
		return operation.Fail(operation.ExitFailed, "release", "Unexpected GitHub release data")
	}
	return nil
}

// githubToken returns the GitHub CLI's token for github.com, which raises
// GitHub's rate limit, or "" to ask GitHub anonymously.
func githubToken(ctx context.Context, c operation.Context) string {
	gh, err := operation.FindExecutable("gh", os.Getenv("PATH"), nil)
	if err != nil {
		return ""
	}
	environment := []string{"HOME=" + c.Home, "PATH=/usr/bin:/bin"}
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_CONFIG_DIR"} {
		if value := os.Getenv(name); value != "" {
			environment = append(environment, name+"="+value)
		}
	}
	output, err := operation.Run(ctx, c, nil, operation.Process{
		Executable:    gh,
		Args:          []string{"auth", "token", "--hostname", "github.com"},
		Directory:     "/",
		Environment:   environment,
		PrivateOutput: true,
		OutputLimit:   4096,
	})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output.Stdout)
}

// Newer reports whether release tag a is a later version than b, both of the
// form vMAJOR.MINOR.PATCH with an optional -prerelease, which comes before the
// release it precedes (v0.1.8-dev.1 is newer than v0.1.7, older than v0.1.8).
// Tags that do not parse are never newer.
func Newer(a, b string) bool {
	left, okA := parseTag(a)
	right, okB := parseTag(b)
	if !okA || !okB {
		return false
	}
	for i := range 3 {
		if left.Core[i] != right.Core[i] {
			return left.Core[i] > right.Core[i]
		}
	}
	switch {
	case left.Pre == right.Pre:
		return false
	case left.Pre == "":
		return true
	case right.Pre == "":
		return false
	}
	return prereleaseNewer(strings.Split(left.Pre, "."), strings.Split(right.Pre, "."))
}

// Version is a semantic version: its three numbers and its prerelease, ""
// for a release.
type Version struct {
	Core [3]uint64
	Pre  string
}

// ParseVersion parses MAJOR.MINOR.PATCH with an optional -prerelease and
// +build, which it drops; ok is false for anything else.
func ParseVersion(s string) (v Version, ok bool) {
	core, pre, _ := strings.Cut(s, "-")
	core, _, _ = strings.Cut(core, "+")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return v, false
		}
		v.Core[i] = n
	}
	v.Pre, _, _ = strings.Cut(pre, "+")
	return v, true
}

// parseTag parses a release tag, a [ParseVersion] version after a v.
func parseTag(s string) (Version, bool) {
	rest, ok := strings.CutPrefix(s, "v")
	if !ok {
		return Version{}, false
	}
	return ParseVersion(rest)
}

// prereleaseNewer compares dot-separated prerelease identifiers as semantic
// versioning does: numbers numerically and below words, words by text, and a
// longer list newer when every shared identifier is equal.
func prereleaseNewer(a, b []string) bool {
	for i := range min(len(a), len(b)) {
		x, errX := strconv.ParseUint(a[i], 10, 64)
		y, errY := strconv.ParseUint(b[i], 10, 64)
		switch {
		case errX == nil && errY == nil && x != y:
			return x > y
		case errX == nil && errY != nil:
			return false
		case errX != nil && errY == nil:
			return true
		case errX != nil && a[i] != b[i]:
			return a[i] > b[i]
		}
	}
	return len(a) > len(b)
}
