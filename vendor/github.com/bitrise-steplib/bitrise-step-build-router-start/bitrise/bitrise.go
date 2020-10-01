package bitrise

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bitrise-io/go-utils/log"
	"github.com/hashicorp/go-retryablehttp"
)

// Build ...
type Build struct {
	Slug                string          `json:"slug"`
	Status              int             `json:"status"`
	StatusText          string          `json:"status_text"`
	BuildNumber         int64           `json:"build_number"`
	TriggeredWorkflow   string          `json:"triggered_workflow"`
	OriginalBuildParams json.RawMessage `json:"original_build_params"`
}

type buildResponse struct {
	Data Build `json:"data"`
}

type hookInfo struct {
	Type string `json:"type"`
}

type startRequest struct {
	HookInfo    hookInfo        `json:"hook_info"`
	BuildParams json.RawMessage `json:"build_params"`
}

// StartResponse ...
type StartResponse struct {
	Status            string `json:"message"`
	Message           string `json:"status"`
	BuildSlug         string `json:"build_slug"`
	BuildNumber       int    `json:"build_number"`
	BuildURL          string `json:"build_url"`
	TriggeredWorkflow string `json:"triggered_workflow"`
}

type buildAbortParams struct {
	AbortReason       string `json:"abort_reason"`
	AbortWithSucces   bool   `json:"abort_with_success"`
	SkipNotifications bool   `json:"skip_notifications"`
}

// BuildArtifactsResponse ...
type BuildArtifactsResponse struct {
	ArtifactSlugs []BuildArtifactSlug `json:"data"`
}

// BuildArtifactSlug ...
type BuildArtifactSlug struct {
	ArtifactSlug string `json:"slug"`
}

// BuildArtifactResponse ...
type BuildArtifactResponse struct {
	Artifact BuildArtifact `json:"data"`
}

// BuildArtifact ...
type BuildArtifact struct {
	DownloadURL string `json:"expiring_download_url"`
	Title       string `json:"title"`
}

// Environment ...
type Environment struct {
	MappedTo string `json:"mapped_to"`
	Value    string `json:"value"`
}

// App ...
type App struct {
	BaseURL, Slug, AccessToken string
	IsDebugRetryTimings        bool
}

// NewAppWithDefaultURL returns a Bitrise client with the default URl
func NewAppWithDefaultURL(slug, accessToken string) App {
	return App{
		BaseURL:     "https://api.bitrise.io",
		Slug:        slug,
		AccessToken: accessToken,
	}
}

// RetryLogAdaptor adapts the retryablehttp.Logger interface to the go-utils logger.
type RetryLogAdaptor struct{}

// Printf implements the retryablehttp.Logger interface
func (*RetryLogAdaptor) Printf(fmtStr string, vars ...interface{}) {
	switch {
	case strings.HasPrefix(fmtStr, "[DEBUG]"):
		log.Debugf(strings.TrimSpace(fmtStr[7:]), vars...)
	case strings.HasPrefix(fmtStr, "[ERR]"):
		log.Errorf(strings.TrimSpace(fmtStr[5:]), vars...)
	case strings.HasPrefix(fmtStr, "[ERROR]"):
		log.Errorf(strings.TrimSpace(fmtStr[7:]), vars...)
	case strings.HasPrefix(fmtStr, "[WARN]"):
		log.Warnf(strings.TrimSpace(fmtStr[6:]), vars...)
	case strings.HasPrefix(fmtStr, "[INFO]"):
		log.Infof(strings.TrimSpace(fmtStr[6:]), vars...)
	default:
		log.Printf(fmtStr, vars...)
	}
}

// NewRetryableClient returns a retryable HTTP client
// isDebugRetryTimings sets the timeouts shoreter for testing purposes
func NewRetryableClient(isDebugRetryTimings bool) *retryablehttp.Client {
	client := retryablehttp.NewClient()
	client.CheckRetry = retryablehttp.DefaultRetryPolicy
	client.Backoff = retryablehttp.DefaultBackoff
	client.Logger = &RetryLogAdaptor{}
	client.ErrorHandler = retryablehttp.PassthroughErrorHandler
	if !isDebugRetryTimings {
		client.RetryWaitMin = 10 * time.Second
		client.RetryWaitMax = 60 * time.Second
		client.RetryMax = 5
	} else {
		client.RetryWaitMin = 100 * time.Millisecond
		client.RetryWaitMax = 400 * time.Millisecond
		client.RetryMax = 3
	}

	return client
}

