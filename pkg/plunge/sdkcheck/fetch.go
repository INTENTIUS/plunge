// Copyright 2026, INTENTIUS.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sdkcheck

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/blang/semver"
)

// Languages are the SDKs the check covers, in output order.
var Languages = []string{"nodejs", "python", "go", "dotnet", "java", "yaml"}

// Lockstep reports whether a language's SDK is released with the CLI from pulumi/pulumi, so its version
// is comparable with plunge's Pulumi-compatible version.
func Lockstep(lang string) bool {
	return lang == "nodejs" || lang == "python" || lang == "go"
}

// Release is an SDK release's source.
type Release struct {
	Lang    string
	Version string
	// Note says where the source came from.
	Note  string
	Files []Source
}

const maxFileSize = 8 << 20

// Fetcher downloads SDK releases from their package registries. It only reads; it never publishes.
type Fetcher struct {
	Client *http.Client
	// GitHubToken, if set, is sent to api.github.com to lift the anonymous rate limit.
	GitHubToken string
}

// NewFetcher returns a Fetcher with a timeout on every request.
func NewFetcher() *Fetcher {
	return &Fetcher{Client: &http.Client{Timeout: 5 * time.Minute}, GitHubToken: os.Getenv("GITHUB_TOKEN")}
}

// Latest fetches the latest release of lang's SDK.
//
//	nodejs  @pulumi/pulumi from registry.npmjs.org (the published tarball)
//	python  pulumi from pypi.org (the wheel, else the sdist)
//	go      github.com/pulumi/pulumi/sdk/v3 from proxy.golang.org (the module zip)
//	dotnet  the Pulumi package version from api.nuget.org; the source at that tag of pulumi/pulumi-dotnet
//	java    com.pulumi:pulumi from Maven Central (the sources jar)
//	yaml    pulumi/pulumi-yaml's latest GitHub release; it runs on the Go SDK, so the Go SDK version its
//	        go.mod requires is what gets checked
func (f *Fetcher) Latest(ctx context.Context, lang string) (Release, error) {
	switch lang {
	case "nodejs":
		return f.nodejs(ctx)
	case "python":
		return f.python(ctx)
	case "go":
		v, err := f.goLatest(ctx)
		if err != nil {
			return Release{}, err
		}
		return f.goSDK(ctx, "go", v)
	case "dotnet":
		return f.dotnet(ctx)
	case "java":
		return f.java(ctx)
	case "yaml":
		return f.yaml(ctx)
	}
	return Release{}, fmt.Errorf("unknown language %q", lang)
}

