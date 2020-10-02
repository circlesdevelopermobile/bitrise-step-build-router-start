package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type BuildType int

const (
	Debug BuildType = iota
	Qa
	Release
)

func (bt BuildType) Name() string {
	switch bt {
	case Debug:
		return "debug"
	case Qa:
		return "qa"
	case Release:
		return "release"
	default:
		return ""
	}
}

type BuildParams struct {
	GradleBuildTask  string `env:"GRADLE_BUILD"`
	Alpha2Code       string `env:"ALPHA_2_CODE"` // Slack, Browserstack
	BuildRegion      string `env:"SLACK_REGION"` // Slack
	GServicesXMLPath string `env:"GMS_XML"`      // QA
	PackageName      string `env:"PKG_NAME"`     // Prod
}

const NONE = "none"
const GSERVICES_XML_FILE_PATH = "accmng/build/generated/res/google-services/%s/%s/values/values.xml"

func reverseMap(m *map[string]string) map[string]string {
	tmpMap := make(map[string]string)
	for k, v := range *m {
		tmpMap[v] = k
	}
	return tmpMap
}

func toBool(envvar string) bool {
	if pr, ok := os.LookupEnv(envvar); ok {
		if b, _ := strconv.ParseBool(pr); b {
			return true
		}
	}
	return false
}

func snakify(words ...string) string {
	out := ""
	for _, word := range words {
		if len(out) == 0 {
			out = out + word
		} else {
			out = out + strings.Title(word)
		}
	}
	return out
}

func findStringOrDefault(re *regexp.Regexp, str string, def string) string {
	if match := re.FindString(str); match != "" {
		return match
	}
	return def
}

func generatePackageName(region string, a2code string, buildType *BuildType) string {
	basePkg := "com.circles.selfcare"
	if region != "singapore" {
		basePkg = basePkg + "." + strings.ToLower(a2code)
	}
	if *buildType != Release {
		basePkg = basePkg + "." + buildType.Name()
	}
	return basePkg
}

func generateBuildParams(regionMap map[string]string) []BuildParams {
	var token string

	buildType := Debug

	if tag, ok := os.LookupEnv("BITRISE_GIT_TAG"); ok {
		token = tag
		buildType = Qa
	} else if branch, ok := os.LookupEnv("BITRISE_GIT_BRANCH"); ok {
		if strings.Contains(branch, "/") {
			token = branch[strings.Index(tag, "/")+1:]
		} else {
			token = branch
		}
	} else {
		token = "1.2.3-SG-RC1" // Sample only
	}

	versionExp := regexp.MustCompile(`\d+\.\d+\.\d+`)
	rcExp := regexp.MustCompile(`(?i)RC\d+`)
	regionExp := regexp.MustCompile(`(?i)au|tw|sg|id`)
	vendorSvcExp := regexp.MustCompile(`(?i)(?:g|h)ms`)

	version := findStringOrDefault(versionExp, token, NONE)
	rc := findStringOrDefault(rcExp, token, NONE)
	regionA2 := strings.ToUpper(findStringOrDefault(regionExp, token, ""))
	vendorSvc := strings.ToLower(findStringOrDefault(vendorSvcExp, token, NONE))

	if vendorSvc == NONE {
		vendorSvc = "gms"
	}

	if version != NONE && rc != NONE {
		buildType = Release
	}

	buildCmd := "assemble"
	if buildType == Release && vendorSvc == "gms" {
		buildCmd = "bundle"
	}
	var buildRegions []string
	if toBool("PR") || regionA2 != "" {
		var buildRegion string
		mapping, exists := regionMap[regionA2]
		if exists {
			buildRegion = mapping
		} else {
			buildRegion = "singapore"
		}
		buildRegions = append(buildRegions, buildRegion)
	} else {
		for _, v := range regionMap {
			buildRegions = append(buildRegions, v)
		}
	}

	var buildParams []BuildParams
	var invMap = reverseMap(&regionMap)
	for _, buildRegion := range buildRegions {
		flavor := snakify(buildRegion, "gms")
		a2Code := invMap[buildRegion]
		buildParam := BuildParams{
			GradleBuildTask:  snakify(buildCmd, buildRegion, vendorSvc, buildType.Name()),
			Alpha2Code:       invMap[buildRegion],
			BuildRegion:      strings.Title(buildRegion),
			GServicesXMLPath: fmt.Sprintf(GSERVICES_XML_FILE_PATH, flavor, buildType.Name()),
			PackageName:      generatePackageName(buildRegion, a2Code, &buildType),
		}
		buildParams = append(buildParams, buildParam)
	}

	return buildParams
}