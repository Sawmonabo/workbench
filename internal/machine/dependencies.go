package machine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

var toolVersion = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+`)

// ResolveDependencies never installs or repairs. Recorded compatible owners win;
// incompatible borrowed tools are retained and reported, never replaced here.
func ResolveDependencies(
	ctx context.Context,
	c operation.Context,
	requirements Requirements,
	recorded []operation.Dependency,
	searchPath string,
) ([]operation.Dependency, []operation.Component) {
	dependencies, results, _ := resolveDependencies(ctx, c, requirements, recorded, searchPath)
	return dependencies, results
}

// resolveDependencies is ResolveDependencies, which also returns the first
// refusal to run a tool that is there, such as one other users can change,
// of a tool no candidate qualified for. That tool's line gives the refusal
// too: it is why the tool is not used, and installing it again leaves a file
// already holding its bytes as it is.
func resolveDependencies(
	ctx context.Context,
	c operation.Context,
	requirements Requirements,
	recorded []operation.Dependency,
	searchPath string,
) ([]operation.Dependency, []operation.Component, error) {
	var dependencies []operation.Dependency
	var results []operation.Component
	var firstRefusal error
	for _, name := range []string{"chezmoi", "python3", "uv"} {
		var candidates []operation.Dependency
		for _, prior := range recorded {
			if prior.Name == name {
				candidates = append(candidates, prior)
			}
		}
		path, err := operation.FindExecutable(
			name,
			searchPath,
			[]string{c.Native.Source, projectRoot(c)},
		)
		if err == nil {
			owner := "user"
			if strings.Contains(path, "/Cellar/") {
				owner = "homebrew"
			}
			if strings.HasPrefix(path, "/usr/bin/") {
				owner = "system"
			}
			candidates = append(
				candidates,
				operation.Dependency{Name: name, Path: path, Owner: owner},
			)
		}
		component := operation.Component{
			Name:    name,
			Status:  operation.StatusBlocked,
			Message: "not found",
			Body: operation.Explain(append(
				[]operation.Part{operation.Line{
					Label: "needs",
					Text:  "Workbench's own " + qualifiedVersions(name, requirements),
				}},
				InstallFix(c)...,
			)...),
		}
		var refusal error
		for _, candidate := range candidates {
			if err := ownToolFolder(c, candidate.Path); err != nil {
				component.Message, component.Body = operation.Explained(err)
				if refusal == nil {
					refusal = err
				}
				continue
			}
			version, err := probeVersion(ctx, c, name, candidate.Path)
			if err != nil {
				component.Message, component.Body = operation.Explained(err)
				if refusal == nil && operation.Category(err) == "tool" {
					refusal = err
				}
				continue
			}
			qualified, capabilities := qualify(name, version, requirements)
			candidate.Capabilities = capabilities
			candidate.Version = version
			if !qualified {
				component.Message = fmt.Sprintf(
					"%s %s at %s is not %s",
					name,
					version,
					c.ShowPath(candidate.Path),
					qualifiedVersions(name, requirements),
				)
				component.Body = operation.Explain(append(
					[]operation.Part{operation.Cause(
						"Workbench installs its own copy and leaves this one alone")},
					InstallFix(c)...,
				)...)
				continue
			}
			dependencies = append(dependencies, candidate)
			component.Status = operation.StatusComplete
			component.Message = version + " (" + candidate.Owner + ") at " + c.ShowPath(
				candidate.Path,
			)
			component.Body = operation.Body{}
			break
		}
		if component.Status != operation.StatusComplete && refusal != nil {
			component.Message, component.Body = operation.Explained(refusal)
			if firstRefusal == nil {
				firstRefusal = refusal
			}
		}
		results = append(results, component)
	}
	return dependencies, results, firstRefusal
}

// ownToolFolder gives the folders from the data folder down to the one that
// holds path, a program Workbench installed there, the checks of a folder
// that is only read (see [operation.CheckPrivateDirectory]) before the
// program is looked at: one its owner may not read or search gets 0700, and
// one others can write in is refused, since a program another user put there
// would run as this user. A program elsewhere has only the checks
// [operation.FindExecutable] gives it.
func ownToolFolder(c operation.Context, path string) error {
	name, err := filepath.Rel(c.Paths.Data, filepath.Dir(path))
	if err != nil || !filepath.IsLocal(name) {
		return nil
	}
	return operation.CheckPrivateDirectory(c.Paths.Data, name)
}

// probeVersion runs a bounded version probe of the tool at path and returns
// the version it reports. chezmoi runs against an empty private scratch
// context so it never reads the user's configuration. Workbench's refusal to
// run the program at all ("tool"), or its finding no program there
// ("dependency"), is returned as it is.
func probeVersion(ctx context.Context, c operation.Context, name, path string) (string, error) {
	args := []string{"--version"}
	environment := []string{"PATH=/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE=1", "HOME=" + c.Home}
	switch name {
	case "chezmoi":
		made, scratch, native, err := scratchNative("workbench-version-", operation.NativeContext{})
		if err != nil {
			return "", operation.Fail(
				operation.ExitFailed,
				"dependency",
				"Cannot create private version-probe scratch",
			)
		}
		defer func() { _ = os.RemoveAll(made) }()
		native.Source, native.Destination = scratch, scratch
		native.Config = filepath.Join(scratch, "config.toml")
		if err = os.WriteFile(native.Config, nil, 0o600); err != nil {
			return "", operation.Fail(
				operation.ExitFailed,
				"dependency",
				"Cannot create private version-probe scratch",
			)
		}
		args, _ = native.Args()
		args = append(args, "--version")
		environment = []string{"PATH=/usr/bin:/bin", "HOME=" + scratch}
	case "python3":
		args = []string{
			"-I",
			"-c",
			"import sys,tomllib; print('.'.join(map(str,sys.version_info[:3])))",
		}
	}
	output, err := operation.Run(ctx, c, nil, operation.Process{
		Executable:  path,
		Args:        args,
		Directory:   "/",
		Environment: environment,
		Timeout:     10 * time.Second,
		OutputLimit: 4096,
	})
	if category := operation.Category(err); category == "tool" || category == "dependency" {
		return "", err
	}
	if err != nil {
		return "", operation.Fail(
			operation.ExitFailed,
			"dependency",
			"Cannot read the version of "+name,
			operation.Kept("the installed "+name),
		)
	}
	return toolVersion.FindString(output.Stdout), nil
}

// qualify reports whether version is qualified for tool name and returns the
// capabilities Workbench relies on from it.
func qualify(name, version string, requirements Requirements) (bool, []string) {
	switch name {
	case "chezmoi":
		return version == requirements.Chezmoi, []string{
			"builtin-git",
			"builtin-diff",
			"native-targets",
		}
	case "uv":
		qualified := version == requirements.UV ||
			slices.Contains(requirements.UVAdditional, version)
		return qualified, []string{"private-python"}
	case "python3":
		parts := strings.Split(version, ".")
		if len(parts) != 3 {
			return false, []string{"tomllib"}
		}
		minor, _ := strconv.Atoi(parts[1])
		qualified := parts[0] == "3" && minor >= requirements.PythonMinMinor &&
			minor <= requirements.PythonMaxMinor
		return qualified, []string{"tomllib"}
	}
	return false, nil
}

// qualifiedVersions describes the versions qualify accepts for tool name.
func qualifiedVersions(name string, requirements Requirements) string {
	switch name {
	case "uv":
		versions := []string{requirements.UV}
		for _, version := range requirements.UVAdditional {
			if !slices.Contains(versions, version) {
				versions = append(versions, version)
			}
		}
		return strings.Join(versions, " or ")
	case "python3":
		return fmt.Sprintf("3.%d–3.%d", requirements.PythonMinMinor, requirements.PythonMaxMinor)
	}
	return requirements.Chezmoi
}

func projectRoot(c operation.Context) string {
	if c.Scope.Kind == "project" {
		return c.Scope.Root
	}
	return ""
}

func dependency(dependencies []operation.Dependency, name string) string {
	for _, dependency := range dependencies {
		if dependency.Name == name {
			return dependency.Path
		}
	}
	return ""
}

// dependencyVersion is the version of the dependency named name, or "".
func dependencyVersion(dependencies []operation.Dependency, name string) string {
	for _, dependency := range dependencies {
		if dependency.Name == name {
			return dependency.Version
		}
	}
	return ""
}

// replacedVersion is the version of the previous private copy of name that
// recorded lists and version replaces, or "" when there is none or it is
// version.
func replacedVersion(recorded []operation.Dependency, name, version string) string {
	for _, dependency := range recorded {
		if dependency.Name == name && dependency.Owner == "workbench" &&
			dependency.Version != version {
			return dependency.Version
		}
	}
	return ""
}
