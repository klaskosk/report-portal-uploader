package send

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/google/uuid"
	"github.com/klaskosk/report-portal-cli/pkg/types"
)

// Metadata holds parsed metadata for the Report Portal target.
type Metadata struct {
	Disabled               bool
	Hostname               string
	HostnameURL            string
	Project                string
	PropertyFilter         []string
	PropertyFilterCompiled []*regexp.Regexp
	ApplyTFA               bool
	ApplyLLMAnalysis       bool
	DisableTestItemUpdater bool
	TestCaseConcatChar     string
	LaunchName             string
	LaunchDescription      string
	LaunchAttributes       []LaunchAttribute
	RequestID              string
}

// LaunchAttribute is a launch-level attribute from metadata JSON.
type LaunchAttribute struct {
	Key    string
	Value  string
	System bool
}

// ParseMetadataFile substitutes template variables and parses targets.reportportal.
func ParseMetadataFile(path string, vars map[string]string) (*Metadata, error) {
	processedPath, err := substituteMetadataVars(path, vars)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(processedPath) }()

	// #nosec G304 -- processedPath is a temp file created by this package
	content, err := os.ReadFile(processedPath)
	if err != nil {
		return nil, fmt.Errorf("reading metadata: %w", err)
	}

	return ParseMetadataJSON(content)
}

type metadataJSONRoot struct {
	Targets struct {
		ReportPortal *reportPortalTargetJSON `json:"reportportal"`
	} `json:"targets"`
}

type reportPortalTargetJSON struct {
	Disabled bool `json:"disabled"`
	Config   struct {
		Hostname string `json:"hostname"`
		Project  string `json:"project"`
	} `json:"config"`
	Processing struct {
		PropertyFilter         []string `json:"property_filter"`
		ApplyTFA               *bool    `json:"apply_tfa"`
		ApplyLLMAnalysis       *bool    `json:"apply_llm_analysis"`
		DisableTestItemUpdater bool     `json:"disable_testitem_updater"`
		TestCaseConcatChar     string   `json:"testcase_concat_char"`
		Launch                 struct {
			Name        string           `json:"name"`
			Description string           `json:"description"`
			Attributes  []map[string]any `json:"attributes"`
		} `json:"launch"`
	} `json:"processing"`
}

// ParseMetadataJSON parses raw metadata JSON bytes.
func ParseMetadataJSON(content []byte) (*Metadata, error) {
	var root metadataJSONRoot

	err := json.Unmarshal(content, &root)
	if err != nil {
		return nil, fmt.Errorf("parsing metadata JSON: %w", err)
	}

	if root.Targets.ReportPortal == nil {
		return nil, errors.New("metadata missing targets.reportportal")
	}

	return metadataFromReportPortalTarget(root.Targets.ReportPortal)
}

func metadataFromReportPortalTarget(target *reportPortalTargetJSON) (*Metadata, error) {
	meta := &Metadata{
		Disabled:               target.Disabled,
		Hostname:               strings.TrimSpace(target.Config.Hostname),
		Project:                strings.TrimSpace(target.Config.Project),
		DisableTestItemUpdater: target.Processing.DisableTestItemUpdater,
		TestCaseConcatChar:     target.Processing.TestCaseConcatChar,
		LaunchName:             strings.TrimSpace(target.Processing.Launch.Name),
		LaunchDescription:      target.Processing.Launch.Description,
		RequestID:              uuid.NewString(),
	}

	if target.Processing.ApplyTFA != nil {
		meta.ApplyTFA = *target.Processing.ApplyTFA
	}

	if target.Processing.ApplyLLMAnalysis != nil {
		meta.ApplyLLMAnalysis = *target.Processing.ApplyLLMAnalysis
	}

	err := validateRequiredMetadataFields(meta)
	if err != nil {
		return nil, err
	}

	meta.HostnameURL = hostnameURL(meta.Hostname)

	if meta.LaunchName == "" {
		meta.LaunchName = DefaultLaunchName
	}

	err = applyPropertyFilters(meta, target.Processing.PropertyFilter)
	if err != nil {
		return nil, err
	}

	attrs, _ := parseLaunchAttributes(target.Processing.Launch.Attributes)
	meta.LaunchAttributes = injectLaunchAttributes(attrs, meta.RequestID)

	err = validateConcatChar(meta.TestCaseConcatChar)
	if err != nil {
		return nil, err
	}

	return meta, nil
}

