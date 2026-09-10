package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/redtidev1918/releasegraph/internal/config"
	rgerrors "github.com/redtidev1918/releasegraph/internal/errors"
)

type Versioning struct {
	Mode     string `json:"mode" yaml:"mode"`
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`
	Package  string `json:"package,omitempty" yaml:"package,omitempty"`
	Manifest string `json:"manifest,omitempty" yaml:"manifest,omitempty"`
	Version  string `json:"version,omitempty" yaml:"version,omitempty"`
}

type Tag struct {
	Template string `json:"template,omitempty" yaml:"template,omitempty"`
}

type BuildMatrixItem struct {
	Runner       string `json:"runner" yaml:"runner"`
	Command      string `json:"command" yaml:"command"`
	VersionCheck string `json:"version_check,omitempty" yaml:"versionCheck,omitempty"`
}

type Build struct {
	Test           string            `json:"test,omitempty" yaml:"test,omitempty"`
	Command        string            `json:"command,omitempty" yaml:"command,omitempty"`
	VersionCheck   string            `json:"version_check,omitempty" yaml:"versionCheck,omitempty"`
	FlutterVersion string            `json:"flutter_version,omitempty" yaml:"flutterVersion,omitempty"`
	Matrix         []BuildMatrixItem `json:"matrix,omitempty" yaml:"matrix,omitempty"`
}

type Assets struct {
	Required []string `json:"required" yaml:"required"`
	Optional []string `json:"optional" yaml:"optional"`
}

type Registry struct {
	Required bool   `json:"required" yaml:"required"`
	Publish  string `json:"publish,omitempty" yaml:"publish,omitempty"`
	Verify   string `json:"verify,omitempty" yaml:"verify,omitempty"`
	Context  string `json:"context,omitempty" yaml:"context,omitempty"`
	File     string `json:"file,omitempty" yaml:"file,omitempty"`
	Image    string `json:"image,omitempty" yaml:"image,omitempty"`
}

type Retention struct {
	Stable      int `json:"stable,omitempty" yaml:"stable,omitempty"`
	Prerelease  int `json:"prerelease,omitempty" yaml:"prerelease,omitempty"`
	FailedDraft int `json:"failed_draft,omitempty" yaml:"failed_draft,omitempty"`
}

type Release struct {
	Prerelease  bool   `json:"prerelease,omitempty" yaml:"prerelease,omitempty"`
	PostPublish string `json:"post_publish,omitempty" yaml:"post_publish,omitempty"`
	// GitHub declares whether a GitHub Release is part of this repository's
	// contract. nil means "not declared" and falls back to the historical
	// default (true), so existing policies keep their meaning.
	GitHub *bool `json:"github,omitempty" yaml:"github,omitempty"`
}

// Artifacts describes distributable build outputs. Repositories without
// distributable artifacts simply do not enable them.
type Artifacts struct {
	Binaries Binaries `json:"binaries,omitempty" yaml:"binaries,omitempty"`
}

// Binaries covers native/CLI artifacts. It is optional: a Python library, an npm
// package, a container service or a source-only repository has none.
type Binaries struct {
	Enabled *bool    `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Targets []string `json:"targets,omitempty" yaml:"targets,omitempty"`
}

type Policy struct {
	APIVersion string              `json:"apiVersion,omitempty" yaml:"apiVersion,omitempty"`
	Kind       string              `json:"kind" yaml:"kind"`
	Versioning Versioning          `json:"versioning" yaml:"versioning"`
	Tag        Tag                 `json:"tag,omitempty" yaml:"tag,omitempty"`
	Build      Build               `json:"build,omitempty" yaml:"build,omitempty"`
	Assets     Assets              `json:"assets" yaml:"assets"`
	Registries map[string]Registry `json:"registries,omitempty" yaml:"registries,omitempty"`
	Retention  Retention           `json:"retention,omitempty" yaml:"retention,omitempty"`
	Release    Release             `json:"release,omitempty" yaml:"release,omitempty"`
	Artifacts  Artifacts           `json:"artifacts,omitempty" yaml:"artifacts,omitempty"`
	Checksums  bool                `json:"checksums" yaml:"checksums"`
	Metadata   bool                `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	SBOM       bool                `json:"sbom,omitempty" yaml:"sbom,omitempty"`
	Hash       string              `json:"hash,omitempty" yaml:"-"`
}

var versionPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9A-Za-z-]+)+$`)

