// gen-pipeline.go generates a Buildkite YAML file that tests the entire
// Sourcegraph application and writes it to stdout.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	bk "github.com/sourcegraph/sourcegraph/pkg/buildkite"
)

func init() {
	bk.Plugins["gopath-checkout#v1.0.1"] = map[string]string{
		"import": "github.com/sourcegraph/sourcegraph",
	}
}

type config struct {
	now               time.Time
	branch            string
	version           string
	commit            string
	mustIncludeCommit string

	taggedRelease       bool
	releaseBranch       bool
	isBextReleaseBranch bool
	patch               bool
	patchNoTest         bool
}

func computeConfig() config {
	now := time.Now()
	branch := os.Getenv("BUILDKITE_BRANCH")
	version := os.Getenv("BUILDKITE_TAG")
	commit := os.Getenv("BUILDKITE_COMMIT")
	if commit == "" {
		commit = "1234567890123456789012345678901234567890" // for testing
	}

	taggedRelease := true // true if this is a tagged release
	switch {
	case strings.HasPrefix(branch, "docker-images-debug/"):
		// A branch like "docker-images-debug/foobar" will produce Docker images
		// tagged as "debug-foobar-$COMMIT".
		version = fmt.Sprintf("debug-%s-%s", strings.TrimPrefix(branch, "docker-images-debug/"), commit)
	case strings.HasPrefix(version, "v"):
		// The Git tag "v1.2.3" should map to the Docker image "1.2.3" (without v prefix).
		version = strings.TrimPrefix(version, "v")
	default:
		taggedRelease = false
		buildNum, _ := strconv.Atoi(os.Getenv("BUILDKITE_BUILD_NUMBER"))
		version = fmt.Sprintf("%05d_%s_%.7s", buildNum, now.Format("2006-01-02"), commit)
	}

	patchNoTest := strings.HasPrefix(branch, "docker-images-patch-notest/")
	patch := strings.HasPrefix(branch, "docker-images-patch/")
	if patchNoTest || patch {
		version = version + "_patch"
	}

	return config{
		now:               now,
		branch:            branch,
		version:           version,
		commit:            commit,
		mustIncludeCommit: os.Getenv("MUST_INCLUDE_COMMIT"),

		taggedRelease:       taggedRelease,
		releaseBranch:       regexp.MustCompile(`^[0-9]+\.[0-9]+$`).MatchString(branch),
		isBextReleaseBranch: branch == "bext/release",
		patch:               patch,
		patchNoTest:         patchNoTest,
	}
}

func (c config) generate() (*bk.Pipeline, error) {
	if c.mustIncludeCommit != "" {
		output, err := exec.Command("git", "merge-base", "--is-ancestor", c.mustIncludeCommit, "HEAD").CombinedOutput()
		if err != nil {
			fmt.Printf("This branch %s at commit %s does not include commit %s.\n", c.branch, c.commit, c.mustIncludeCommit)
			fmt.Println("Rebase onto the latest master to get the latest CI fixes.")
			fmt.Println(string(output))
			return nil, err
		}
	}

	pipeline := &bk.Pipeline{}

	// Common build env
	bk.OnEveryStepOpts = append(bk.OnEveryStepOpts,
		bk.Env("GO111MODULE", "on"),
		bk.Env("PUPPETEER_SKIP_CHROMIUM_DOWNLOAD", "true"),
		bk.Env("FORCE_COLOR", "1"),
		bk.Env("ENTERPRISE", "1"),
		bk.Env("COMMIT_SHA", c.commit),
		bk.Env("DATE", c.now.Format(time.RFC3339)),
	)

	// Generate pipeline steps
	var pipelineOperations []func(*bk.Pipeline)
	switch {
	case c.isPR() && isDocsOnly():
		pipelineOperations = []func(*bk.Pipeline){
			generateDocsOnly,
		}
	case c.patchNoTest:
		pipelineOperations = []func(*bk.Pipeline){
			dockerImageConfig{config: c, app: c.branch[27:], insiders: false}.generate,
		}
	case c.isBextReleaseBranch:
		pipelineOperations = []func(*bk.Pipeline){
			generateLint,
			generateBuildBrowserExt,
			generateSharedTests,
			wait,
			generateCodeCov,
			addBrowserExtensionReleaseSteps,
		}
	default:
		pipelineOperations = []func(*bk.Pipeline){
			c.generateBuildServerDockerImage,
			generateCheck,
			generateLint,
			generateBuildBrowserExt,
			generateBuildWebApp,
			generateSharedTests,
			generatePostgresBackcompatTest,
			generateGoTests,
			generateGoBuild,
			generateDockerfileLint,
			wait,
			c.generateE2ETest,
			wait,
			generateCodeCov,
			wait,
			c.generateDockerBuilds,
			c.generateCleanup, // TODO: cleanup Docker build -- can roll this into something else?
		}
	}

	for _, p := range pipelineOperations {
		p(pipeline)
	}

	return pipeline, nil
}

func (c config) isPR() bool {
	return !c.isBextReleaseBranch &&
		!c.releaseBranch &&
		!c.taggedRelease &&
		c.branch != "master" &&
		!strings.HasPrefix(c.branch, "master-dry-run/") &&
		!strings.HasPrefix(c.branch, "docker-images-patch/")
}

func isDocsOnly() bool {
	output, err := exec.Command("git", "diff", "--name-only", "origin/master...").Output()
	if err != nil {
		panic(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if !strings.HasPrefix(line, "doc") && line != "CHANGELOG.md" {
			return false
		}
	}
	return true
}

func main() {
	pipeline, err := computeConfig().generate()
	if err != nil {
		panic(err)
	}
	_, err = pipeline.WriteTo(os.Stdout)
	if err != nil {
		panic(err)
	}
}