func validateRequiredMetadataFields(meta *Metadata) error {
	if meta.Hostname == "" {
		return errors.New("metadata missing targets.reportportal.config.hostname")
	}

	if meta.Project == "" {
		return errors.New("metadata missing targets.reportportal.config.project")
	}

	return nil
}

func applyPropertyFilters(meta *Metadata, patterns []string) error {
	if patterns == nil {
		return nil
	}

	meta.PropertyFilter = patterns

	compiled, err := compilePropertyFilters(meta.PropertyFilter)
	if err != nil {
		return err
	}

	meta.PropertyFilterCompiled = compiled

	return nil
}

func substituteMetadataVars(path string, vars map[string]string) (string, error) {
	err := validateCLIPath(path)
	if err != nil {
		return "", fmt.Errorf("reading metadata template: %w", err)
	}

	content, err := os.ReadFile(path) //nolint:gosec // path validated above
	if err != nil {
		return "", fmt.Errorf("reading metadata template: %w", err)
	}

	tmpl, err := template.New("metadata").Option("missingkey=error").Parse(string(content))
	if err != nil {
		return "", fmt.Errorf("parsing metadata template: %w", err)
	}

	temp, err := os.CreateTemp("", "rpu-processed-metadata-*.json")
	if err != nil {
		return "", fmt.Errorf("creating temp metadata file: %w", err)
	}

	err = tmpl.Execute(temp, vars)

	closeErr := temp.Close()
	if err != nil {
		_ = os.Remove(temp.Name())

		return "", fmt.Errorf("substituting metadata variables: %w", err)
	}

	if closeErr != nil {
		_ = os.Remove(temp.Name())

		return "", fmt.Errorf("closing temp metadata file: %w", closeErr)
	}

	return temp.Name(), nil
}

// CompilePropertyFilters compiles RE2 patterns for full-string property name matching.
func CompilePropertyFilters(patterns []string) ([]*regexp.Regexp, error) {
	return compilePropertyFilters(patterns)
}

func compilePropertyFilters(patterns []string) ([]*regexp.Regexp, error) {
	if len(patterns) == 0 {
		return nil, nil
	}

	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		re, err := compileFullStringPattern(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid property_filter pattern %q: %w", pattern, err)
		}

		compiled = append(compiled, re)
	}

	return compiled, nil
}

// compileFullStringPattern wraps pattern for Java String.matches() semantics (RE2 full-string match).
func validateCLIPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("invalid path %q", path)
	}

	if strings.Contains(path, "\x00") {
		return fmt.Errorf("invalid path %q", path)
	}

	return nil
}

func compileFullStringPattern(pattern string) (*regexp.Regexp, error) {
	re, err := regexp.Compile("^(?:" + pattern + ")$")
	if err != nil {
		return nil, fmt.Errorf("compiling property filter pattern: %w", err)
	}

	return re, nil
}

// PropertyNameMatches reports whether name matches any compiled filter pattern.
func PropertyNameMatches(compiled []*regexp.Regexp, name string) bool {
	if len(compiled) == 0 {
		return false
	}

	for _, re := range compiled {
		if re.MatchString(name) {
			return true
		}
	}

	return false
}

func parseLaunchAttributes(raw []map[string]any) ([]LaunchAttribute, WarningList) {
	var warnings WarningList

	attrs := make([]LaunchAttribute, 0, len(raw))

	for _, item := range raw {
		key, _ := item["key"].(string)
		value, _ := item["value"].(string)
		system, _ := item["system"].(bool)

		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			warnings.Add(
				"metadata",
				fmt.Sprintf("Skipping launch attribute with empty key or value: %v", item),
			)

			continue
		}

		attrs = append(attrs, LaunchAttribute{Key: key, Value: value, System: system})
	}

	return attrs, warnings
}

// InjectLaunchAttributes adds rpu-req-id and skippedIsNotIssue launch attributes.
func InjectLaunchAttributes(attrs []LaunchAttribute, requestID string) []LaunchAttribute {
	return injectLaunchAttributes(attrs, requestID)
}

