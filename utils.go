package main

import (
	"fmt"
	"github.com/bitrise-io/go-utils/log"
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
	GradleBuildTask    string    `env:"GRADLE_BUILD" json:"build_task"`
	Alpha2Code         string    `env:"ALPHA_2_CODE" json:"-"`      // Slack, Browserstack
	SlackFlag          string    `env:"SLACK_FLAG" json:"-"`        // Slack
	BuildRegion        string    `env:"SLACK_REGION" json:"-"`      // Slack
	GServicesXMLPath   string    `env:"GMS_XML" json:"-"`           // QA
	PackageName        string    `env:"PKG_NAME" json:"pkg"`        // Prod
	BrowserstackSuffix string    `env:"BS_SUFFIX" json:"bs_suffix"` // Browserstack
	NewTag             string    `env:"-" json:"new_tag"`           // Internal
	TgtBuildType       BuildType `env:"-" json:"build_type"`        // Internal
}

const NONE = "none"
const GSERVICES_XML_FILE_PATH = "accmng/build/generated/res/google-services/%s/%s/values/values.xml"

// Check the link below if the build fails
// https://developers.google.com/android/guides/google-services-plugin#processing_the_json_file

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

func generateNewTag(version string, a2 string, rc string, buildType BuildType) string {
	newTag := ""
	switch buildType {
	case Qa:
		newTag = fmt.Sprintf("%s-%s-%s", version, a2, rc)
		break
	case Release:
		newTag = fmt.Sprintf("%s-%s", version, a2)
		break
	}
	return newTag
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
		os.Exit(1)
	}

	versionExp := regexp.MustCompile(`\d+\.\d+\.\d+`)
	rcExp := regexp.MustCompile(`(?i)RC\d+`)
	regionExp := regexp.MustCompile(`(?i)au|tw|sg|id`)
	vendorSvcExp := regexp.MustCompile(`(?i)(?:g|h)ms`)

	version := findStringOrDefault(versionExp, token, NONE)
	rc := findStringOrDefault(rcExp, token, NONE)
	regionA2 := findStringOrDefault(regionExp, token, NONE)
	vendorSvc := strings.ToLower(findStringOrDefault(vendorSvcExp, token, NONE))

	envLogFmt := "Environment information:\nversion=\"%s\"\nrc=\"%s\"\nregionA2=\"%s\"\nvendorSvc=\"%s\""
	// println(fmt.Sprintf(envLogFmt, version, rc, regionA2, vendorSvc))
	log.Infof(fmt.Sprintf(envLogFmt, version, rc, regionA2, vendorSvc))

	if vendorSvc == NONE {
		vendorSvc = "gms"
	}

	if version != NONE && rc == NONE {
		buildType = Release
	}

	buildCmd := "assemble"
	if buildType == Release && vendorSvc == "gms" {
		buildCmd = "bundle"
	}

	var buildRegions []string
	var newTagMapping = make(map[string]string)
	mapping, exists := regionMap[strings.ToUpper(regionA2)]
	if exists {
		buildRegions = append(buildRegions, mapping)
	} else if toBool("PR") {
		// fallback to SG builds on PRs
		buildRegions = append(buildRegions, "singapore")
	} else {
		for a2, region := range regionMap {
			newTagMapping[region] = generateNewTag(version, a2, rc, buildType)
			buildRegions = append(buildRegions, region)
		}
	}

	var buildParams []BuildParams
	var regionToA2 = reverseMap(&regionMap)
	for _, buildRegion := range buildRegions {
		flavor := snakify(buildRegion, "gms")
		a2Code := regionToA2[buildRegion]
		bsSuffix := "QA"

		if buildType == Release {
			bsSuffix = "PROD"
		}

		buildParam := BuildParams{
			GradleBuildTask:    snakify(buildCmd, buildRegion, vendorSvc, buildType.Name()),
			Alpha2Code:         a2Code,
			SlackFlag:          fmt.Sprintf(":flag-%s:", strings.ToLower(a2Code)),
			BuildRegion:        strings.Title(buildRegion),
			GServicesXMLPath:   fmt.Sprintf(GSERVICES_XML_FILE_PATH, flavor, buildType.Name()),
			PackageName:        generatePackageName(buildRegion, a2Code, &buildType),
			BrowserstackSuffix: bsSuffix,
			NewTag:             newTagMapping[buildRegion],
			TgtBuildType:       buildType,
		}

		buildParams = append(buildParams, buildParam)
	}

	return buildParams
}