// GetBuild ...
func (app App) GetBuild(buildSlug string) (build Build, err error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v0.1/apps/%s/builds/%s", app.BaseURL, app.Slug, buildSlug), nil)
	if err != nil {
		return Build{}, err
	}

	req.Header.Add("Authorization", "token "+app.AccessToken)

	retryReq, err := retryablehttp.FromRequest(req)
	if err != nil {
		return Build{}, fmt.Errorf("failed to create retryable request: %s", err)
	}

	client := NewRetryableClient(app.IsDebugRetryTimings)

	resp, err := client.Do(retryReq)
	if err != nil {
		return Build{}, err
	}

	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return Build{}, err
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Build{}, fmt.Errorf("failed to get response, statuscode: %d, body: %s", resp.StatusCode, respBody)
	}

	var buildResponse buildResponse
	if err := json.Unmarshal(respBody, &buildResponse); err != nil {
		return Build{}, fmt.Errorf("failed to decode response, body: %s, error: %s", respBody, err)
	}
	return buildResponse.Data, nil
}

// StartBuild ...
func (app App) StartBuild(workflow string, buildParams json.RawMessage, buildNumber string, environments []Environment) (startResponse StartResponse, err error) {
	var params map[string]interface{}
	if err := json.Unmarshal(buildParams, &params); err != nil {
		return StartResponse{}, err
	}
	params["workflow_id"] = workflow
	params["skip_git_status_report"] = true

	sourceBuildNumber := Environment{
		MappedTo: "SOURCE_BITRISE_BUILD_NUMBER",
		Value:    buildNumber,
	}

	envs := []Environment{sourceBuildNumber}
	params["environments"] = append(envs, environments...)

	b, err := json.Marshal(params)
	if err != nil {
		return StartResponse{}, nil
	}

	rm := startRequest{HookInfo: hookInfo{Type: "bitrise"}, BuildParams: b}
	b, err = json.Marshal(rm)
	if err != nil {
		return StartResponse{}, nil
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/v0.1/apps/%s/builds", app.BaseURL, app.Slug), bytes.NewReader(b))
	if err != nil {
		return StartResponse{}, nil
	}
	req.Header.Add("Authorization", "token "+app.AccessToken)

	retryReq, err := retryablehttp.FromRequest(req)
	if err != nil {
		return StartResponse{}, fmt.Errorf("failed to create retryable request: %s", err)
	}

	retryClient := NewRetryableClient(app.IsDebugRetryTimings)

	resp, err := retryClient.Do(retryReq)
	if err != nil {
		return StartResponse{}, nil
	}

	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return StartResponse{}, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return StartResponse{}, fmt.Errorf("failed to get response, statuscode: %d, body: %s", resp.StatusCode, respBody)
	}

	var response StartResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return StartResponse{}, fmt.Errorf("failed to decode response, body: %s, error: %s", respBody, err)
	}
	return response, nil
}

// GetBuildArtifacts ...
func (build Build) GetBuildArtifacts(app App) (BuildArtifactsResponse, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v0.1/apps/%s/builds/%s/artifacts", app.BaseURL, app.Slug, build.Slug), nil)
	if err != nil {
		return BuildArtifactsResponse{}, nil
	}
	req.Header.Add("Authorization", "token "+app.AccessToken)

	retryReq, err := retryablehttp.FromRequest(req)
	if err != nil {
		return BuildArtifactsResponse{}, fmt.Errorf("failed to create retryable request: %s", err)
	}

	retryClient := NewRetryableClient(app.IsDebugRetryTimings)

	resp, err := retryClient.Do(retryReq)
	if err != nil {
		return BuildArtifactsResponse{}, nil
	}

	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return BuildArtifactsResponse{}, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return BuildArtifactsResponse{}, fmt.Errorf("failed to get response, statuscode: %d, body: %s", resp.StatusCode, respBody)
	}

	var response BuildArtifactsResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return BuildArtifactsResponse{}, fmt.Errorf("failed to decode response, body: %s, error: %s", respBody, err)
	}
	return response, nil
}