var kinds = map[string]bool{"binary": true, "python-library": true, "node-library": true, "container": true, "flutter": true, "android": true, "hybrid": true, "none": true}
var versionModes = map[string]bool{"release-please": true, "manual": true, "tag": true}
var registryNames = map[string]bool{"github": true, "pypi": true, "npm": true, "pub": true, "ghcr": true}

func Load(path string) (*Policy, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, rgerrors.Wrap(rgerrors.Policy, "read policy", err)
	}
	return Parse(raw)
}

func Parse(raw []byte) (*Policy, error) {
	var p Policy
	if err := config.Unmarshal(raw, &p); err != nil {
		return nil, rgerrors.Wrap(rgerrors.Policy, "parse policy", err)
	}
	if p.APIVersion == "" {
		p.APIVersion = "releasegraph.dev/v1"
	}
	var rawMap map[string]any
	if err := yaml.Unmarshal(raw, &rawMap); err != nil {
		return nil, rgerrors.Wrap(rgerrors.Policy, "parse policy", err)
	}
	if _, ok := rawMap["checksums"]; !ok {
		p.Checksums = true
	}
	if p.Registries == nil {
		p.Registries = map[string]Registry{}
	}
	if err := Validate(&p); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	p.Hash = hex.EncodeToString(sum[:])
	return &p, nil
}

func Validate(p *Policy) error {
	if p.APIVersion != "" && p.APIVersion != "releasegraph.dev/v1" {
		return rgerrors.New(rgerrors.Policy, "apiVersion must be releasegraph.dev/v1")
	}
	if !kinds[p.Kind] {
		return rgerrors.New(rgerrors.Policy, "kind must be binary, python-library, node-library, container, flutter, android, hybrid, or none")
	}
	mode := p.Versioning.Mode
	if mode == "" {
		mode = p.Versioning.Provider
	}
	if !versionModes[mode] {
		return rgerrors.New(rgerrors.Policy, "versioning.mode must be release-please, manual, or tag")
	}
	if p.Tag.Template != "" && !strings.Contains(p.Tag.Template, "{version}") {
		return rgerrors.New(rgerrors.Policy, "tag.template must contain {version}")
	}
	for _, key := range []string{"required", "optional"} {
		values := map[string][]string{"required": p.Assets.Required, "optional": p.Assets.Optional}[key]
		for _, value := range values {
			if value == "" {
				return rgerrors.New(rgerrors.Policy, "assets."+key+" must contain non-empty strings")
			}
		}
	}
	names := make([]string, 0, len(p.Registries))
	for name := range p.Registries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !registryNames[name] {
			return rgerrors.New(rgerrors.Policy, "unknown registry: "+name)
		}
		config := p.Registries[name]
		if containsNewline(config.Publish) || containsNewline(config.Verify) {
			return rgerrors.New(rgerrors.Policy, "registry publish and verify must be single-line commands")
		}
	}
	for _, item := range p.Build.Matrix {
		if item.Runner == "" || item.Command == "" {
			return rgerrors.New(rgerrors.Policy, "each build matrix item needs runner and command")
		}
		if containsNewline(item.Command) {
			return rgerrors.New(rgerrors.Policy, "build matrix commands must be single-line strings")
		}
	}
	if containsNewline(p.Release.PostPublish) {
		return rgerrors.New(rgerrors.Policy, "release.post_publish must be a single-line command")
	}
	return nil
}