func injectLaunchAttributes(attrs []LaunchAttribute, requestID string) []LaunchAttribute {
	out := make([]LaunchAttribute, 0, len(attrs)+2)
	out = append(out, attrs...)

	out = append(out, LaunchAttribute{Key: LaunchRequestIDAttrKey, Value: requestID})

	hasSkipped := false

	for _, attr := range out {
		if attr.Key == "skippedIsNotIssue" {
			hasSkipped = true

			break
		}
	}

	if !hasSkipped {
		out = append(out, LaunchAttribute{Key: "skippedIsNotIssue", Value: "true", System: true})
	}

	return out
}

// HostnameURL returns the Report Portal API base URL for a hostname value.
func HostnameURL(hostname string) string {
	return hostnameURL(hostname)
}

func hostnameURL(hostname string) string {
	trimmed := strings.TrimSpace(hostname)
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return strings.TrimSuffix(trimmed, "/") + "/"
	}

	return fmt.Sprintf("https://%s/", strings.TrimSuffix(trimmed, "/"))
}

// ValidateConcatChar reports whether testcase_concat_char is allowed.
func ValidateConcatChar(char string) error {
	return validateConcatChar(char)
}

func validateConcatChar(char string) error {
	if char == "" {
		return nil
	}

	if len([]rune(char)) > 1 {
		return fmt.Errorf("testcase_concat_char must be at most one character, got %q", char)
	}

	for _, forbidden := range []string{"<", ">", "\"", "?", "*", "'"} {
		if strings.Contains(char, forbidden) {
			return fmt.Errorf("testcase_concat_char contains forbidden character %q", forbidden)
		}
	}

	return nil
}

// BuildLaunchImportRQ converts metadata launch fields to the plugin import DTO.
func (meta *Metadata) BuildLaunchImportRQ() *types.LaunchImportRQ {
	mode := types.LaunchImportRQModeDEFAULT
	name := meta.LaunchName
	description := meta.LaunchDescription

	attrs := make([]types.ItemAttributesRQ, len(meta.LaunchAttributes))
	for i, attr := range meta.LaunchAttributes {
		key := attr.Key
		system := attr.System
		attrs[i] = types.ItemAttributesRQ{
			Key:    &key,
			Value:  attr.Value,
			System: &system,
		}
	}

	return &types.LaunchImportRQ{
		Name:        &name,
		Description: &description,
		Mode:        &mode,
		Attributes:  &attrs,
	}
}

// BuildUpdateLaunchRQ converts metadata launch fields to the launch update DTO.
func (meta *Metadata) BuildUpdateLaunchRQ() types.UpdateLaunchRQ {
	mode := types.UpdateLaunchRQModeDEFAULT
	description := meta.LaunchDescription

	attrs := make([]types.ItemAttributeResource, len(meta.LaunchAttributes))
	for i, attr := range meta.LaunchAttributes {
		key := attr.Key
		attrs[i] = types.ItemAttributeResource{
			Key:   &key,
			Value: attr.Value,
		}
	}

	return types.UpdateLaunchRQ{
		Description: &description,
		Mode:        &mode,
		Attributes:  &attrs,
	}
}

// LaunchUIURL returns the Report Portal UI URL for a launch.
func LaunchUIURL(meta *Metadata, launchID int64) string {
	host := strings.TrimSuffix(meta.HostnameURL, "/")

	return fmt.Sprintf("%s/ui/#%s/launches/all/%d", host, meta.Project, launchID)
}

// ExpandResultsGlob expands a results glob and returns absolute file paths.
func ExpandResultsGlob(pattern string) ([]string, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, errors.New("--results is required")
	}

	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("expanding results glob %q: %w", pattern, err)
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("results glob %q matched no files", pattern)
	}

	out := make([]string, 0, len(matches))
	for _, match := range matches {
		info, statErr := os.Stat(match)
		if statErr != nil {
			return nil, fmt.Errorf("stat results file %q: %w", match, statErr)
		}

		if !info.IsDir() {
			abs, err := filepath.Abs(match)
			if err != nil {
				return nil, fmt.Errorf("resolving results file %q: %w", match, err)
			}

			out = append(out, abs)
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("results glob %q matched no files", pattern)
	}

	return out, nil
}