func (f *Fetcher) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if f.GitHubToken != "" && strings.HasPrefix(url, "https://api.github.com/") {
		req.Header.Set("Authorization", "Bearer "+f.GitHubToken)
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func (f *Fetcher) getJSON(ctx context.Context, url string, v any) error {
	data, err := f.get(ctx, url)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func (f *Fetcher) nodejs(ctx context.Context) (Release, error) {
	var meta struct {
		Version string `json:"version"`
		Dist    struct {
			Tarball string `json:"tarball"`
		} `json:"dist"`
	}
	if err := f.getJSON(ctx, "https://registry.npmjs.org/@pulumi/pulumi/latest", &meta); err != nil {
		return Release{}, err
	}
	data, err := f.get(ctx, meta.Dist.Tarball)
	if err != nil {
		return Release{}, err
	}
	files, err := ReadTarGz(data)
	return Release{Lang: "nodejs", Version: meta.Version, Note: meta.Dist.Tarball, Files: files}, err
}

func (f *Fetcher) python(ctx context.Context) (Release, error) {
	var meta struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
		URLs []struct {
			PackageType string `json:"packagetype"`
			URL         string `json:"url"`
		} `json:"urls"`
	}
	if err := f.getJSON(ctx, "https://pypi.org/pypi/pulumi/json", &meta); err != nil {
		return Release{}, err
	}
	var wheel, sdist string
	for _, u := range meta.URLs {
		switch u.PackageType {
		case "bdist_wheel":
			wheel = u.URL
		case "sdist":
			sdist = u.URL
		}
	}
	rel := Release{Lang: "python", Version: meta.Info.Version}
	var err error
	switch {
	case wheel != "":
		var data []byte
		if data, err = f.get(ctx, wheel); err == nil {
			rel.Files, err = ReadZip(data)
		}
		rel.Note = wheel
	case sdist != "":
		var data []byte
		if data, err = f.get(ctx, sdist); err == nil {
			rel.Files, err = ReadTarGz(data)
		}
		rel.Note = sdist
	default:
		err = fmt.Errorf("pypi lists no wheel or sdist for pulumi %s", meta.Info.Version)
	}
	return rel, err
}

func (f *Fetcher) goLatest(ctx context.Context) (string, error) {
	var meta struct{ Version string }
	if err := f.getJSON(ctx, "https://proxy.golang.org/github.com/pulumi/pulumi/sdk/v3/@latest", &meta); err != nil {
		return "", err
	}
	return meta.Version, nil
}

func (f *Fetcher) goSDK(ctx context.Context, lang, version string) (Release, error) {
	url := "https://proxy.golang.org/github.com/pulumi/pulumi/sdk/v3/@v/" + version + ".zip"
	data, err := f.get(ctx, url)
	if err != nil {
		return Release{}, err
	}
	files, err := ReadZip(data)
	return Release{Lang: lang, Version: strings.TrimPrefix(version, "v"), Note: url, Files: files}, err
}

func (f *Fetcher) dotnet(ctx context.Context) (Release, error) {
	var index struct {
		Versions []string `json:"versions"`
	}
	if err := f.getJSON(ctx, "https://api.nuget.org/v3-flatcontainer/pulumi/index.json", &index); err != nil {
		return Release{}, err
	}
	v, err := newestStable(index.Versions)
	if err != nil {
		return Release{}, fmt.Errorf("nuget Pulumi: %w", err)
	}
	// The nupkg holds compiled assemblies; the release tag's source says the same thing readably.
	url := "https://codeload.github.com/pulumi/pulumi-dotnet/tar.gz/refs/tags/v" + v
	data, err := f.get(ctx, url)
	if err != nil {
		return Release{}, err
	}
	files, err := ReadTarGz(data)
	return Release{Lang: "dotnet", Version: v, Note: url, Files: files}, err
}

func (f *Fetcher) java(ctx context.Context) (Release, error) {
	base := "https://repo1.maven.org/maven2/com/pulumi/pulumi/"
	data, err := f.get(ctx, base+"maven-metadata.xml")
	if err != nil {
		return Release{}, err
	}
	var meta struct {
		Versioning struct {
			Release string `xml:"release"`
		} `xml:"versioning"`
	}
	if err := xml.Unmarshal(data, &meta); err != nil {
		return Release{}, err
	}
	v := meta.Versioning.Release
	if v == "" {
		return Release{}, errors.New("maven-metadata.xml for com.pulumi:pulumi has no release")
	}
	url := fmt.Sprintf("%s%s/pulumi-%s-sources.jar", base, v, v)
	jar, err := f.get(ctx, url)
	if err != nil {
		return Release{}, err
	}
	files, err := ReadZip(jar)
	return Release{Lang: "java", Version: v, Note: url, Files: files}, err
}

var goModSDK = regexp.MustCompile(`(?m)^\s*(?:require\s+)?github\.com/pulumi/pulumi/sdk/v3\s+(v\S+)`)

func (f *Fetcher) yaml(ctx context.Context) (Release, error) {
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := f.getJSON(ctx, "https://api.github.com/repos/pulumi/pulumi-yaml/releases/latest", &rel); err != nil {
		return Release{}, err
	}
	gomod, err := f.get(ctx, "https://raw.githubusercontent.com/pulumi/pulumi-yaml/"+rel.TagName+"/go.mod")
	if err != nil {
		return Release{}, err
	}
	m := goModSDK.FindSubmatch(gomod)
	if m == nil {
		return Release{}, fmt.Errorf("pulumi-yaml %s go.mod requires no github.com/pulumi/pulumi/sdk/v3", rel.TagName)
	}
	sdk, err := f.goSDK(ctx, "yaml", string(m[1]))
	if err != nil {
		return Release{}, err
	}
	sdk.Version = strings.TrimPrefix(rel.TagName, "v")
	sdk.Note = fmt.Sprintf("pulumi-yaml %s runs on github.com/pulumi/pulumi/sdk/v3 %s", rel.TagName, m[1])
	return sdk, nil
}

func newestStable(versions []string) (string, error) {
	var best *semver.Version
	var bestRaw string
	for _, raw := range versions {
		v, err := semver.ParseTolerant(raw)
		if err != nil || len(v.Pre) > 0 {
			continue
		}
		if best == nil || v.GT(*best) {
			best, bestRaw = &v, raw
		}
	}
	if best == nil {
		return "", errors.New("no stable version")
	}
	return bestRaw, nil
}

func keep(path string, size int64) bool {
	if size > maxFileSize {
		return false
	}
	return isSource(path) || strings.HasSuffix(path, ".json") || strings.HasSuffix(path, ".yaml") ||
		strings.HasSuffix(path, ".py")
}

// ReadTarGz reads the source files out of a .tar.gz (an npm tarball, an sdist, a GitHub source tarball).
func ReadTarGz(data []byte) ([]Source, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	var files []Source
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg || !keep(h.Name, h.Size) {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		files = append(files, Source{Path: h.Name, Data: b})
	}
}

// ReadZip reads the source files out of a zip (a wheel, a Go module zip, a sources jar).
func ReadZip(data []byte) ([]Source, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var files []Source
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() || !keep(zf.Name, int64(zf.UncompressedSize64)) {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		files = append(files, Source{Path: zf.Name, Data: b})
	}
	return files, nil
}

// ReadDir reads the source files under a local checkout or unpacked release, for -source overrides.
func ReadDir(root string) ([]Source, error) {
	var files []Source
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "node_modules" || name == ".git" || name == "bin" || name == "obj" {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || !keep(path, info.Size()) {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, Source{Path: filepath.ToSlash(rel), Data: b})
		return nil
	})
	return files, err
}
