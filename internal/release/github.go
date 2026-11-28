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
	"strconv"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/download"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// repository publishes Workbench releases; install.sh names the same one.
const repository = "Sawmonabo/workbench"

// InstallCommand is the command, as README gives it, that installs the
// latest published release: install.sh downloads its bundle, which the
// bundle's own CLI verifies before it installs anything.
const InstallCommand = "curl -fsSL https://github.com/" + repository +
	"/releases/latest/download/install.sh | sh"

// Published is one published release.
type Published struct {
	Tag       string    `json:"tag"`
	Published time.Time `json:"published"`
}

// Remote is a published release and this machine's bundle in it.
type Remote struct {
	Tag   string
	asset string
	auth  githubAuth
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
	auth := githubToken(ctx, c)
	if err := githubAPI(ctx, auth, "/releases?per_page=100", "", &releases); err != nil {
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
	auth := githubToken(ctx, c)
	var r githubRelease
	if err := githubAPI(ctx, auth, path, tag, &r); err != nil {
		return Remote{}, err
	}
	name := "workbench-" + r.Tag + "-" + Target() + ".tar.gz"
	for _, asset := range r.Assets {
		if asset.Name == name {
			return Remote{Tag: r.Tag, asset: asset.URL, auth: auth}, nil
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
		download.Request{
			Accept:   "application/octet-stream",
			Token:    r.auth.token,
			Progress: progress,
		},
		r.auth.login(),
	)
	if err != nil {
		return Bundle{}, err
	}
	return verify(bytes.NewReader(data), r.Tag, Target())
}

// githubAPI reads one JSON answer from the repository's GitHub REST API, sent
// as auth says. tag names the release asked for, if any, when GitHub has none.
func githubAPI(ctx context.Context, auth githubAuth, path, tag string, answer any) error {
	location := "https://api.github.com/repos/" + repository + path
	data, _, err := download.Get(
		ctx,
		location,
		4<<20,
		download.Request{Accept: "application/vnd.github+json", Token: auth.token},
	)
	if status, ok := errors.AsType[*download.StatusError](err); ok &&
		status.Status == http.StatusNotFound {
		if tag != "" {
			return operation.Fail(
				operation.ExitBlocked,
				"release_unavailable",
				"No Workbench release "+tag,
				operation.Fix("workbench", "update", "--list"),
			)
		}
		return operation.Fail(
			operation.ExitBlocked,
			"release_unavailable",
			"No published Workbench release found",
		)
	}
	if err != nil {
		return releaseAnswer(err, location, auth.token != "", auth.login())
	}
	if json.Unmarshal(data, answer) != nil {
		return operation.Fail(operation.ExitFailed, "release", "Unexpected GitHub release data")
	}
	return nil
}

// getRelease is [download.Get] for a release, from GitHub's API or a bundle's
// address, returning the body, with its failure named as [releaseAnswer]
// names it, login the fix lines that raise GitHub's limit for it.
func getRelease(
	ctx context.Context,
	location string,
	limit int64,
	r download.Request,
	login []operation.Part,
) ([]byte, error) {
	data, _, err := download.Get(ctx, location, limit, r)
	if err != nil {
		return nil, releaseAnswer(err, location, r.Token != "", login)
	}
	return data, nil
}

// releaseAnswer is err, a download's failure from the release address
// location, as a release's: a missing resource is a release that is not there
// (release_unavailable, in the download's words). From GitHub (see
// [fromGitHub]) a request past its limit says when to ask again (see
// [retryLine]) and then login, the fix lines that raise the limit for that
// request, none when a token would not; a refusal of the token sent, when
// token is set, says to check it; any other refusal, and any other host's,
// keeps the download's words.
func releaseAnswer(err error, location string, token bool, login []operation.Part) error {
	status, ok := errors.AsType[*download.StatusError](err)
	switch {
	case !ok:
		return err
	case status.Status == http.StatusNotFound:
		return operation.FailAs(operation.ExitBlocked, "release_unavailable", status)
	case !fromGitHub(location):
		return err
	case limited(status):
		return operation.Fail(
			operation.ExitFailed,
			"download",
			"GitHub's request limit is used up for now",
			append([]operation.Part{
				operation.FixText(retryLine(status.Header, time.Now())),
			}, login...)...,
		)
	case token && (status.Status == http.StatusUnauthorized ||
		status.Status == http.StatusForbidden):
		return operation.Fail(
			operation.ExitFailed,
			"download",
			fmt.Sprintf("GitHub refused the request (HTTP %d)", status.Status),
			operation.Fix("gh", "auth", "status"),
			operation.FixText("or check the GH_TOKEN and GITHUB_TOKEN variables"),
		)
	}
	return err
}

// limited reports whether status is GitHub's answer to a request past one of
// its limits: a 429, or a 403 with no requests remaining or with a
// Retry-After header, as GitHub's REST API documentation describes them.
func limited(status *download.StatusError) bool {
	return status.Status == http.StatusTooManyRequests ||
		status.Status == http.StatusForbidden &&
			(status.Header.Get("X-RateLimit-Remaining") == "0" ||
				status.Header.Get("Retry-After") != "")
}

// retryLine is when GitHub, past a limit, accepts the next request, as header
// says at now: Retry-After seconds from now, else, with no requests remaining,
// the X-RateLimit-Reset time, rounded up to its minute on the machine's clock
// ("wait until 7:42 AM"), and "wait a minute" when that is a minute or less
// away or neither header says.
func retryLine(header http.Header, now time.Time) string {
	var until time.Time
	if seconds, err := strconv.ParseInt(header.Get("Retry-After"), 10, 64); err == nil {
		until = now.Add(time.Duration(seconds) * time.Second)
	} else if header.Get("X-RateLimit-Remaining") == "0" {
		if reset, err := strconv.ParseInt(header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			until = time.Unix(reset, 0)
		}
	}
	if until.Sub(now) <= time.Minute {
		return "wait a minute"
	}
	if minute := until.Truncate(time.Minute); minute.Before(until) {
		until = minute.Add(time.Minute)
	}
	return "wait until " + display.Clock(until, now)
}

// fromGitHub reports whether location is on a GitHub host Workbench
// downloads releases from: github.com, whose release pages serve the chezmoi
// and uv archives and may be a --bundle address, or api.github.com, whose
// REST API lists Workbench's releases and serves their bundles.
func fromGitHub(location string) bool {
	u, err := url.Parse(location)
	return err == nil && (u.Hostname() == "github.com" || u.Hostname() == "api.github.com")
}

// githubAuth is how Workbench asks GitHub's API: with token, the GitHub CLI's
// token for github.com, which raises GitHub's limit, or anonymously when it
// is empty; gh says whether the GitHub CLI is on PATH.
type githubAuth struct {
	token string
	gh    bool
}

// login is the fix lines that raise GitHub's limit for a request sent as auth
// says: none once a token was sent, gh auth login when gh is on PATH, else
// how to install gh with the package manager Workbench uses here, apt, dnf
// (from EPEL, as the epel step turns on) or Homebrew, or from its site when
// none is found, then gh auth login.
func (auth githubAuth) login() []operation.Part {
	switch {
	case auth.token != "":
		return nil
	case auth.gh:
		return []operation.Part{operation.FixText("or gh auth login")}
	}
	install := []operation.Part{operation.FixText("or install gh from https://cli.github.com")}
	switch platform.PackageManager() {
	case "apt":
		install = []operation.Part{operation.FixText("or sudo apt-get install gh")}
	case "dnf":
		install = []operation.Part{
			operation.FixText("or sudo dnf install epel-release"),
			operation.FixText("then sudo dnf install gh"),
		}
	default:
		if _, err := operation.FindExecutable("brew", os.Getenv("PATH"), nil); err == nil {
			install = []operation.Part{operation.FixText("or brew install gh")}
		}
	}
	return append(install, operation.FixText("then gh auth login"))
}

// githubToken is how Workbench asks GitHub's API: with the GitHub CLI's
// token for github.com when gh is on PATH and gives one, else anonymously.
func githubToken(ctx context.Context, c operation.Context) githubAuth {
	gh, err := operation.FindExecutable("gh", os.Getenv("PATH"), nil)
	if err != nil {
		return githubAuth{}
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
		return githubAuth{gh: true}
	}
	return githubAuth{token: strings.TrimSpace(output.Stdout), gh: true}
}
