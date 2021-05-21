package main

import (
	"encoding/json"
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
	ParentBuild      string          `env:"SOURCE_BITRISE_BUILD_NUMBER"`
	AppSlug          string          `env:"BITRISE_APP_SLUG,required"`
	BuildSlug        string          `env:"BITRISE_BUILD_SLUG,required"`
	BuildNumber      string          `env:"BITRISE_BUILD_NUMBER,required"`
	AccessToken      stepconf.Secret `env:"access_token,required"`
	SupportedRegions string          `env:"supported_regions,required"`
	AllTagExcludes   string          `env:"all_tag_excludes"`
	IsVerboseLog     bool            `env:"verbose,required"`
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

	if cfg.ParentBuild == "" {
		log.Infof("I am the master. I will fork more if necessary")
	} else {
		log.Infof("Bypassing script, child build of %s", cfg.ParentBuild)
		return
	}

	log.SetEnableDebugLog(cfg.IsVerboseLog)

	app := bitrise.NewAppWithDefaultURL(cfg.AppSlug, string(cfg.AccessToken))

	build, err := app.GetBuild(cfg.BuildSlug)
	if err != nil {
		failf("failed to get build, error: %s", err)
	}

	log.Infof("Starting builds:")

	// parse supported regions
	supportedRegions := make(map[string]string)
	for _, line := range strings.Split(cfg.SupportedRegions, "\n") {
		pair := strings.Split(line, "=")
		key := pair[0]
		value := pair[1]
		supportedRegions[key] = value
	}
	// parse all tag excludes
	allTagExcludes := make(map[string]bool)
	for _, line := range strings.Split(cfg.AllTagExcludes, "\n") {
		if len(line) != 0 {
			allTagExcludes[line] = true
		}
	}

	var buildSlugs []string
	var environments []bitrise.Environment

	for i, buildParam := range generateBuildParams(supportedRegions, allTagExcludes) {
		log.Infof(fmt.Sprintf("BuildParam: %v", buildParam))
		if i == 0 {
			writeBuildParamsToEnvs(&buildParam, nil) // write to envman directly!
			// rewrite tag if necessary
			if buildParam.NewTag != "" {
				oldTag := os.Getenv("BITRISE_GIT_TAG")
				log.Infof(fmt.Sprintf("Overriding TAG: %s -> %s", oldTag, buildParam.NewTag))
				if err := tools.ExportEnvironmentWithEnvman("BITRISE_GIT_TAG", buildParam.NewTag); err != nil {
					failf("Unable to overwrite BITRISE_GIT_TAG")
				}
			}
			if buildParam.NewCommitHash != "" {
				if err := tools.ExportEnvironmentWithEnvman("BITRISE_GIT_COMMIT", buildParam.NewCommitHash); err != nil {
					failf("Unable to overwrite BITRISE_GIT_COMMIT")
				}
			}
		} else {
			newEnvs := writeBuildParamsToEnvs(&buildParam, &environments)
			// always fork the triggered workflow
			workflow := os.Getenv("BITRISE_TRIGGERED_WORKFLOW_ID")
			startedBuild, err := app.StartBuild(
				workflow,
				tryInjectNewParamsToBuild(build, buildParam),
				cfg.BuildNumber,
				newEnvs,
			)
			if err != nil {
				failf("Failed to start build, error: %s", err)
			}
			buildSlugs = append(buildSlugs, startedBuild.BuildSlug)
			log.Printf("- %s started (https://app.bitrise.io/build/%s)", startedBuild.TriggeredWorkflow, startedBuild.BuildSlug)
		}
	}

	// Export the forked buildslug
	if err := tools.ExportEnvironmentWithEnvman(envBuildSlugs, strings.Join(buildSlugs, "\n")); err != nil {
		failf("Failed to export environment variable, error: %s", err)
	}
}

func writeBuildParamsToEnvs(buildParams *BuildParams, src *[]bitrise.Environment) []bitrise.Environment {
	var newEnvs []bitrise.Environment
	rType := reflect.TypeOf(*buildParams)
	rValue := reflect.ValueOf(*buildParams)
	if src == nil {
		for i := 0; i < rType.NumField(); i++ {
			field := rType.Field(i)
			fieldValue := rValue.Field(i)
			key := field.Tag.Get("env")
			value := fmt.Sprintf("%v", fieldValue.Interface())
			if err := tools.ExportEnvironmentWithEnvman(key, value); err != nil {
				failf("Failed to export environment variable, error: %s", err)
			}
		}
	} else {
		copy(newEnvs, *src)
		for i := 0; i < rType.NumField(); i++ {
			field := rType.Field(i)
			fieldValue := rValue.Field(i)
			if key := field.Tag.Get("env"); key != "-" {
				env := bitrise.Environment{
					MappedTo: key,
					Value:    fmt.Sprintf("%v", fieldValue.Interface()),
				}
				newEnvs = append(newEnvs, env)
			}
		}
	}
	return newEnvs
}

func tryInjectNewParamsToBuild(build bitrise.Build, newParams BuildParams) json.RawMessage {
	var params map[string]interface{}
	if err := json.Unmarshal(build.OriginalBuildParams, &params); err != nil {
		failf("Forbidden technique doesn't work!")
	}
	if tag := newParams.NewTag; tag != "" {
		params["tag"] = tag
	}
	if newCommitHash := newParams.NewCommitHash; newCommitHash != "" {
		params["commit_hash"] = newCommitHash
	}
	params["triggered_by"] = fmt.Sprintf("Build #%d", build.BuildNumber)
	result, _ := json.Marshal(params)
	return result
}
