package main

import (
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

const NONE = "none"

var REGION_MAP = map[string]string{
	"SG": "singapore",
	"TW": "taiwan",
	"AU": "australia",
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

func regex() []string {
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

	println(token)

	versionExp := regexp.MustCompile(`\d+\.\d+\.\d+`)
	rcExp := regexp.MustCompile(`(?i)RC\d+`)
	regionExp := regexp.MustCompile(`(?i)au|tw|sg|id`)
	vendorSvcExp := regexp.MustCompile(`(?i)(?:g|h)ms`)

	version := findStringOrDefault(versionExp, token, NONE)
	rc := findStringOrDefault(rcExp, token, NONE)
	region := findStringOrDefault(regionExp, token, "")
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
	if toBool("PR") || region != "" {
		var buildRegion string
		mapping, exists := REGION_MAP[region]
		if exists {
			buildRegion = mapping
		} else {
			buildRegion = "singapore"
		}
		buildRegions = append(buildRegions, buildRegion)
	} else {
		for _, v := range REGION_MAP {
			buildRegions = append(buildRegions, v)
		}
	}

	var buildCommands []string
	for _, buildRegion := range buildRegions {
		buildCommands = append(buildCommands, snakify(buildCmd, buildRegion, vendorSvc, buildType.Name()))
	}

	return buildCommands
}
