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
	ParentBuild     string          `env:"SOURCE_BITRISE_BUILD_NUMBER"`
	AppSlug         string          `env:"BITRISE_APP_SLUG,required"`
	BuildSlug       string          `env:"BITRISE_BUILD_SLUG,required"`
	BuildNumber     string          `env:"BITRISE_BUILD_NUMBER,required"`
	AccessToken     stepconf.Secret `env:"access_token,required"`
	RegionMap       string          `env:"region_mapping,required"`
	DebugWorkflow   string          `env:"debug_workflow,required"`
	QaWorkflow      string          `env:"qa_workflow,required"`
	ReleaseWorkflow string          `env:"release_workflow,required"`
	Environments    string          `env:"environment_key_list"`
	IsVerboseLog    bool            `env:"verbose,required"`
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

	regionMap := make(map[string]string)
	for _, line := range strings.Split(cfg.RegionMap, "\n") {
		pair := strings.Split(line, "=")
		key := pair[0]
		value := pair[1]
		regionMap[key] = value
	}

	var buildSlugs []string
	environments := createEnvs(cfg.Environments)

	for i, buildParam := range generateBuildParams(regionMap) {
		log.Infof(fmt.Sprintf("BuildParam: %v", buildParam))
		if i == 0 {
			writeBuildParamsToEnvs(&buildParam, nil) // write to envman directly!
		} else {
			newEnvs := writeBuildParamsToEnvs(&buildParam, &environments)
			startedBuild, err := app.StartBuild(cfg.DebugWorkflow, build.OriginalBuildParams, cfg.BuildNumber, newEnvs)
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
			env := bitrise.Environment{
				MappedTo: field.Tag.Get("env"),
				Value:    fmt.Sprintf("%v", fieldValue.Interface()),
			}
			newEnvs = append(newEnvs, env)
		}
	}
	return newEnvs
}
