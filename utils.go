package main

import (
	"bytes"
	"fmt"
	"github.com/bitrise-io/go-utils/log"
	"os"
	"os/exec"
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
	Alpha2Code         string    `env:"ALPHA_2_CODE" json:"-"`        // Slack, Browserstack
	SlackFlag          string    `env:"SLACK_FLAG" json:"-"`          // Slack
	BuildRegion        string    `env:"SLACK_REGION" json:"-"`        // Slack
	GServicesXMLPath   string    `env:"GMS_XML" json:"-"`             // QA
	PackageName        string    `env:"PKG_NAME" json:"pkg"`          // Prod
	BrowserstackSuffix string    `env:"BS_SUFFIX" json:"bs_suffix"`   // Browserstack
	NewTag             string    `env:"-" json:"new_tag"`             // Internal
	NewCommitHash      string    `env:"-" json:"new_commit_hash"`     // Internal
	TgtBuildType       BuildType `env:"BUILD_TYPE" json:"build_type"` // Internal
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

func joinIgnoreEmpty(items []string, sep string) string {
	var ret strings.Builder
	for _, item := range items {
		if item != "" {
			if ret.Len() > 0 {
				ret.WriteString(sep)
			}
			ret.WriteString(item)
		}
	}
	return ret.String()
}

func removeKeywords(lut []string, original string, sep string) string {
	var newTagBuilder strings.Builder
	for _, token := range strings.Split(original, sep) {
		exists := false
		for _, l := range lut {
			if strings.ToLower(token) == strings.ToLower(l) {
				exists = true
				break
			}
		}
		if exists {
			continue
		} else {
			if newTagBuilder.Len() != 0 {
				newTagBuilder.WriteString("-")
			}
			newTagBuilder.WriteString(token)
		}
	}
	return newTagBuilder.String()
}

func generateNewTag(currentTag string, version string, a2 string, rc string, buildType BuildType) string {
	lut := []string{currentTag, version, a2, rc, "ALL", "APK"}
	newTag := removeKeywords(lut, currentTag, "-")
	switch buildType {
	case Qa:
		newTag = joinIgnoreEmpty([]string{version, newTag, a2, rc}, "-")
		break
	case Release:
		newTag = fmt.Sprintf("%s-%s", version, a2)
		break
	}
	return newTag
}

func generateBuildParams(supportedRegions map[string]string, allTagExcludes map[string]bool, defaultRegion string) []BuildParams {
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

	a2codes := make([]string, len(supportedRegions))
	i := 0
	for key := range supportedRegions {
		a2codes[i] = key
		i++
	}

	versionExp := regexp.MustCompile(`\d+\.\d+\.\d+`)
	rcExp := regexp.MustCompile(`RC\d+`)
	regionExp := regexp.MustCompile(strings.Join(a2codes, "|"))
	vendorSvcExp := regexp.MustCompile(`(G|H)MS`)

	version := findStringOrDefault(versionExp, token, NONE)
	rc := findStringOrDefault(rcExp, token, NONE)
	regionA2 := findStringOrDefault(regionExp, token, NONE)
	vendorSvc := findStringOrDefault(vendorSvcExp, token, NONE)
	isApk := strings.Contains(token, "-APK")

	envLogFmt := "Environment information:\nversion=\"%s\"\nrc=\"%s\"\nregionA2=\"%s\"\nvendorSvc=\"%s\""
	//println(fmt.Sprintf(envLogFmt, version, rc, regionA2, vendorSvc))
	log.Infof(fmt.Sprintf(envLogFmt, version, rc, regionA2, vendorSvc))

	if vendorSvc == NONE {
		vendorSvc = "GMS"
	}

	if version != NONE && rc == NONE {
		buildType = Release
	}

	buildCmd := "assemble"
	if !isApk && buildType == Release && vendorSvc == "GMS" {
		buildCmd = "bundle"
	}

	var buildRegions []string
	var newTagMapping = make(map[string]string)
	mapping, exists := supportedRegions[strings.ToUpper(regionA2)]
	if exists {
		// single build
		buildRegions = append(buildRegions, mapping)
	} else if toBool("PR") {
		// fallback to SG builds on PRs
		buildRegions = append(buildRegions, supportedRegions[defaultRegion])
	} else {
		// "ALL" build, iterate supported regions
		for a2, region := range supportedRegions {
			// remember to exclude it tho
			if !allTagExcludes[a2] {
				newTagMapping[region] = generateNewTag(token, version, a2, rc, buildType)
				buildRegions = append(buildRegions, region)
			}
		}
	}

	var buildParams []BuildParams
	var regionToA2 = reverseMap(&supportedRegions)
	for _, buildRegion := range buildRegions {
		flavor := snakify(buildRegion, "gms")
		a2Code := regionToA2[buildRegion]
		bsSuffix := "QA"

		if buildType == Release {
			bsSuffix = "PROD"
		}

		buildParam := BuildParams{
			GradleBuildTask:    snakify(buildCmd, buildRegion, strings.ToLower(vendorSvc), buildType.Name()),
			Alpha2Code:         a2Code,
			SlackFlag:          fmt.Sprintf(":flag-%s:", strings.ToLower(a2Code)),
			BuildRegion:        strings.Title(buildRegion),
			GServicesXMLPath:   fmt.Sprintf(GSERVICES_XML_FILE_PATH, flavor, buildType.Name()),
			PackageName:        generatePackageName(buildRegion, a2Code, &buildType),
			BrowserstackSuffix: bsSuffix,
			NewTag:             newTagMapping[buildRegion],
			NewCommitHash:      revParseTag(os.Getenv("BITRISE_GIT_TAG")),
			TgtBuildType:       buildType,
		}

		buildParams = append(buildParams, buildParam)
	}

	return buildParams
}

func revParseTag(tag string) string {
	if _, defined := os.LookupEnv("BITRISE_GIT_COMMIT"); defined {
		return ""
	}
	cmd := exec.Command("git", "rev-parse", tag)
	cmd.Dir = os.Getenv("BITRISE_SOURCE_DIR")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return ""
	}
	return out.String()
}