func DesiredVersion(p *Policy, explicit, root string) (string, error) {
	if explicit != "" {
		version := trimPrefix(explicit, "v")
		if !versionPattern.MatchString(version) {
			return "", rgerrors.New(rgerrors.Policy, "invalid release version: "+explicit)
		}
		return version, nil
	}
	mode := p.Versioning.Mode
	if mode == "" {
		mode = p.Versioning.Provider
	}
	if mode == "manual" || mode == "tag" {
		if p.Versioning.Version == "" {
			return "", rgerrors.New(rgerrors.Policy, "manual versioning requires versioning.version or --version")
		}
		return DesiredVersion(&Policy{Versioning: Versioning{Mode: "manual", Version: p.Versioning.Version}}, p.Versioning.Version, root)
	}
	manifestPath := filepath.Join(root, defaultValue(p.Versioning.Manifest, ".release-please-manifest.json"))
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", rgerrors.Wrap(rgerrors.Policy, "read release manifest", err)
	}
	return DesiredVersionFromManifest(p, raw)
}

func DesiredVersionFromManifest(p *Policy, raw []byte) (string, error) {
	var manifest map[string]string
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", rgerrors.Wrap(rgerrors.Policy, "parse release manifest", err)
	}
	packageName := defaultValue(p.Versioning.Package, ".")
	version, ok := manifest[packageName]
	if !ok {
		return "", rgerrors.New(rgerrors.Policy, fmt.Sprintf("manifest has no package %q", packageName))
	}
	return DesiredVersion(&Policy{Versioning: Versioning{Mode: "manual", Version: version}}, version, ".")
}

func ReleaseTag(p *Policy, version string) string {
	return strings.ReplaceAll(defaultValue(p.Tag.Template, "v{version}"), "{version}", version)
}

func defaultValue(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
func trimPrefix(value, prefix string) string {
	if len(value) >= len(prefix) && value[:len(prefix)] == prefix {
		return value[len(prefix):]
	}
	return value
}
func containsNewline(value string) bool {
	for i := range value {
		if value[i] == '\n' {
			return true
		}
	}
	return false
}

// Capabilities is the release contract of one repository, derived from its
// policy. ReleaseGraph is capability-driven: nothing here may be assumed for
// every repository.
type Capabilities struct {
	// GitHubRelease reports whether a public GitHub Release is part of the contract.
	GitHubRelease bool `json:"githubRelease"`
	// Binaries reports whether native artifacts are built and uploaded.
	Binaries bool `json:"binaries"`
	// Checksums reports whether SHA256SUMS must cover the required assets.
	Checksums bool `json:"checksums"`
	// Assets are the required asset patterns (may be empty: source-only repos).
	Assets []string `json:"assets,omitempty"`
	// Registries are the registry names that must be published.
	Registries []string `json:"registries,omitempty"`
}

// CapabilitiesOf derives the effective contract.
//
// Defaults preserve existing policies: a policy that builds a matrix or
// declares required assets has binaries; one that declares neither does not.
// Absence of a capability is a contract choice, never a release failure.
func (p *Policy) CapabilitiesOf() Capabilities {
	caps := Capabilities{GitHubRelease: true, Assets: append([]string{}, p.Assets.Required...)}

	if p.Release.GitHub != nil {
		caps.GitHubRelease = *p.Release.GitHub
	}
	switch {
	case p.Artifacts.Binaries.Enabled != nil:
		caps.Binaries = *p.Artifacts.Binaries.Enabled
	default:
		caps.Binaries = len(p.Build.Matrix) > 0 || len(p.Assets.Required) > 0
	}
	caps.Checksums = p.Checksums && len(caps.Assets) > 0

	names := make([]string, 0, len(p.Registries))
	for name, config := range p.Registries {
		if name == "github" || !config.Required {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	caps.Registries = names
	return caps
}

// Names renders the capability set as stable identifiers used by ReleaseGraph
// compatibility metadata ("binary", "github-release", "checksums", registry
// names). Rollout compares these with a ReleaseGraph release's
// affected_capabilities to decide whether a repository is affected at all.
func (c Capabilities) Names() []string {
	names := []string{}
	if c.GitHubRelease {
		names = append(names, "github-release")
	}
	if c.Binaries {
		names = append(names, "binary")
	}
	if c.Checksums {
		names = append(names, "checksums")
	}
	for _, registry := range c.Registries {
		names = append(names, registry)
	}
	sort.Strings(names)
	return names
}
