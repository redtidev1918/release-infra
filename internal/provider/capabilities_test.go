package provider

import (
	"testing"

	"github.com/redtidev1918/releasegraph/internal/domain"
	"github.com/redtidev1918/releasegraph/internal/policy"
)

// srcOnly is a repository whose contract is "tag + GitHub Release" only.
func srcOnlyCaps() policy.Capabilities {
	return policy.Capabilities{GitHubRelease: true}
}

// releaseOnly is a repository with a GitHub Release and no other artifacts.
func releaseOnlyActual() Actual {
	return Actual{
		TagExists: true, TagCommit: "abc", ExpectedCommit: "abc",
		ReleaseExists: true, Latest: true,
		AssetsComplete: true, ChecksumsVerified: true,
	}
}

// Acceptance: source-only repositories have no assets, and that is not a failure.
func TestSourceOnlyRepositoryIsHealthyWithoutAssets(t *testing.T) {
	actual := releaseOnlyActual()
	actual.AssetsComplete = false
	actual.ChecksumsVerified = false
	observed := Observed{Provider: KindReleasePlease, Version: "1.0.0", Actual: actual, ProviderState: StateTagged, Capabilities: srcOnlyCaps()}

	verdict := Classify(observed)
	if verdict.Drift != DriftInSync || verdict.Health != domain.HealthHealthy {
		t.Fatalf("source-only verdict = %+v, want HEALTHY/IN_SYNC", verdict)
	}
}

// Acceptance: a registry-only repository (npm) with no assets is healthy once
// the registry is published — no binary asset may be demanded.
func TestRegistryOnlyRepositoryIsHealthyWithoutBinaries(t *testing.T) {
	caps := policy.Capabilities{GitHubRelease: true, Registries: []string{"npm"}}
	actual := releaseOnlyActual()
	actual.AssetsComplete = false
	actual.ChecksumsVerified = false
	actual.RegistriesHealthy = true

	observed := Observed{Provider: KindReleasePlease, Version: "4.2.0", Actual: actual, ProviderState: StateTagged, Capabilities: caps}
	if verdict := Classify(observed); verdict.Health != domain.HealthHealthy || verdict.Drift != DriftInSync {
		t.Fatalf("npm-only verdict = %+v, want HEALTHY/IN_SYNC", verdict)
	}

	// The same repository with an unpublished required registry is incomplete.
	// With the provider still pending this is a repairable transaction; had the
	// provider already claimed tagged it would be PROVIDER_FALSE_ACK.
	actual.RegistriesHealthy = false
	observed.Actual = actual
	observed.ProviderState = StatePending
	if verdict := Classify(observed); verdict.Drift != DriftRegistryIncomplete || verdict.Health != domain.HealthRecoverable {
		t.Fatalf("unpublished npm registry = %+v, want REGISTRY_INCOMPLETE/RECOVERABLE", verdict)
	}
}

// Acceptance: a repository that declares no GitHub Release is healthy on its tag
// alone; the missing Release object must not be reported as an incomplete release.
func TestNoGitHubReleaseContractDoesNotRequireARelease(t *testing.T) {
	caps := policy.Capabilities{GitHubRelease: false}
	actual := Actual{TagExists: true, TagCommit: "abc", ExpectedCommit: "abc"} // no release, no assets

	observed := Observed{Provider: KindReleasePlease, Version: "0.1.0", Actual: actual, ProviderState: StateTagged, Capabilities: caps}
	if verdict := Classify(observed); verdict.Health != domain.HealthHealthy {
		t.Fatalf("release-less contract verdict = %+v, want HEALTHY", verdict)
	}
}

// Acceptance: when the contract does require binaries, a missing binary is a
// genuinely incomplete transaction.
func TestMissingRequiredBinariesAreIncomplete(t *testing.T) {
	caps := policy.Capabilities{GitHubRelease: true, Binaries: true, Checksums: true, Assets: []string{"app-linux", "app-macos"}}
	actual := releaseOnlyActual()
	actual.AssetsComplete = false

	observed := Observed{Provider: KindReleasePlease, Version: "2.0.0", Actual: actual, ProviderState: StateTagged, Capabilities: caps}
	if verdict := Classify(observed); verdict.Drift != DriftFalseACK || verdict.Health != domain.HealthDegraded {
		t.Fatalf("missing required binaries = %+v, want FALSE_ACK/DEGRADED", verdict)
	}
}

// Acceptance: a historical release keeps the contract it was published under, so
// a policy that later adds a capability does not retroactively break history.
func TestHistoricalReleaseKeepsItsOwnContract(t *testing.T) {
	// Today's policy demands binaries (derived below).
	today := &policy.Policy{
		Assets: policy.Assets{Required: []string{"app-linux", "app-macos"}},
	}
	if caps := today.CapabilitiesOf(); !caps.Binaries {
		t.Fatal("fixture policy should require binaries today")
	}

	// The historical release recorded "no binaries, GitHub release only".
	historical := policy.Capabilities{GitHubRelease: true}
	actual := releaseOnlyActual()
	actual.AssetsComplete = true // nothing was required

	observed := Observed{Provider: KindReleasePlease, Version: "1.4.0", Actual: actual, ProviderState: StateTagged, Capabilities: historical}
	if verdict := Classify(observed); verdict.Health != domain.HealthHealthy {
		t.Fatalf("historical release judged against today's contract: %+v", verdict)
	}
}

// Acceptance: capability derivation is backward compatible.
func TestCapabilityDerivationDefaults(t *testing.T) {
	// Parsed through the real loader so policy defaults apply.
	binary, err := policy.Parse([]byte(`{"kind":"binary","versioning":{"mode":"manual","version":"1.0.0"},
		"build":{"matrix":[{"runner":"ubuntu-latest","command":"make"}]},
		"assets":{"required":["app-linux"]},"registries":{"github":{"required":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	caps := binary.CapabilitiesOf()
	if !caps.Binaries || !caps.Checksums || !caps.GitHubRelease {
		t.Fatalf("binary policy capabilities = %+v", caps)
	}

	sourceOnly := &policy.Policy{}
	disabled := false
	sourceOnly.Artifacts.Binaries.Enabled = &disabled
	caps = sourceOnly.CapabilitiesOf()
	if caps.Binaries || caps.Checksums || len(caps.Assets) != 0 {
		t.Fatalf("source-only capabilities = %+v", caps)
	}
	if !caps.GitHubRelease {
		t.Fatal("GitHub Release must stay the historical default")
	}

	noRelease := &policy.Policy{Registries: map[string]policy.Registry{"npm": {Required: true}}}
	githubDisabled := false
	noRelease.Release.GitHub = &githubDisabled
	caps = noRelease.CapabilitiesOf()
	if caps.GitHubRelease || len(caps.Registries) != 1 || caps.Registries[0] != "npm" {
		t.Fatalf("registry-only capabilities = %+v", caps)
	}
}
