package main

import (
	"fmt"
	"strings"

	bk "github.com/sourcegraph/sourcegraph/pkg/buildkite"
)

var allDockerImages = []string{
	"frontend",
	"github-proxy",
	"gitserver",
	"management-console",
	"query-runner",
	"repo-updater",
	"searcher",
	"server",
	"symbols",
}

func (c config) generateDockerBuilds(pipeline *bk.Pipeline) {
	switch {
	case c.taggedRelease:
		for _, dockerImage := range allDockerImages {
			dockerImageConfig{config: c, app: dockerImage, insiders: false}.generate(pipeline)
		}
		pipeline.AddWait()
	case c.releaseBranch:
		dockerImageConfig{config: c, app: "server", insiders: false}.generate(pipeline)
		pipeline.AddWait()
	case strings.HasPrefix(c.branch, "master-dry-run/"): // replicates `master` build but does not deploy
		fallthrough
	case c.branch == "master":
		for _, dockerImage := range allDockerImages {
			dockerImageConfig{config: c, app: dockerImage, insiders: true}.generate(pipeline)
		}
		pipeline.AddWait()

	case strings.HasPrefix(c.branch, "docker-images-patch/"):
		dockerImageConfig{config: c, app: c.branch[20:], insiders: false}.generate(pipeline)
		pipeline.AddWait()
	}
}

func generateDocsOnly(pipeline *bk.Pipeline) {
	pipeline.AddStep(":memo:",
		bk.Cmd("./dev/ci/yarn-run.sh prettier-check"),
		bk.Cmd("./dev/check/docsite.sh"))
}

func generateCheck(pipeline *bk.Pipeline) {
	pipeline.AddStep(":white_check_mark:", bk.Cmd("./dev/check/all.sh"))
}

func generateLint(pipeline *bk.Pipeline) {
	pipeline.AddStep(":lipstick: :lint-roller: :stylelint: :typescript: :graphql:",
		bk.Cmd("dev/ci/yarn-run.sh prettier-check all:tslint-eslint all:stylelint all:typecheck graphql-lint"))
}

// Build Sourcegraph Server Docker image
func (c config) generateBuildServerDockerImage(pipeline *bk.Pipeline) {
	pipeline.AddStep(":docker:",
		bk.Cmd("pushd enterprise"),
		bk.Cmd("./cmd/server/pre-build.sh"),
		bk.Env("IMAGE", "sourcegraph/server:"+c.version+"_candidate"),
		bk.Env("VERSION", c.version),
		bk.Cmd("./cmd/server/build.sh"),
		bk.Cmd("popd"))
}

func generateBuildWebApp(pipeline *bk.Pipeline) {
	// Webapp build
	pipeline.AddStep(":webpack::globe_with_meridians:",
		bk.Cmd("dev/ci/yarn-build.sh web"),
		bk.Env("NODE_ENV", "production"),
		bk.Env("ENTERPRISE", "0"))

	// Webapp enterprise build
	pipeline.AddStep(":webpack::globe_with_meridians::moneybag:",
		bk.Cmd("dev/ci/yarn-build.sh web"),
		bk.Env("NODE_ENV", "production"),
		bk.Env("ENTERPRISE", "1"))

	// Webapp tests
	pipeline.AddStep(":jest::globe_with_meridians:",
		bk.Cmd("dev/ci/yarn-test.sh web"),
		bk.ArtifactPaths("web/coverage/coverage-final.json"))
}

func generateBuildBrowserExt(pipeline *bk.Pipeline) {
	// Browser extension build
	pipeline.AddStep(":webpack::chrome:",
		bk.Cmd("dev/ci/yarn-build.sh browser"))

	// Browser extension tests
	pipeline.AddStep(":jest::chrome:",
		bk.Cmd("dev/ci/yarn-test.sh browser"),
		bk.ArtifactPaths("browser/coverage/coverage-final.json"))
}

func generateSharedTests(pipeline *bk.Pipeline) {
	// Shared tests
	pipeline.AddStep(":jest:",
		bk.Cmd("dev/ci/yarn-test.sh shared"),
		bk.ArtifactPaths("shared/coverage/coverage-final.json"))

	// Storybook
	pipeline.AddStep(":storybook:", bk.Cmd("dev/ci/yarn-run.sh storybook:smoke-test"))
}

