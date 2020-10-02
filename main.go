package main

import (
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/bitrise-io/go-steputils/stepconf"
	"github.com/bitrise-io/go-steputils/tools"
	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-steplib/bitrise-step-build-router-start/bitrise"
)

const envBuildSlugs = "ROUTER_STARTED_BUILD_SLUGS"

// Config ...
type Config struct {
	AppSlug                string          `env:"BITRISE_APP_SLUG,required"`
	BuildSlug              string          `env:"BITRISE_BUILD_SLUG,required"`
	BuildNumber            string          `env:"BITRISE_BUILD_NUMBER,required"`
	AccessToken            stepconf.Secret `env:"access_token,required"`
	WaitForBuilds          string          `env:"wait_for_builds"`
	BuildArtifactsSavePath string          `env:"build_artifacts_save_path"`
	AbortBuildsOnFail      string          `env:"abort_on_fail"`
	Workflows              string          `env:"workflows,required"`
	Environments           string          `env:"environment_key_list"`
	IsVerboseLog           bool            `env:"verbose,required"`
}

func failf(s string, a ...interface{}) {
	log.Errorf(s, a...)
	os.Exit(1)
}

func main() {
	var cfg Config
	if err := stepconf.Parse(&cfg); err != nil {
		failf("Issue with an input: %s", err)
	}

	stepconf.Print(cfg)
	fmt.Println()

	log.SetEnableDebugLog(cfg.IsVerboseLog)

	app := bitrise.NewAppWithDefaultURL(cfg.AppSlug, string(cfg.AccessToken))

	build, err := app.GetBuild(cfg.BuildSlug)
	if err != nil {
		failf("failed to get build, error: %s", err)
	}

	log.Infof("Starting builds:")

	var buildSlugs []string
	environments := createEnvs(cfg.Environments)
	for _, buildParam := range generateBuildParams() {
		log.Infof(fmt.Sprintf("BuildParam: %v", buildParam))
		newEnvs := writeBuildParamsToEnvs(&buildParam, &environments)
		startedBuild, err := app.StartBuild(cfg.Workflows, build.OriginalBuildParams, cfg.BuildNumber, newEnvs)
		if err != nil {
			failf("Failed to start build, error: %s", err)
		}
		buildSlugs = append(buildSlugs, startedBuild.BuildSlug)
		log.Printf("- %s started (https://app.bitrise.io/build/%s)", startedBuild.TriggeredWorkflow, startedBuild.BuildSlug)
	}
	//for _, wf := range strings.Split(strings.TrimSpace(cfg.Workflows), "\n") {
	//	wf = strings.TrimSpace(wf)
	//	startedBuild, err := app.StartBuild(wf, build.OriginalBuildParams, cfg.BuildNumber, environments)
	//	if err != nil {
	//		failf("Failed to start build, error: %s", err)
	//	}
	//	buildSlugs = append(buildSlugs, startedBuild.BuildSlug)
	//	log.Printf("- %s started (https://app.bitrise.io/build/%s)", startedBuild.TriggeredWorkflow, startedBuild.BuildSlug)
	//}

	if err := tools.ExportEnvironmentWithEnvman(envBuildSlugs, strings.Join(buildSlugs, "\n")); err != nil {
		failf("Failed to export environment variable, error: %s", err)
	}

	if cfg.WaitForBuilds != "true" {
		return
	}

	fmt.Println()
	log.Infof("Waiting for builds:")

	if err := app.WaitForBuilds(buildSlugs, func(build bitrise.Build) {
		var failReason string
		switch build.Status {
		case 0:
			log.Printf("- %s %s", build.TriggeredWorkflow, build.StatusText)
		case 1:
			log.Donef("- %s successful", build.TriggeredWorkflow)
		case 2:
			log.Errorf("- %s failed", build.TriggeredWorkflow)
			failReason = "failed"
		case 3:
			log.Warnf("- %s aborted", build.TriggeredWorkflow)
			failReason = "aborted"
		case 4:
			log.Infof("- %s cancelled", build.TriggeredWorkflow)
			failReason = "cancelled"
		}

		if cfg.AbortBuildsOnFail == "yes" && build.Status > 1 {
			for _, buildSlug := range buildSlugs {
				if buildSlug != build.Slug {
					abortErr := app.AbortBuild(buildSlug, "Abort on Fail - Build [https://app.bitrise.io/build/"+build.Slug+"] "+failReason+"\nAuto aborted by parent build")
					if abortErr != nil {
						log.Warnf("failed to abort build, error: %s", abortErr)
					}
					log.Donef("Build " + buildSlug + " aborted due to associated build failure")
				}
			}
		}

		if build.Status != 0 {
			if strings.TrimSpace(cfg.BuildArtifactsSavePath) != "" {
				artifactsResponse, err := build.GetBuildArtifacts(app)
				if err != nil {
					log.Warnf("failed to get build artifacts, error: %s", err)
				}
				for _, artifactSlug := range artifactsResponse.ArtifactSlugs {
					artifactObj, err := build.GetBuildArtifact(app, artifactSlug.ArtifactSlug)
					if err != nil {
						log.Warnf("failed to get build artifact, error: %s", err)
					}

					downloadErr := artifactObj.Artifact.DownloadArtifact(strings.TrimSpace(cfg.BuildArtifactsSavePath) + artifactObj.Artifact.Title)
					if downloadErr != nil {
						log.Warnf("failed to download artifact, error: %s", downloadErr)
					}
					log.Donef("Downloaded: " + artifactObj.Artifact.Title + " to path " + strings.TrimSpace(cfg.BuildArtifactsSavePath))
				}
			}
		}
	}); err != nil {
		failf("An error occoured: %s", err)
	}
}

func createEnvs(environmentKeys string) []bitrise.Environment {
	environmentKeys = strings.Replace(environmentKeys, "$", "", -1)
	environmentsKeyList := strings.Split(environmentKeys, "\n")

	var environments []bitrise.Environment
	for _, key := range environmentsKeyList {
		if key == "" {
			continue
		}

		env := bitrise.Environment{
			MappedTo: key,
			Value:    os.Getenv(key),
		}
		environments = append(environments, env)
	}
	return environments
}

func writeBuildParamsToEnvs(buildParams *BuildParams, src *[]bitrise.Environment) []bitrise.Environment {
	var newEnvs []bitrise.Environment
	copy(newEnvs, *src)
	rType := reflect.TypeOf(*buildParams)
	rValue := reflect.ValueOf(*buildParams)
	for i := 0; i < rType.NumField(); i++ {
		field := rType.Field(i)
		fieldValue := rValue.Field(i)
		env := bitrise.Environment{
			MappedTo: field.Tag.Get("env"),
			Value:    fmt.Sprintf("%v", fieldValue.Interface()),
		}
		newEnvs = append(newEnvs, env)
	}
	return newEnvs
}
