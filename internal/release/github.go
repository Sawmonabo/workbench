package release

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/download"
	"github.com/Sawmonabo/workbench/internal/operation"
)

// repository publishes Workbench releases; install.sh names the same one.
const repository = "Sawmonabo/workbench"

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
	data, err := getRelease(
		ctx,
		r.asset,
		MaxDownload,
		download.Request{Accept: "application/octet-stream", Token: r.token, Progress: progress},
	)
	if err != nil {
		return Bundle{}, err
	}
	return verify(bytes.NewReader(data), r.Tag, Target())
}

// githubAPI reads one JSON answer from the repository's GitHub REST API. tag
// names the release asked for, if any, when GitHub has none.
func githubAPI(ctx context.Context, token, path, tag string, answer any) error {
	location := "https://api.github.com/repos/" + repository + path
	data, _, err := download.Get(
		ctx,
		location,
		4<<20,
		download.Request{Accept: "application/vnd.github+json", Token: token},
	)
	if status, ok := errors.AsType[*download.StatusError](err); ok &&
		status.Status == http.StatusNotFound {
		message := "No published Workbench release found"
		if tag != "" {
			message = "No Workbench release " + tag + "; see workbench update --list"
		}
		return operation.Fail(operation.ExitBlocked, "release_unavailable", message)
	}
	if err != nil {
		return releaseAnswer(err, location, token != "")
	}
	if json.Unmarshal(data, answer) != nil {
		return operation.Fail(operation.ExitFailed, "release", "Unexpected GitHub release data")
	}
	return nil
}

// getRelease is [download.Get] for a release, from GitHub's API or a bundle's
// address, returning the body, with its failure named as [releaseAnswer]
// names it.
func getRelease(
	ctx context.Context,
	location string,
	limit int64,
	r download.Request,
) ([]byte, error) {
	data, _, err := download.Get(ctx, location, limit, r)
	if err != nil {
		return nil, releaseAnswer(err, location, r.Token != "")
	}
	return data, nil
}

// releaseAnswer is err, a download's failure from the release address
// location, as a release's: a missing resource is a release that is not there
// (release_unavailable, in the download's words). From GitHub (see
// [fromGitHub]) its refusals, of an anonymous request past its limit or of
// the token sent when token is set, say what to do about them; any other
// host's refusal keeps the download's words.
func releaseAnswer(err error, location string, token bool) error {
	status, ok := errors.AsType[*download.StatusError](err)
	switch {
	case !ok:
		return err
	case status.Status == http.StatusNotFound:
		return operation.Fail(operation.ExitBlocked, "release_unavailable", status.Error())
	case !fromGitHub(location):
		return err
	case status.Status == http.StatusForbidden &&
		status.Header.Get("X-RateLimit-Remaining") == "0":
		return operation.Fail(
			operation.ExitFailed,
			"download",
			"GitHub's request limit is used up for now; wait an hour, or log in with gh auth login",
		)
	case token && (status.Status == http.StatusUnauthorized ||
		status.Status == http.StatusForbidden):
		return operation.Fail(
			operation.ExitFailed,
			"download",
			fmt.Sprintf(
				"GitHub refused the request (HTTP %d); check gh auth status or the GH_TOKEN/GITHUB_TOKEN variables",
				status.Status,
			),
		)
	}
	return err
}

// fromGitHub reports whether location is on a GitHub host Workbench
// downloads releases from: github.com, whose release pages serve the chezmoi
// and uv archives and may be a --bundle address, or api.github.com, whose
// REST API lists Workbench's releases and serves their bundles.
func fromGitHub(location string) bool {
	u, err := url.Parse(location)
	return err == nil && (u.Hostname() == "github.com" || u.Hostname() == "api.github.com")
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