func generatePostgresBackcompatTest(pipeline *bk.Pipeline) {
	pipeline.AddStep(":postgres:",
		bk.Cmd("./dev/ci/ci-db-backcompat.sh"))
}

func generateGoTests(pipeline *bk.Pipeline) {
	pipeline.AddStep(":go:",
		bk.Cmd("./cmd/symbols/build.sh buildLibsqlite3Pcre"), // for symbols tests
		bk.Cmd("go test -timeout 4m -coverprofile=coverage.txt -covermode=atomic -race ./..."),
		bk.ArtifactPaths("coverage.txt"))
}

func generateGoBuild(pipeline *bk.Pipeline) {
	pipeline.AddStep(":go:",
		bk.Cmd("go generate ./..."),
		bk.Cmd("go install -tags dist ./cmd/... ./enterprise/cmd/..."),
	)
}

func generateDockerfileLint(pipeline *bk.Pipeline) {
	pipeline.AddStep(":docker:",
		bk.Cmd("curl -sL -o hadolint \"https://github.com/hadolint/hadolint/releases/download/v1.15.0/hadolint-$(uname -s)-$(uname -m)\" && chmod 700 hadolint"),
		bk.Cmd("git ls-files | grep Dockerfile | xargs ./hadolint"))
}

func (c config) generateE2ETest(pipeline *bk.Pipeline) {
	pipeline.AddStep(":chromium:",
		// Avoid crashing the sourcegraph/server containers. See
		// https://github.com/sourcegraph/sourcegraph/issues/2657
		bk.ConcurrencyGroup("e2e"),
		bk.Concurrency(1),

		bk.Env("IMAGE", "sourcegraph/server:"+c.version+"_candidate"),
		bk.Env("VERSION", c.version),
		bk.Env("PUPPETEER_SKIP_CHROMIUM_DOWNLOAD", ""),
		bk.Cmd("./dev/ci/e2e.sh"),
		bk.ArtifactPaths("./puppeteer/*.png;./web/e2e.mp4;./web/ffmpeg.log"))
}

func generateCodeCov(pipeline *bk.Pipeline) {
	pipeline.AddStep(":codecov:",
		bk.Cmd("buildkite-agent artifact download 'coverage.txt' . || true"), // ignore error when no report exists
		bk.Cmd("buildkite-agent artifact download '*/coverage-final.json' . || true"),
		bk.Cmd("bash <(curl -s https://codecov.io/bash) -X gcov -X coveragepy -X xcode"))
}

func (c config) generateCleanup(pipeline *bk.Pipeline) {
	pipeline.AddStep(":sparkles:",
		bk.Cmd("docker image rm -f sourcegraph/server:"+c.version+"_candidate"))
}

func addBrowserExtensionReleaseSteps(pipeline *bk.Pipeline) {
	for _, browser := range []string{"chrome", "firefox"} {
		// Run e2e tests
		pipeline.AddStep(fmt.Sprintf(":%s:", browser),
			bk.Env("PUPPETEER_SKIP_CHROMIUM_DOWNLOAD", ""),
			bk.Env("E2E_BROWSER", browser),
			bk.Cmd("yarn --frozen-lockfile --network-timeout 60000"),
			bk.Cmd("pushd browser"),
			bk.Cmd("yarn -s run build"),
			bk.Cmd("yarn -s run test-e2e"),
			bk.Cmd("popd"),
			bk.ArtifactPaths("./puppeteer/*.png"))
	}

	pipeline.AddWait()

	// Release to the Chrome Webstore
	pipeline.AddStep(":rocket::chrome:",
		bk.Env("FORCE_COLOR", "1"),
		bk.Cmd("yarn --frozen-lockfile --network-timeout 60000"),
		bk.Cmd("pushd browser"),
		bk.Cmd("yarn -s run build"),
		bk.Cmd("yarn release:chrome"),
		bk.Cmd("popd"))

	// Build and self sign the FF extension and upload it to ...
	pipeline.AddStep(":rocket::firefox:",
		bk.Env("FORCE_COLOR", "1"),
		bk.Cmd("yarn --frozen-lockfile --network-timeout 60000"),
		bk.Cmd("pushd browser"),
		bk.Cmd("yarn release:ff"),
		bk.Cmd("popd"))
}

func wait(pipeline *bk.Pipeline) {
	pipeline.AddWait()
}
