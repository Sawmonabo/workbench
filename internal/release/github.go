package release

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
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

// Bundle downloads and verifies the release's bundle.
func (r Remote) Bundle(ctx context.Context) (Bundle, error) {
	data, err := get(ctx, r.asset, MaxDownload, "application/octet-stream", r.token)
	if err != nil {
		return Bundle{}, err
	}
	return Verify(bytes.NewReader(data), r.Tag, Target())
}

// githubAPI reads one JSON answer from the repository's GitHub REST API. tag
// names the release asked for, if any, when GitHub has none.
func githubAPI(ctx context.Context, token, path, tag string, answer any) error {
	data, err := get(
		ctx,
		"https://api.github.com/repos/"+Repository+path,
		4<<20,
		"application/vnd.github+json",
		token,
	)
	if errors.Is(err, errNotFound) {
		message := "No published Workbench release found"
		if tag != "" {
			message = "No Workbench release " + tag + "; see workbench version --list"
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