// GetBuildArtifact ...
func (build Build) GetBuildArtifact(app App, artifactSlug string) (BuildArtifactResponse, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v0.1/apps/%s/builds/%s/artifacts/%s", app.BaseURL, app.Slug, build.Slug, artifactSlug), nil)
	if err != nil {
		return BuildArtifactResponse{}, nil
	}
	req.Header.Add("Authorization", "token "+app.AccessToken)

	retryReq, err := retryablehttp.FromRequest(req)
	if err != nil {
		return BuildArtifactResponse{}, fmt.Errorf("failed to create retryable request: %s", err)
	}

	retryClient := NewRetryableClient(app.IsDebugRetryTimings)

	resp, err := retryClient.Do(retryReq)
	if err != nil {
		return BuildArtifactResponse{}, nil
	}

	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return BuildArtifactResponse{}, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return BuildArtifactResponse{}, fmt.Errorf("failed to get response, statuscode: %d, body: %s", resp.StatusCode, respBody)
	}

	var response BuildArtifactResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return BuildArtifactResponse{}, fmt.Errorf("failed to decode response, body: %s, error: %s", respBody, err)
	}
	return response, nil
}

// DownloadArtifact ...
func (artifact BuildArtifact) DownloadArtifact(filepath string) error {
	resp, err := http.Get(artifact.DownloadURL)
	if err != nil {
		return err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Println("Failed to close body, error:", err)
		}
	}()

	out, err := os.Create(filepath)
	if err != nil {
		return err
	}

	defer func() {
		if err := out.Close(); err != nil {
			fmt.Println("Failed to close output stream, error:", err)
		}
	}()

	_, err = io.Copy(out, resp.Body)
	return err
}

// AbortBuild ...
func (app App) AbortBuild(buildSlug string, abortReason string) error {
	b, err := json.Marshal(buildAbortParams{
		AbortReason:       abortReason,
		AbortWithSucces:   false,
		SkipNotifications: true})

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/v0.1/apps/%s/builds/%s/abort", app.BaseURL, app.Slug, buildSlug), bytes.NewReader(b))
	if err != nil {
		return nil
	}
	req.Header.Add("Authorization", "token "+app.AccessToken)

	retryReq, err := retryablehttp.FromRequest(req)
	if err != nil {
		return fmt.Errorf("failed to create retryable request: %s", err)
	}

	retryClient := NewRetryableClient(app.IsDebugRetryTimings)

	resp, err := retryClient.Do(retryReq)
	if err != nil {
		return nil
	}

	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("failed to get response, statuscode: %d, body: %s", resp.StatusCode, respBody)
	}
	return nil
}

// WaitForBuilds ...
func (app App) WaitForBuilds(buildSlugs []string, statusChangeCallback func(build Build)) error {
	failed := false
	status := map[string]string{}
	for {
		running := 0
		for _, buildSlug := range buildSlugs {
			build, err := app.GetBuild(buildSlug)
			if err != nil {
				return fmt.Errorf("failed to get build info, error: %s", err)
			}

			if status[buildSlug] != build.StatusText {
				statusChangeCallback(build)
				status[buildSlug] = build.StatusText
			}

			if build.Status == 0 {
				running++
				continue
			}

			if build.Status != 1 {
				failed = true
			}

			buildSlugs = remove(buildSlugs, buildSlug)
		}
		if running == 0 {
			break
		}
		time.Sleep(time.Second * 3)
	}
	if failed {
		return fmt.Errorf("at least one build failed or aborted")
	}
	return nil
}

func remove(slice []string, what string) (b []string) {
	for _, s := range slice {
		if s != what {
			b = append(b, s)
		}
	}
	return
}
