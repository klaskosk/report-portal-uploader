package upload

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	defaultLaneProperty   = "ci-lane"
	defaultExcludePattern = "*report_testrun*"
)

// Config is the TOML configuration for rpu upload.
type Config struct {
	ReportPortal ReportPortalConfig `toml:"report_portal"`
	Launch       LaunchConfig       `toml:"launch"`
	Sources      []SourceConfig     `toml:"sources"`
	Processing   ProcessingConfig   `toml:"processing"`
	Attachments  AttachmentsConfig  `toml:"attachments"`
	Output       OutputConfig       `toml:"output"`
}

// ReportPortalConfig holds the Report Portal endpoint settings.
type ReportPortalConfig struct {
	Hostname string `toml:"hostname"`
	Project  string `toml:"project"`
}

// LaunchConfig holds launch metadata from config.
type LaunchConfig struct {
	Name        string            `toml:"name"`
	Description string            `toml:"description"`
	Attributes  map[string]string `toml:"attributes"`
}

// SourceConfig is one [[sources]] entry.
type SourceConfig struct {
	Path string `toml:"path"`
	Lane string `toml:"lane"`
}

// ProcessingConfig holds XML processing options.
type ProcessingConfig struct {
	LaneProperty       string   `toml:"lane_property"`
	PropertyFilter     []string `toml:"property_filter"`
	TestCaseConcatChar string   `toml:"testcase_concat_char"`
	ExcludePatterns    []string `toml:"exclude_patterns"`
}

// AttachmentsConfig holds attachment directory settings.
type AttachmentsConfig struct {
	Directory string `toml:"directory"`
}

// OutputConfig holds post-upload artifact settings.
type OutputConfig struct {
	LaunchURLFile string `toml:"launch_url_file"`
}

// ParseConfigFile reads and validates a TOML config from path ("-" reads stdin).
func ParseConfigFile(path string) (*Config, error) {
	var reader io.Reader

	switch path {
	case "-":
		reader = os.Stdin
	case "":
		return nil, errors.New("--config is required")
	default:
		err := validateCLIPath(path)
		if err != nil {
			return nil, fmt.Errorf("opening config %q: %w", path, err)
		}

		file, err := os.Open(path) //nolint:gosec // path validated above
		if err != nil {
			return nil, fmt.Errorf("opening config %q: %w", path, err)
		}
		defer func() { _ = file.Close() }()

		reader = file
	}

	return ParseConfig(reader)
}

// ParseConfig decodes and validates TOML from reader.
func ParseConfig(reader io.Reader) (*Config, error) {
	var cfg Config

	_, err := toml.NewDecoder(reader).Decode(&cfg)
	if err != nil {
		return nil, fmt.Errorf("parsing config TOML: %w", err)
	}

	return ValidateConfig(&cfg)
}

// ValidateConfig applies defaults and validates a decoded config.
func ValidateConfig(cfg *Config) (*Config, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}

	cfg.ReportPortal.Hostname = strings.TrimSpace(cfg.ReportPortal.Hostname)
	cfg.ReportPortal.Project = strings.TrimSpace(cfg.ReportPortal.Project)
	cfg.Launch.Name = strings.TrimSpace(cfg.Launch.Name)

	if cfg.ReportPortal.Hostname == "" {
		return nil, errors.New("report_portal.hostname is required")
	}

	if cfg.ReportPortal.Project == "" {
		return nil, errors.New("report_portal.project is required")
	}

	if cfg.Launch.Name == "" {
		return nil, errors.New("launch.name is required")
	}

	if len(cfg.Sources) == 0 {
		return nil, errors.New("at least one [[sources]] entry is required")
	}

	for i, source := range cfg.Sources {
		if strings.TrimSpace(source.Path) == "" {
			return nil, fmt.Errorf("sources[%d].path is required", i)
		}
	}

	if cfg.Processing.LaneProperty == "" {
		cfg.Processing.LaneProperty = defaultLaneProperty
	}

	if len(cfg.Processing.ExcludePatterns) == 0 {
		cfg.Processing.ExcludePatterns = []string{defaultExcludePattern}
	}

	return cfg, nil
}

// EffectivePropertyFilter returns lane_property plus any extra property_filter patterns.
func (cfg *Config) EffectivePropertyFilter() []string {
	filter := make([]string, 0, 1+len(cfg.Processing.PropertyFilter))
	filter = append(filter, cfg.Processing.LaneProperty)
	filter = append(filter, cfg.Processing.PropertyFilter...)

	return filter
}

func validateCLIPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("invalid path %q", path)
	}

	if strings.Contains(path, "\x00") {
		return fmt.Errorf("invalid path %q", path)
	}

	return nil
}
