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

// plunge-sdk-check takes each language SDK's latest release and checks it against plunge's resource
// monitor: the CLI version it requires against plunge's Pulumi-compatible version, and every engine
// feature it asks about through SupportsFeature or GetDeploymentInfo. It prints verdict lines:
//
//	MONITOR-FALSE plunge - <feature> (...)            plunge's monitor answers false
//	SDK <lang> <sdk-version> files=.. legacy=.. enum=.. missing=..
//	VERSION-OK|VERSION-TOO-OLD|VERSION-UNKNOWN <lang> <sdk-version> ...
//	SDK-AHEAD <lang> <sdk-version> plunge-pulumi=<v>  a lockstep SDK newer than plunge's upstream base
//	FEATURE-OK <lang> <sdk-version> <feature> (...)
//	FEATURE-MISSING <lang> <sdk-version> <feature> (...)
//	SDK-LEGACY-ID <lang> <sdk-version> <id> (...)    the SDK asks SupportsFeature for an id upstream's
//	                                                  engine doesn't know either; the feature is advertised
//	                                                  through GetDeploymentInfo
//	SDK-ERROR <lang> ...                              the release couldn't be fetched or read
//
// Usage, from pkg/:
//
//	go run ./cmd/plunge-sdk-check -pulumi-version "$(../scripts/plunge-version.sh pulumi)" -out verdicts.txt
//	go run ./cmd/plunge-sdk-check -lang nodejs -source nodejs=../sdk/nodejs -source-version nodejs=dev
//
// It only downloads from package registries and GitHub; it runs no program and starts no engine.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/pulumi/pulumi/pkg/v3/plunge/sdkcheck"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/sdk/v3/go/common/version"
)

type pairs map[string]string

func (p pairs) String() string { return fmt.Sprint(map[string]string(p)) }

func (p pairs) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	if !ok || k == "" || v == "" {
		return fmt.Errorf("want lang=value, got %q", s)
	}
	p[k] = v
	return nil
}

func main() {
	langs := flag.String("lang", strings.Join(sdkcheck.Languages, ","), "comma-separated languages to check")
	pulumiVersion := flag.String("pulumi-version", strings.TrimPrefix(version.Version, "v"),
		"plunge's Pulumi-compatible version (scripts/plunge-version.sh pulumi)")
	out := flag.String("out", "", "also write the verdict lines to this file")
	strict := flag.Bool("strict", false, "exit 1 when any FEATURE-MISSING, VERSION-TOO-OLD or SDK-ERROR line is printed")
	sources := pairs{}
	versions := pairs{}
	flag.Var(sources, "source", "lang=dir: read that language's SDK from a local directory instead of downloading")
	flag.Var(versions, "source-version", "lang=version: the version to report for a -source directory")
	flag.Parse()

	if *pulumiVersion == "" {
		fmt.Fprintln(os.Stderr, "plunge-sdk-check: set -pulumi-version (scripts/plunge-version.sh pulumi)")
		os.Exit(2)
	}

	w := io.Writer(os.Stdout)
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		defer f.Close()
		w = io.MultiWriter(os.Stdout, f)
	}

	code := run(context.Background(), w, strings.Split(*langs, ","), *pulumiVersion, sources, versions, *strict)
	os.Exit(code)
}

func run(ctx context.Context, w io.Writer, langs []string, pulumiVersion string,
	sources, versions pairs, strict bool,
) int {
	opts := sdkcheck.MonitorOptions()
	fetcher := sdkcheck.NewFetcher()

	var all []sdkcheck.Verdict
	legacy := slices.Clone(sdkcheck.LegacyIDs)
	for _, lang := range langs {
		lang = strings.TrimSpace(lang)
		if lang == "" {
			continue
		}
		rel, err := release(ctx, fetcher, lang, sources, versions)
		if err != nil {
			all = append(all, sdkcheck.Verdict{Kind: "SDK-ERROR", Lang: lang, Version: "-", Subject: "fetch", Note: err.Error()})
			continue
		}
		vs, err := sdkcheck.Check(ctx, rel, pulumiVersion, opts)
		if err != nil {
			all = append(all, sdkcheck.Verdict{
				Kind: "SDK-ERROR", Lang: lang, Version: rel.Version, Subject: "check", Note: err.Error(),
			})
			continue
		}
		all = append(all, vs...)
		for _, id := range sdkcheck.Extract(rel.Files).Legacy {
			if !slices.Contains(legacy, id) {
				legacy = append(legacy, id)
			}
		}
	}

	slices.Sort(legacy)
	monitor, err := deploy.QueryMonitorFeatures(ctx, opts, legacy)
	if err != nil {
		fmt.Fprintln(os.Stderr, "plunge-sdk-check: querying the monitor:", err)
		return 2
	}
	lines := append(sdkcheck.MonitorVerdicts(legacy, monitor), all...)
	if err := sdkcheck.WriteVerdicts(w, lines); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	if strict {
		for _, v := range all {
			if v.Kind == "FEATURE-MISSING" || v.Kind == "VERSION-TOO-OLD" || v.Kind == "SDK-ERROR" {
				return 1
			}
		}
	}
	return 0
}

func release(ctx context.Context, f *sdkcheck.Fetcher, lang string, sources, versions pairs) (sdkcheck.Release, error) {
	dir, ok := sources[lang]
	if !ok {
		return f.Latest(ctx, lang)
	}
	files, err := sdkcheck.ReadDir(dir)
	if err != nil {
		return sdkcheck.Release{}, err
	}
	v := versions[lang]
	if v == "" {
		v = "local"
	}
	return sdkcheck.Release{Lang: lang, Version: v, Note: "local " + dir, Files: files}, nil
}
